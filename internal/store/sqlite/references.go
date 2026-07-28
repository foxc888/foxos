package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/foxc888/foxos/internal/domain"
)

var ErrReferenceConflict = errors.New("resource is referenced")

// ReferenceError carries the exact database objects that prevented an atomic
// delete or type change. Callers may present these identifiers without
// repeating a separate, racy reference query.
type ReferenceError struct {
	Resource   string
	References []string
}

func (e *ReferenceError) Error() string {
	if e == nil {
		return ErrReferenceConflict.Error()
	}
	return fmt.Sprintf("%s is referenced by %s", e.Resource, strings.Join(e.References, ", "))
}

func (e *ReferenceError) Unwrap() error { return ErrReferenceConflict }

func (s *Store) NodeReferences(ctx context.Context, id string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,payload_json FROM proxy_groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	refs := make([]string, 0)
	for rows.Next() {
		var groupID, payload string
		if err := rows.Scan(&groupID, &payload); err != nil {
			return nil, err
		}
		var group domain.Group
		if err := json.Unmarshal([]byte(payload), &group); err != nil {
			return nil, err
		}
		for _, nodeID := range group.NodeIDs {
			if nodeID == id {
				refs = append(refs, "proxy-group:"+groupID)
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	policyRows, err := s.db.QueryContext(ctx, `SELECT id,payload_json FROM device_policies ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer policyRows.Close()
	for policyRows.Next() {
		var policyID, payload string
		if err := policyRows.Scan(&policyID, &payload); err != nil {
			return nil, err
		}
		var policy domain.DevicePolicy
		if err := json.Unmarshal([]byte(payload), &policy); err != nil {
			return nil, err
		}
		if policy.TargetID == id && policy.Egress == domain.EgressMihomoNode {
			refs = append(refs, "device-policy:"+policyID)
		}
	}
	return refs, policyRows.Err()
}

func (s *Store) GroupReferences(ctx context.Context, id string) ([]string, error) {
	return groupReferences(ctx, s.db, id)
}

type referenceQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func groupReferences(ctx context.Context, queryer referenceQueryer, id string) ([]string, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT id,payload_json FROM proxy_groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	refs := make([]string, 0)
	for rows.Next() {
		var groupID, payload string
		if err := rows.Scan(&groupID, &payload); err != nil {
			return nil, err
		}
		if groupID == id {
			continue
		}
		var group domain.Group
		if err := json.Unmarshal([]byte(payload), &group); err != nil {
			return nil, err
		}
		for _, groupIDRef := range group.GroupIDs {
			if groupIDRef == id {
				refs = append(refs, "proxy-group:"+groupID)
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	policyRows, err := queryer.QueryContext(ctx, `SELECT id,payload_json FROM device_policies ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer policyRows.Close()
	for policyRows.Next() {
		var policyID, payload string
		if err := policyRows.Scan(&policyID, &payload); err != nil {
			return nil, err
		}
		var policy domain.DevicePolicy
		if err := json.Unmarshal([]byte(payload), &policy); err != nil {
			return nil, err
		}
		if policy.TargetID == id && policy.Egress == domain.EgressProxyChain {
			refs = append(refs, "device-policy:"+policyID)
		}
	}
	if err := policyRows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(refs)
	return refs, nil
}

func proxyChainPolicyReferences(ctx context.Context, queryer referenceQueryer, id string) ([]string, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT id,payload_json FROM device_policies ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := make([]string, 0)
	for rows.Next() {
		var policyID, payload string
		if err := rows.Scan(&policyID, &payload); err != nil {
			return nil, err
		}
		var policy domain.DevicePolicy
		if err := json.Unmarshal([]byte(payload), &policy); err != nil {
			return nil, err
		}
		if policy.Egress == domain.EgressProxyChain && policy.TargetID == id {
			refs = append(refs, "device-policy:"+policyID)
		}
	}
	return refs, rows.Err()
}

func (s *Store) ValidateDevicePolicyTarget(ctx context.Context, policy domain.DevicePolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	return validateDevicePolicyTarget(ctx, s.db, policy)
}

func validateDevicePolicyTarget(ctx context.Context, queryer referenceQueryer, policy domain.DevicePolicy) error {
	switch policy.Egress {
	case domain.EgressMihomoNode:
		var exists int
		if err := queryer.QueryRowContext(ctx, `SELECT 1 FROM nodes WHERE id=?`, policy.TargetID).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				err = ErrNotFound
			}
			return fmt.Errorf("Mihomo node target is unavailable: %w", err)
		}
	case domain.EgressProxyChain:
		var payload string
		err := queryer.QueryRowContext(ctx, `SELECT payload_json FROM proxy_groups WHERE id=?`, policy.TargetID).Scan(&payload)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				err = ErrNotFound
			}
			return fmt.Errorf("proxy group target is unavailable: %w", err)
		}
		var group domain.Group
		if err := json.Unmarshal([]byte(payload), &group); err != nil {
			return fmt.Errorf("decode proxy group target: %w", err)
		}
		if group.Type != "chain" {
			return fmt.Errorf("proxy-chain target %q is not a chain group", policy.TargetID)
		}
	}
	return nil
}
