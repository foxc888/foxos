package subscription

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/mihomo"
)

var ErrContentChanged = errors.New("subscription content changed after preview")

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
	preview, err := u.Preview(ctx, id)
	if err != nil {
		u.recordFailure(ctx, id, err)
		return UpdatePreview{}, err
	}
	if expected != nil && !EqualUpdatePlans(*expected, preview.Plan) {
		u.recordFailure(ctx, id, ErrContentChanged)
		return UpdatePreview{}, ErrContentChanged
	}
	if err := u.Nodes.ReplaceSubscriptionNodes(ctx, id, preview.Nodes); err != nil {
		u.recordFailure(ctx, id, err)
		return UpdatePreview{}, err
	}
	if err := u.Sources.UpdateSubscriptionResult(ctx, id, preview.Plan.Digest, "", true); err != nil {
		return UpdatePreview{}, err
	}
	return preview, nil
}

func EqualUpdatePlans(left, right UpdatePlan) bool {
	leftBody, leftErr := json.Marshal(left)
	rightBody, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil || len(leftBody) != len(rightBody) {
		return false
	}
	return subtle.ConstantTimeCompare(leftBody, rightBody) == 1
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
	prepared := make([]domain.Node, 0, len(parsed))
	seen := make(map[string]struct{}, len(parsed))
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
			SubscriptionID string      `json:"subscriptionId"`
			Node           domain.Node `json:"node"`
		}{SubscriptionID: item.ID, Node: node})
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(body)
		fingerprint := hex.EncodeToString(digest[:])
		if _, duplicate := seen[fingerprint]; duplicate {
			continue
		}
		seen[fingerprint] = struct{}{}
		node.ID = "sub-" + fingerprint[:24]
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
