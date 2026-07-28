package subscription

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/foxc888/foxos/internal/domain"
)

var (
	ErrContentChanged                     = errors.New("subscription content changed after preview")
	ErrPartialParseRequiresConfirmation   = errors.New("subscription contains skipped entries and requires an explicit preview")
	ErrLargeReductionRequiresConfirmation = errors.New("subscription node count dropped unexpectedly and requires an explicit preview")
)

type RecoveryState string

const (
	RecoveryCompleted RecoveryState = "completed"
	RecoveryPreState  RecoveryState = "prestate"
	RecoveryPartial   RecoveryState = "partial"
)

type SourceStore interface {
	Subscription(context.Context, string) (domain.Subscription, error)
	UpdateSubscriptionResult(context.Context, string, int64, string, string, bool) error
}

type NodeStore interface {
	SubscriptionNodes(context.Context, string) ([]domain.Node, error)
	ReplaceSubscriptionNodes(context.Context, string, []domain.Node) error
}

type AtomicStore interface {
	ApplySubscriptionUpdate(context.Context, string, []domain.Node, string, int64) error
}

type IdentityHasher func([]byte) ([sha256.Size]byte, error)

func NewIdentityHasher(key []byte) (IdentityHasher, error) {
	if len(key) < 32 {
		return nil, errors.New("subscription identity key must contain at least 32 bytes")
	}
	keyCopy := append([]byte(nil), key...)
	return func(body []byte) ([sha256.Size]byte, error) {
		mac := hmac.New(sha256.New, keyCopy)
		_, _ = mac.Write([]byte("foxos/subscription-node-id/v1\x00"))
		_, _ = mac.Write(body)
		var digest [sha256.Size]byte
		copy(digest[:], mac.Sum(nil))
		return digest, nil
	}, nil
}

type RemoteFetcher interface {
	Fetch(context.Context, string) (Result, error)
}

type UpdatePlan struct {
	Action              string       `json:"action"`
	SubscriptionID      string       `json:"subscriptionId"`
	Digest              string       `json:"digest"`
	NodeIDs             []string     `json:"nodeIds"`
	RemovedNodeIDs      []string     `json:"removedNodeIds"`
	ExistingCount       int          `json:"existingCount"`
	NodeCount           int          `json:"nodeCount"`
	AddCount            int          `json:"addCount"`
	UpdateCount         int          `json:"updateCount"`
	RemoveCount         int          `json:"removeCount"`
	ParseValidCount     int          `json:"parseValidCount"`
	ParseSkippedCount   int          `json:"parseSkippedCount"`
	ParseErrors         []ParseIssue `json:"parseErrors,omitempty"`
	SuspiciousReduction bool         `json:"suspiciousReduction"`
	SettingsRevision    int64        `json:"settingsRevision"`
}

type UpdatePreview struct {
	Plan     UpdatePlan
	Nodes    []domain.Node
	Previous []domain.Node
}

type Updater struct {
	Sources        SourceStore
	Nodes          NodeStore
	Atomic         AtomicStore
	Fetcher        RemoteFetcher
	Parser         func([]byte) ([]domain.Node, error)
	IdentityHasher IdentityHasher
}

func (u Updater) Preview(ctx context.Context, id string) (UpdatePreview, error) {
	preview, _, err := u.preview(ctx, id)
	return preview, err
}

func (u Updater) preview(ctx context.Context, id string) (UpdatePreview, int64, error) {
	if u.Sources == nil || u.Nodes == nil || u.Fetcher == nil || u.IdentityHasher == nil {
		return UpdatePreview{}, 0, errors.New("subscription updater is not configured")
	}
	item, err := u.Sources.Subscription(ctx, id)
	if err != nil {
		return UpdatePreview{}, 0, err
	}
	revision := item.SettingsRevision
	if revision < 1 {
		return UpdatePreview{}, revision, errors.New("subscription settings revision is invalid")
	}
	result, err := u.Fetcher.Fetch(ctx, item.URL)
	if err != nil {
		return UpdatePreview{}, revision, err
	}
	parser := u.Parser
	parseResult := ParseResult{Format: "custom"}
	var parsed []domain.Node
	if parser == nil {
		parseResult, err = ParseNodes(result.Body)
		parsed = parseResult.Nodes
	} else {
		parsed, err = parser(result.Body)
		parseResult.Nodes = parsed
	}
	if err != nil {
		return UpdatePreview{}, revision, err
	}
	existing, err := u.Nodes.SubscriptionNodes(ctx, id)
	if err != nil {
		return UpdatePreview{}, revision, err
	}
	prepared, err := prepareNodes(item, parsed, existing, u.IdentityHasher)
	if err != nil {
		return UpdatePreview{}, revision, err
	}
	plan := buildUpdatePlan(id, item.SettingsRevision, result.Digest, existing, prepared)
	plan.ParseValidCount = len(parseResult.Nodes)
	plan.ParseSkippedCount = parseResult.Skipped
	plan.ParseErrors = append([]ParseIssue(nil), parseResult.Errors...)
	plan.SuspiciousReduction = suspiciousSubscriptionReduction(len(existing), len(prepared))
	return UpdatePreview{Plan: plan, Nodes: prepared, Previous: append([]domain.Node(nil), existing...)}, revision, nil
}

