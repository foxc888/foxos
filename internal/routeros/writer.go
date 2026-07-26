package routeros

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return errors.New("RouterOS authentication failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("RouterOS write status %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
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
