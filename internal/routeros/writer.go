package routeros

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var ErrWriteVerification = errors.New("RouterOS write verification failed")

func (c *Client) Apply(ctx context.Context, operation Operation) error {
	if err := validateBindingOperation(operation); err != nil {
		return err
	}
	body, err := json.Marshal(operation.Body)
	if err != nil {
		return err
	}
	target := *c.base
	target.Path = strings.TrimRight(c.base.Path, "/") + operation.Path
	target.RawQuery = ""
	request, err := http.NewRequestWithContext(ctx, operation.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.SetBasicAuth(c.username, c.password)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	// #nosec G704 -- the base URL is a validated private IP literal and the
	// operation path has passed the RouterOS binding allowlist.
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return errors.New("RouterOS authentication failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("RouterOS write status %d", response.StatusCode)
	}
	return nil
}

func (c *Client) Verify(ctx context.Context, plan Plan) error {
	leases, err := c.Leases(ctx)
	if err != nil {
		return err
	}
	for _, operation := range plan.Operations {
		wantMAC := normalizeMAC(operation.Body["mac-address"])
		wantIP := operation.Body["address"]
		wantComment := operation.OwnedComment
		matched := false
		for _, lease := range leases {
			if normalizeMAC(lease.MACAddress) == wantMAC && lease.Address == wantIP && lease.Dynamic != "true" && lease.Comment == wantComment {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%w: lease %s %s", ErrWriteVerification, wantMAC, wantIP)
		}
	}
	return nil
}

// Compensate restores only the FoxOS-owned resources touched by a plan. PUT
// operations are removed by matching the ownership marker after a failed
// verification; PATCH operations use the rollback operation embedded in the
// signed plan.
func (c *Client) Compensate(ctx context.Context, operations []Operation) error {
	for index := len(operations) - 1; index >= 0; index-- {
		operation := operations[index]
		if operation.Rollback != nil {
			if err := c.Apply(ctx, *operation.Rollback); err != nil {
				return err
			}
			continue
		}
		if operation.Method != http.MethodPut {
			continue
		}
		leases, err := c.Leases(ctx)
		if err != nil {
			return err
		}
		for _, lease := range leases {
			if lease.Comment != operation.OwnedComment || normalizeMAC(lease.MACAddress) != normalizeMAC(operation.Body["mac-address"]) || lease.Address != operation.Body["address"] {
				continue
			}
			if !safeRouterOSID(lease.ID) {
				return errors.New("invalid RouterOS lease id during compensation")
			}
			if err := c.deleteLease(ctx, lease.ID, operation.OwnedComment); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Client) deleteLease(ctx context.Context, id, owner string) error {
	if !safeRouterOSID(id) || !strings.HasPrefix(owner, "foxos:") {
		return ErrUnsafeOperation
	}
	target := *c.base
	target.Path = strings.TrimRight(c.base.Path, "/") + "/rest/ip/dhcp-server/lease/" + id
	target.RawQuery = ""
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, target.String(), nil)
	if err != nil {
		return err
	}
	request.SetBasicAuth(c.username, c.password)
	request.Header.Set("Accept", "application/json")
	// #nosec G704 -- the base URL is a validated private IP literal and the
	// lease path contains only a validated RouterOS resource ID.
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("RouterOS compensation status %d", response.StatusCode)
	}
	return nil
}

func (c *Client) ApplyEgress(ctx context.Context, operation EgressOperation) error {
	if err := ValidateEgressOperation(operation); err != nil {
		return err
	}
	if operation.Method == http.MethodPatch || operation.Method == http.MethodDelete {
		if err := c.verifyEgressOwnership(ctx, operation.Path, operation.OwnedComment); err != nil {
			return err
		}
	}
	return c.writeOwned(ctx, operation.Method, operation.Path, operation.Body)
}

func (c *Client) verifyEgressOwnership(ctx context.Context, path, owner string) error {
	base, id, ok := egressPath(path)
	if !ok || id == "" || !strings.HasPrefix(owner, "foxos:") {
		return ErrUnsafeOperation
	}
	items, err := c.readOwnedCollection(ctx, base)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item[".id"] == id {
			if item["comment"] != owner {
				return fmt.Errorf("%w: RouterOS resource ownership changed", ErrUnsafeOperation)
			}
			return nil
		}
	}
	return fmt.Errorf("%w: RouterOS resource no longer exists", ErrWriteVerification)
}

func (c *Client) VerifyEgress(ctx context.Context, plan EgressPlan) error {
	for _, operation := range plan.Operations {
		base, id, ok := egressPath(operation.Path)
		if !ok {
			return ErrUnsafeOperation
		}
		items, err := c.readOwnedCollection(ctx, base)
		if err != nil {
			return err
		}
		if operation.Method == http.MethodDelete {
			for _, item := range items {
				if item[".id"] == id && item["comment"] == operation.OwnedComment {
					return fmt.Errorf("%w: deleted resource %s still exists", ErrWriteVerification, id)
				}
			}
			continue
		}
		matched := false
		for _, item := range items {
			if item["comment"] != operation.OwnedComment {
				continue
			}
			if id != "" && item[".id"] != id {
				continue
			}
			if sameManagedFields(base, item, operation.Body) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%w: %s", ErrWriteVerification, operation.OwnedComment)
		}
	}
	return nil
}

func (c *Client) CompensateEgress(ctx context.Context, operations []EgressOperation) error {
	for index := len(operations) - 1; index >= 0; index-- {
		operation := operations[index]
		if operation.Rollback != nil {
			if err := c.ApplyEgress(ctx, *operation.Rollback); err != nil {
				return err
			}
			continue
		}
		items, err := c.readOwnedCollection(ctx, operation.Path)
		if err != nil {
			return err
		}
		for _, item := range items {
			if item["comment"] != operation.OwnedComment || !safeRouterOSID(item[".id"]) {
				continue
			}
			if err := c.writeOwned(ctx, http.MethodDelete, operation.Path+"/"+item[".id"], map[string]string{}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Client) writeOwned(ctx context.Context, method, path string, body map[string]string) error {
	if !allowedEgressPath(path, method) {
		return ErrUnsafeOperation
	}
	var encoded []byte
	var err error
	if method != http.MethodDelete {
		encoded, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	target := *c.base
	target.Path = strings.TrimRight(c.base.Path, "/") + path
	target.RawQuery = ""
	request, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.SetBasicAuth(c.username, c.password)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	// #nosec G704 -- the base URL is a validated private IP literal and
	// allowedEgressPath constrains the complete write target.
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("RouterOS egress write status %d", response.StatusCode)
	}
	return nil
}

func (c *Client) readOwnedCollection(ctx context.Context, path string) ([]map[string]string, error) {
	if !allowedEgressPath(path, http.MethodGet) {
		return nil, ErrUnsafeOperation
	}
	var output []map[string]string
	if err := c.get(ctx, path, &output); err != nil {
		return nil, err
	}
	return output, nil
}
