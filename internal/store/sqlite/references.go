package sqlite

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/foxc888/foxos/internal/domain"
)

func (s *Store) NodeReferences(ctx context.Context, id string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,payload_json FROM proxy_groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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
	rows, err := s.db.QueryContext(ctx, `SELECT id,payload_json FROM proxy_groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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
		if policy.TargetID == id && policy.Egress == domain.EgressProxyChain {
			refs = append(refs, "device-policy:"+policyID)
		}
	}
	return refs, policyRows.Err()
}

func (s *Store) ValidateDevicePolicyTarget(ctx context.Context, policy domain.DevicePolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	switch policy.Egress {
	case domain.EgressMihomoNode:
		if _, err := s.Node(ctx, policy.TargetID); err != nil {
			return fmt.Errorf("Mihomo node target is unavailable: %w", err)
		}
	case domain.EgressProxyChain:
		group, err := s.Group(ctx, policy.TargetID)
		if err != nil {
			return fmt.Errorf("proxy group target is unavailable: %w", err)
		}
		if group.Type != "chain" {
			return fmt.Errorf("proxy-chain target %q is not a chain group", policy.TargetID)
		}
	}
	return nil
}
