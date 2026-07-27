package subscription

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/mihomo"
)

var ErrContentChanged = errors.New("subscription content changed after preview")

type RecoveryState string

const (
	RecoveryCompleted RecoveryState = "completed"
	RecoveryPreState  RecoveryState = "prestate"
	RecoveryPartial   RecoveryState = "partial"
)

type SourceStore interface {
	Subscription(context.Context, string) (domain.Subscription, error)
	UpdateSubscriptionResult(context.Context, string, string, string, bool) error
}

type NodeStore interface {
	SubscriptionNodes(context.Context, string) ([]domain.Node, error)
	ReplaceSubscriptionNodes(context.Context, string, []domain.Node) error
}

type RemoteFetcher interface {
	Fetch(context.Context, string) (Result, error)
}

type UpdatePlan struct {
	Action         string   `json:"action"`
	SubscriptionID string   `json:"subscriptionId"`
	Digest         string   `json:"digest"`
	NodeIDs        []string `json:"nodeIds"`
	RemovedNodeIDs []string `json:"removedNodeIds"`
	ExistingCount  int      `json:"existingCount"`
	NodeCount      int      `json:"nodeCount"`
	AddCount       int      `json:"addCount"`
	UpdateCount    int      `json:"updateCount"`
	RemoveCount    int      `json:"removeCount"`
}

type UpdatePreview struct {
	Plan  UpdatePlan
	Nodes []domain.Node
}

type Updater struct {
	Sources SourceStore
	Nodes   NodeStore
	Fetcher RemoteFetcher
	Parser  func([]byte) ([]domain.Node, error)
}

func (u Updater) Preview(ctx context.Context, id string) (UpdatePreview, error) {
	if u.Sources == nil || u.Nodes == nil || u.Fetcher == nil {
		return UpdatePreview{}, errors.New("subscription updater is not configured")
	}
	item, err := u.Sources.Subscription(ctx, id)
	if err != nil {
		return UpdatePreview{}, err
	}
	result, err := u.Fetcher.Fetch(ctx, item.URL)
	if err != nil {
		return UpdatePreview{}, err
	}
	parser := u.Parser
	if parser == nil {
		parser = parseNodes
	}
	parsed, err := parser(result.Body)
	if err != nil {
		return UpdatePreview{}, err
	}
	prepared, err := prepareNodes(item, parsed)
	if err != nil {
		return UpdatePreview{}, err
	}
	existing, err := u.Nodes.SubscriptionNodes(ctx, id)
	if err != nil {
		return UpdatePreview{}, err
	}
	return UpdatePreview{Plan: buildUpdatePlan(id, result.Digest, existing, prepared), Nodes: prepared}, nil
}

func (u Updater) Apply(ctx context.Context, id string, expected *UpdatePlan) (UpdatePreview, error) {
	return u.ApplyWithCheckpoint(ctx, id, expected, nil)
}

func (u Updater) ApplyWithCheckpoint(ctx context.Context, id string, expected *UpdatePlan, checkpoint func(string, UpdatePreview) error) (UpdatePreview, error) {
	preview, err := u.Preview(ctx, id)
	if err != nil {
		u.recordFailure(ctx, id, err)
		return UpdatePreview{}, err
	}
	if expected != nil && !EqualUpdatePlans(*expected, preview.Plan) {
		u.recordFailure(ctx, id, ErrContentChanged)
		return UpdatePreview{}, ErrContentChanged
	}
	if checkpoint != nil {
		if err := checkpoint("preview_verified", preview); err != nil {
			return UpdatePreview{}, err
		}
	}
	if err := u.Nodes.ReplaceSubscriptionNodes(ctx, id, preview.Nodes); err != nil {
		u.recordFailure(ctx, id, err)
		return UpdatePreview{}, err
	}
	if checkpoint != nil {
		if err := checkpoint("nodes_replaced", preview); err != nil {
			return UpdatePreview{}, err
		}
	}
	if err := u.Sources.UpdateSubscriptionResult(ctx, id, preview.Plan.Digest, "", true); err != nil {
		return UpdatePreview{}, err
	}
	if checkpoint != nil {
		if err := checkpoint("source_recorded", preview); err != nil {
			return UpdatePreview{}, err
		}
	}
	return preview, nil
}

