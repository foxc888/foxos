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

var ErrSubscriptionNodesReferenced = errors.New("subscription nodes are referenced")

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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := replaceSubscriptionNodesTx(ctx, tx, subscriptionID, nodes); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteSubscriptionWithNodes(ctx context.Context, subscriptionID string) error {
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
	if err := replaceSubscriptionNodesTx(ctx, tx, subscriptionID, nil); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM subscriptions WHERE id=?`, subscriptionID); err != nil {
		return err
	}
	return tx.Commit()
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
