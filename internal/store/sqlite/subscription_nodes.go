package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/foxc888/foxos/internal/domain"
)

var (
	ErrSubscriptionNodesReferenced = errors.New("subscription nodes are referenced")
	ErrSubscriptionDeletePlanStale = errors.New("subscription delete plan is stale")
)

func (s *Store) SubscriptionNodes(ctx context.Context, subscriptionID string) ([]domain.Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM nodes ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.Node, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var node domain.Node
		if err := json.Unmarshal([]byte(payload), &node); err != nil {
			return nil, err
		}
		if node.SubscriptionID == subscriptionID {
			items = append(items, node)
		}
	}
	return items, rows.Err()
}

func (s *Store) ReplaceSubscriptionNodes(ctx context.Context, subscriptionID string, nodes []domain.Node) error {
	if err := validateSubscriptionNodes(subscriptionID, nodes); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := replaceSubscriptionNodesTx(ctx, tx.Tx, subscriptionID, nodes); err != nil {
		return err
	}
	return tx.Commit()
}

// ApplySubscriptionUpdate commits the complete source-owned node set and the
// source success metadata together. A crash can expose either the old state or
// the complete new state, never new nodes with a stale source digest.
func (s *Store) ApplySubscriptionUpdate(ctx context.Context, subscriptionID string, nodes []domain.Node, digest string, settingsRevision int64) error {
	if err := validateSubscriptionNodes(subscriptionID, nodes); err != nil {
		return err
	}
	if strings.TrimSpace(digest) == "" {
		return errors.New("subscription digest is required")
	}
	if settingsRevision < 1 {
		return errors.New("subscription settings revision is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	lockResult, err := tx.ExecContext(ctx, `UPDATE subscriptions SET settings_revision=settings_revision WHERE id=? AND settings_revision=?`, subscriptionID, settingsRevision)
	if err != nil {
		return err
	}
	locked, err := lockResult.RowsAffected()
	if err != nil {
		return err
	}
	if locked != 1 {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM subscriptions WHERE id=?`, subscriptionID).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		return domain.ErrSubscriptionSettingsStale
	}
	if err := replaceSubscriptionNodesTx(ctx, tx.Tx, subscriptionID, nodes); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `
		UPDATE subscriptions
		SET last_digest=?,last_success_at=?,last_attempt_at=?,last_error='',updated_at=?
		WHERE id=? AND settings_revision=?
	`, digest, now, now, now, subscriptionID, settingsRevision)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return domain.ErrSubscriptionSettingsStale
	}
	return tx.Commit()
}

func validateSubscriptionNodes(subscriptionID string, nodes []domain.Node) error {
	if strings.TrimSpace(subscriptionID) == "" {
		return errors.New("subscription id is required")
	}
	for _, node := range nodes {
		if node.SubscriptionID != subscriptionID {
			return errors.New("node subscription ownership mismatch")
		}
		if err := node.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteSubscriptionWithNodes(ctx context.Context, subscriptionID string, expectedNodeIDs []string) error {
	if strings.TrimSpace(subscriptionID) == "" {
		return errors.New("subscription id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM subscriptions WHERE id=?`, subscriptionID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := verifySubscriptionNodeSetTx(ctx, tx.Tx, subscriptionID, expectedNodeIDs); err != nil {
		return err
	}
	if err := replaceSubscriptionNodesTx(ctx, tx.Tx, subscriptionID, nil); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM subscriptions WHERE id=?`, subscriptionID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteSubscriptionPreservingNodes detaches every source-owned node and then
// removes the source in one transaction. Existing group and policy references
// remain valid because node IDs do not change.
func (s *Store) DeleteSubscriptionPreservingNodes(ctx context.Context, subscriptionID string, expectedNodeIDs []string) error {
	if strings.TrimSpace(subscriptionID) == "" {
		return errors.New("subscription id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM subscriptions WHERE id=?`, subscriptionID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	nodes, err := subscriptionNodesTx(ctx, tx.Tx, subscriptionID)
	if err != nil {
		return err
	}
	if !matchingNodeIDs(nodes, expectedNodeIDs) {
		return ErrSubscriptionDeletePlanStale
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, node := range nodes {
		node.SubscriptionID = ""
		body, err := json.Marshal(node) // #nosec G117 -- credentials remain in the mode-0600 local database.
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE nodes SET payload_json=?,updated_at=? WHERE id=?`, string(body), now, node.ID)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			if err != nil {
				return err
			}
			return errors.New("subscription node changed during detach")
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM subscriptions WHERE id=?`, subscriptionID); err != nil {
		return err
	}
	return tx.Commit()
}

func verifySubscriptionNodeSetTx(ctx context.Context, tx *sql.Tx, subscriptionID string, expectedNodeIDs []string) error {
	nodes, err := subscriptionNodesTx(ctx, tx, subscriptionID)
	if err != nil {
		return err
	}
	if !matchingNodeIDs(nodes, expectedNodeIDs) {
		return ErrSubscriptionDeletePlanStale
	}
	return nil
}

func matchingNodeIDs(nodes []domain.Node, expected []string) bool {
	if len(nodes) != len(expected) {
		return false
	}
	actual := make([]string, 0, len(nodes))
	for _, node := range nodes {
		actual = append(actual, node.ID)
	}
	expectedCopy := append([]string(nil), expected...)
	sort.Strings(actual)
	sort.Strings(expectedCopy)
	for index := range actual {
		if actual[index] != expectedCopy[index] {
			return false
		}
	}
	return true
}

func replaceSubscriptionNodesTx(ctx context.Context, tx *sql.Tx, subscriptionID string, nodes []domain.Node) error {
	existing, err := subscriptionNodesTx(ctx, tx, subscriptionID)
	if err != nil {
		return err
	}
	desired := make(map[string]domain.Node, len(nodes))
	names := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if _, duplicate := desired[node.ID]; duplicate {
			return errors.New("duplicate subscription node id")
		}
		nameKey := strings.ToLower(strings.TrimSpace(node.Name))
		if _, duplicate := names[nameKey]; duplicate {
			return errors.New("duplicate subscription node name")
		}
		desired[node.ID] = node
		names[nameKey] = struct{}{}
		var existingPayload string
		err := tx.QueryRowContext(ctx, `SELECT payload_json FROM nodes WHERE id=?`, node.ID).Scan(&existingPayload)
		if err == nil {
			var owner domain.Node
			if err := json.Unmarshal([]byte(existingPayload), &owner); err != nil {
				return err
			}
			if owner.SubscriptionID != subscriptionID {
				return errors.New("subscription node id collides with another owner")
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	removed := make(map[string]struct{})
	for _, node := range existing {
		if _, keep := desired[node.ID]; !keep {
			removed[node.ID] = struct{}{}
		}
	}
	if len(removed) > 0 {
		references, err := nodeReferencesTx(ctx, tx, removed)
		if err != nil {
			return err
		}
		if len(references) > 0 {
			return fmt.Errorf("%w: %s", ErrSubscriptionNodesReferenced, strings.Join(references, ", "))
		}
	}
	for id := range removed {
		if _, err := tx.ExecContext(ctx, `DELETE FROM nodes WHERE id=?`, id); err != nil {
			return err
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, node := range nodes {
		body, err := json.Marshal(node) // #nosec G117 -- credentials are required runtime state in the mode-0600 local database; API output uses a redacted type.
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO nodes(id,name,type,payload_json,created_at,updated_at)
			VALUES(?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET name=excluded.name,type=excluded.type,payload_json=excluded.payload_json,updated_at=excluded.updated_at
		`, node.ID, node.Name, node.Type, string(body), now, now); err != nil {
			return err
		}
	}
	return nil
}

func subscriptionNodesTx(ctx context.Context, tx *sql.Tx, subscriptionID string) ([]domain.Node, error) {
	rows, err := tx.QueryContext(ctx, `SELECT payload_json FROM nodes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.Node, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var node domain.Node
		if err := json.Unmarshal([]byte(payload), &node); err != nil {
			return nil, err
		}
		if node.SubscriptionID == subscriptionID {
			items = append(items, node)
		}
	}
	return items, rows.Err()
}

func nodeReferencesTx(ctx context.Context, tx *sql.Tx, nodeIDs map[string]struct{}) ([]string, error) {
	references := make([]string, 0)
	groupRows, err := tx.QueryContext(ctx, `SELECT id,payload_json FROM proxy_groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for groupRows.Next() {
		var id, payload string
		if err := groupRows.Scan(&id, &payload); err != nil {
			_ = groupRows.Close()
			return nil, err
		}
		var group domain.Group
		if err := json.Unmarshal([]byte(payload), &group); err != nil {
			_ = groupRows.Close()
			return nil, err
		}
		for _, nodeID := range group.NodeIDs {
			if _, found := nodeIDs[nodeID]; found {
				references = append(references, "proxy-group:"+id+"->"+nodeID)
			}
		}
	}
	if err := groupRows.Close(); err != nil {
		return nil, err
	}
	policyRows, err := tx.QueryContext(ctx, `SELECT id,payload_json FROM device_policies ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer policyRows.Close()
	for policyRows.Next() {
		var id, payload string
		if err := policyRows.Scan(&id, &payload); err != nil {
			return nil, err
		}
		var policy domain.DevicePolicy
		if err := json.Unmarshal([]byte(payload), &policy); err != nil {
			return nil, err
		}
		if policy.Egress == domain.EgressMihomoNode {
			if _, found := nodeIDs[policy.TargetID]; found {
				references = append(references, "device-policy:"+id+"->"+policy.TargetID)
			}
		}
	}
	sort.Strings(references)
	return references, policyRows.Err()
}