// Reconcile proves which side of the subscription replacement transaction is
// visible after a crash. It may only repair the source result after proving
// that the complete desired node set is already present.
func (u Updater) Reconcile(ctx context.Context, id, expectedDigest, expectedPlanDigest string) (RecoveryState, UpdatePreview, error) {
	if expectedDigest == "" || expectedPlanDigest == "" {
		return RecoveryPartial, UpdatePreview{}, nil
	}
	preview, err := u.Preview(ctx, id)
	if err != nil {
		return RecoveryPartial, UpdatePreview{}, err
	}
	if !strings.EqualFold(preview.Plan.Digest, expectedDigest) {
		return RecoveryPartial, preview, nil
	}
	current, err := u.Nodes.SubscriptionNodes(ctx, id)
	if err != nil {
		return RecoveryPartial, preview, err
	}
	item, err := u.Sources.Subscription(ctx, id)
	if err != nil {
		return RecoveryPartial, preview, err
	}
	if equalNodeSets(current, preview.Nodes) {
		if !strings.EqualFold(item.LastDigest, expectedDigest) || item.LastError != "" {
			if err := u.Sources.UpdateSubscriptionResult(ctx, id, expectedDigest, "", true); err != nil {
				return RecoveryPartial, preview, err
			}
		}
		return RecoveryCompleted, preview, nil
	}
	if strings.EqualFold(item.LastDigest, expectedDigest) {
		return RecoveryPartial, preview, nil
	}
	if subtle.ConstantTimeCompare([]byte(UpdatePlanDigest(preview.Plan)), []byte(expectedPlanDigest)) == 1 {
		return RecoveryPreState, preview, nil
	}
	return RecoveryPartial, preview, nil
}

func EqualUpdatePlans(left, right UpdatePlan) bool {
	leftBody, leftErr := json.Marshal(left)
	rightBody, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil || len(leftBody) != len(rightBody) {
		return false
	}
	return subtle.ConstantTimeCompare(leftBody, rightBody) == 1
}