func (u Updater) Apply(ctx context.Context, id string, expected *UpdatePlan) (UpdatePreview, error) {
	return u.ApplyWithCheckpoint(ctx, id, expected, nil)
}

func (u Updater) ApplyWithCheckpoint(ctx context.Context, id string, expected *UpdatePlan, checkpoint func(string, UpdatePreview) error) (UpdatePreview, error) {
	preview, revision, err := u.preview(ctx, id)
	if err != nil {
		return UpdatePreview{}, u.recordFailure(ctx, id, revision, err)
	}
	if expected != nil && expected.SettingsRevision != preview.Plan.SettingsRevision {
		return preview, domain.ErrSubscriptionSettingsStale
	}
	if expected != nil && !EqualUpdatePlans(*expected, preview.Plan) {
		return UpdatePreview{}, u.recordFailure(ctx, id, revision, ErrContentChanged)
	}
	if expected == nil && preview.Plan.ParseSkippedCount > 0 {
		return preview, u.recordFailure(ctx, id, revision, ErrPartialParseRequiresConfirmation)
	}
	if expected == nil && preview.Plan.SuspiciousReduction {
		return preview, u.recordFailure(ctx, id, revision, ErrLargeReductionRequiresConfirmation)
	}
	if checkpoint != nil {
		if err := checkpoint("preview_verified", preview); err != nil {
			return preview, err
		}
	}
	if u.Atomic == nil {
		return preview, errors.New("subscription updates require an atomic store")
	}
	if err := u.Atomic.ApplySubscriptionUpdate(ctx, id, preview.Nodes, preview.Plan.Digest, preview.Plan.SettingsRevision); err != nil {
		return preview, u.recordFailure(ctx, id, revision, err)
	}
	if checkpoint != nil {
		if err := checkpoint("state_committed", preview); err != nil {
			return preview, err
		}
	}
	return preview, nil
}

// Readback classifies the state visible after an interrupted apply without
// fetching the remote source again. It only reports the pre-state when the
// complete source-owned node set exactly matches the captured preview state.
func (u Updater) Readback(ctx context.Context, id string, preview UpdatePreview) (RecoveryState, error) {
	if preview.Plan.SubscriptionID != id || preview.Plan.Digest == "" || u.Sources == nil || u.Nodes == nil {
		return RecoveryPartial, errors.New("subscription readback requires a complete preview")
	}
	current, err := u.Nodes.SubscriptionNodes(ctx, id)
	if err != nil {
		return RecoveryPartial, err
	}
	item, err := u.Sources.Subscription(ctx, id)
	if err != nil {
		return RecoveryPartial, err
	}
	if item.SettingsRevision != preview.Plan.SettingsRevision {
		return RecoveryPartial, nil
	}
	if equalNodeSets(current, preview.Nodes) && strings.EqualFold(item.LastDigest, preview.Plan.Digest) && item.LastError == "" {
		return RecoveryCompleted, nil
	}
	if equalNodeSets(current, preview.Previous) && !strings.EqualFold(item.LastDigest, preview.Plan.Digest) {
		return RecoveryPreState, nil
	}
	return RecoveryPartial, nil
}