func UpdatePlanDigest(plan UpdatePlan) string {
	body, err := json.Marshal(plan)
	if err != nil {
		panic("subscription update plan contains unsupported data")
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func equalNodeSets(left, right []domain.Node) bool {
	if len(left) != len(right) {
		return false
	}
	left = append([]domain.Node(nil), left...)
	right = append([]domain.Node(nil), right...)
	sort.Slice(left, func(i, j int) bool { return left[i].ID < left[j].ID })
	sort.Slice(right, func(i, j int) bool { return right[i].ID < right[j].ID })
	for index := range left {
		if !reflect.DeepEqual(left[index], right[index]) {
			return false
		}
	}
	return true
}

func (u Updater) recordFailure(ctx context.Context, id string, cause error) {
	message := "subscription update failed"
	if errors.Is(cause, ErrContentChanged) {
		message = "subscription content changed after preview"
	}
	_ = u.Sources.UpdateSubscriptionResult(ctx, id, "", message, false)
}

func parseNodes(body []byte) ([]domain.Node, error) {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil, errors.New("subscription is empty")
	}
	return mihomo.ParseShareLinks(text)
}

func prepareNodes(item domain.Subscription, parsed []domain.Node) ([]domain.Node, error) {
	const maxSubscriptionNodes = 4096
	if len(parsed) > maxSubscriptionNodes {
		return nil, fmt.Errorf("subscription exceeds %d nodes", maxSubscriptionNodes)
	}
	prepared := make([]domain.Node, 0, len(parsed))
	seen := make(map[string][]domain.Node, len(parsed))
	names := make(map[string]struct{}, len(parsed))
	for _, node := range parsed {
		remoteName := strings.TrimSpace(node.Name)
		if remoteName == "" {
			remoteName = node.Server
		}
		node.ID = ""
		node.Name = ""
		node.SubscriptionID = ""
		body, err := json.Marshal(struct {
			SubscriptionID string `json:"subscriptionId"`
			Type           string `json:"type"`
			Server         string `json:"server"`
			Port           int    `json:"port"`
			Cipher         string `json:"cipher,omitempty"`
			Network        string `json:"network,omitempty"`
			SNI            string `json:"sni,omitempty"`
			Path           string `json:"path,omitempty"`
			Host           string `json:"host,omitempty"`
			TLS            bool   `json:"tls,omitempty"`
		}{SubscriptionID: item.ID, Type: node.Type, Server: node.Server, Port: node.Port, Cipher: node.Cipher, Network: node.Network, SNI: node.SNI, Path: node.Path, Host: node.Host, TLS: node.TLS})
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(body)
		fingerprint := hex.EncodeToString(digest[:])
		duplicate := false
		for _, previous := range seen[fingerprint] {
			if equivalentSubscriptionNode(previous, node) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		seen[fingerprint] = append(seen[fingerprint], node)
		node.ID = "sub-" + fingerprint[:24]
		if occurrence := len(seen[fingerprint]); occurrence > 1 {
			node.ID += "-" + strconv.Itoa(occurrence)
		}
		node.SubscriptionID = item.ID
		node.Name = strings.TrimSpace(item.Name) + " / " + remoteName
		nameKey := strings.ToLower(node.Name)
		if _, duplicate := names[nameKey]; duplicate {
			node.Name += " [" + fingerprint[:6] + "]"
			nameKey = strings.ToLower(node.Name)
		}
		names[nameKey] = struct{}{}
		if err := node.Validate(); err != nil {
			return nil, err
		}
		prepared = append(prepared, node)
	}
	if len(prepared) == 0 {
		return nil, errors.New("subscription has no supported nodes")
	}
	sort.Slice(prepared, func(i, j int) bool { return prepared[i].ID < prepared[j].ID })
	return prepared, nil
}

func equivalentSubscriptionNode(left, right domain.Node) bool {
	left.ID, right.ID = "", ""
	left.Name, right.Name = "", ""
	left.SubscriptionID, right.SubscriptionID = "", ""
	return reflect.DeepEqual(left, right)
}

func buildUpdatePlan(subscriptionID, digest string, existing, desired []domain.Node) UpdatePlan {
	plan := UpdatePlan{Action: "subscription.update", SubscriptionID: subscriptionID, Digest: digest, ExistingCount: len(existing), NodeCount: len(desired)}
	existingByID := make(map[string]domain.Node, len(existing))
	for _, node := range existing {
		existingByID[node.ID] = node
	}
	for _, node := range desired {
		plan.NodeIDs = append(plan.NodeIDs, node.ID)
		if previous, found := existingByID[node.ID]; !found {
			plan.AddCount++
		} else {
			previousBody, _ := json.Marshal(previous) // #nosec G117 -- compared only in memory and never returned or logged.
			nextBody, _ := json.Marshal(node)         // #nosec G117 -- compared only in memory and never returned or logged.
			if subtle.ConstantTimeCompare(previousBody, nextBody) != 1 {
				plan.UpdateCount++
			}
			delete(existingByID, node.ID)
		}
	}
	for id := range existingByID {
		plan.RemovedNodeIDs = append(plan.RemovedNodeIDs, id)
	}
	sort.Strings(plan.NodeIDs)
	sort.Strings(plan.RemovedNodeIDs)
	plan.RemoveCount = len(plan.RemovedNodeIDs)
	return plan
}