// Reconcile proves which side of the subscription replacement transaction is
// visible after a crash. It may only repair the source result after proving
// that the complete desired node set is already present.
func (u Updater) Reconcile(ctx context.Context, id, expectedDigest, expectedPlanDigest string, expectedSettingsRevision int64) (RecoveryState, UpdatePreview, error) {
	if expectedDigest == "" || expectedPlanDigest == "" || expectedSettingsRevision < 1 {
		return RecoveryPartial, UpdatePreview{}, nil
	}
	preview, err := u.Preview(ctx, id)
	if err != nil {
		return RecoveryPartial, UpdatePreview{}, err
	}
	if !strings.EqualFold(preview.Plan.Digest, expectedDigest) {
		return RecoveryPartial, preview, nil
	}
	if preview.Plan.SettingsRevision != expectedSettingsRevision {
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
			if err := u.Sources.UpdateSubscriptionResult(ctx, id, expectedSettingsRevision, expectedDigest, "", true); err != nil {
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

func (u Updater) recordFailure(ctx context.Context, id string, settingsRevision int64, cause error) error {
	if errors.Is(cause, domain.ErrSubscriptionSettingsStale) {
		return domain.ErrSubscriptionSettingsStale
	}
	if settingsRevision < 1 {
		return cause
	}
	message := "subscription update failed"
	if errors.Is(cause, ErrContentChanged) {
		message = "subscription content changed after preview"
	}
	if err := u.Sources.UpdateSubscriptionResult(ctx, id, settingsRevision, "", message, false); err != nil {
		if errors.Is(err, domain.ErrSubscriptionSettingsStale) {
			return domain.ErrSubscriptionSettingsStale
		}
		return fmt.Errorf("record subscription update failure: %w", err)
	}
	return cause
}

func suspiciousSubscriptionReduction(existing, desired int) bool {
	return existing >= 10 && existing-desired >= 5 && desired*2 < existing
}

func prepareNodes(item domain.Subscription, parsed, existing []domain.Node, identityHasher IdentityHasher) ([]domain.Node, error) {
	const maxSubscriptionNodes = 4096
	if len(parsed) > maxSubscriptionNodes {
		return nil, fmt.Errorf("subscription exceeds %d nodes", maxSubscriptionNodes)
	}
	if identityHasher == nil {
		return nil, errors.New("subscription identity hasher is required")
	}
	existingByFingerprint := make(map[string]domain.Node, len(existing))
	existingIDs := make(map[string]string, len(existing))
	for _, node := range existing {
		fingerprint, err := subscriptionNodeFingerprint(item.ID, node, identityHasher)
		if err != nil {
			return nil, err
		}
		if previous, duplicate := existingByFingerprint[fingerprint]; duplicate && !equivalentSubscriptionNode(previous, node) {
			return nil, errors.New("subscription identity HMAC collision in existing nodes")
		}
		existingByFingerprint[fingerprint] = node
		existingIDs[node.ID] = fingerprint
	}
	type preparedNode struct {
		node        domain.Node
		remoteName  string
		fingerprint string
	}
	candidates := make([]preparedNode, 0, len(parsed))
	seen := make(map[string]domain.Node, len(parsed))
	generatedIDs := make(map[string]string, len(parsed))
	for _, node := range parsed {
		remoteName := strings.TrimSpace(node.Name)
		if remoteName == "" {
			remoteName = node.Server
		}
		fingerprint, err := subscriptionNodeFingerprint(item.ID, node, identityHasher)
		if err != nil {
			return nil, err
		}
		if previous, duplicate := seen[fingerprint]; duplicate {
			if equivalentSubscriptionNode(previous, node) {
				continue
			}
			return nil, errors.New("subscription identity HMAC collision")
		}
		seen[fingerprint] = node
		if previous, found := existingByFingerprint[fingerprint]; found {
			if !equivalentSubscriptionNode(previous, node) {
				return nil, errors.New("subscription identity HMAC collision with existing node")
			}
			node.ID = previous.ID
		} else {
			node.ID = "sub-" + fingerprint[:24]
			if previousFingerprint, collision := existingIDs[node.ID]; collision && previousFingerprint != fingerprint {
				return nil, errors.New("subscription node ID collides with a legacy node")
			}
			if previousFingerprint, collision := generatedIDs[node.ID]; collision && previousFingerprint != fingerprint {
				return nil, errors.New("subscription node ID HMAC prefix collision")
			}
			generatedIDs[node.ID] = fingerprint
		}
		node.SubscriptionID = item.ID
		candidates = append(candidates, preparedNode{node: node, remoteName: remoteName, fingerprint: fingerprint})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].node.ID < candidates[j].node.ID })
	prepared := make([]domain.Node, 0, len(candidates))
	names := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		node := candidate.node
		node.Name = strings.TrimSpace(item.Name) + " / " + candidate.remoteName
		nameKey := strings.ToLower(node.Name)
		if _, duplicate := names[nameKey]; duplicate {
			node.Name += " [" + candidate.fingerprint[:6] + "]"
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
	return prepared, nil
}

func subscriptionNodeFingerprint(subscriptionID string, node domain.Node, identityHasher IdentityHasher) (string, error) {
	normalized := normalizedSubscriptionNode(node)
	body, err := json.Marshal(struct {
		SubscriptionID string      `json:"subscriptionId"`
		Node           domain.Node `json:"node"`
	}{SubscriptionID: subscriptionID, Node: normalized}) // #nosec G117 -- keyed HMAC input remains process-local.
	if err != nil {
		return "", err
	}
	digest, err := identityHasher(body)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(digest[:]), nil
}

func normalizedSubscriptionNode(node domain.Node) domain.Node {
	node.ID = ""
	node.Name = ""
	node.SubscriptionID = ""
	node.Type = strings.ToLower(strings.TrimSpace(node.Type))
	node.Server = strings.ToLower(strings.TrimSpace(node.Server))
	node.Cipher = strings.ToLower(strings.TrimSpace(node.Cipher))
	node.Network = strings.ToLower(strings.TrimSpace(node.Network))
	node.SNI = strings.ToLower(strings.TrimSpace(node.SNI))
	node.Host = strings.ToLower(strings.TrimSpace(node.Host))
	return node
}

func equivalentSubscriptionNode(left, right domain.Node) bool {
	return reflect.DeepEqual(normalizedSubscriptionNode(left), normalizedSubscriptionNode(right))
}

func buildUpdatePlan(subscriptionID string, settingsRevision int64, digest string, existing, desired []domain.Node) UpdatePlan {
	plan := UpdatePlan{Action: "subscription.update", SubscriptionID: subscriptionID, SettingsRevision: settingsRevision, Digest: digest, ExistingCount: len(existing), NodeCount: len(desired)}
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
