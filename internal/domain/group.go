package domain

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

var ErrInvalidGroup = errors.New("invalid proxy group")

const (
	maxGroupMembers = 256
	maxChainHops    = 16
)

type Group struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	NodeIDs   []string `json:"nodeIds"`
	GroupIDs  []string `json:"groupIds,omitempty"`
	URL       string   `json:"url,omitempty"`
	Interval  int      `json:"interval,omitempty"`
	Tolerance int      `json:"tolerance,omitempty"`
	Strategy  string   `json:"strategy,omitempty"`
}

func (g Group) Validate() error {
	if !validGroupText(g.ID, 128) || !validGroupText(g.Name, 128) || g.ID == "" || g.Name == "" || strings.ContainsAny(g.Name, ",\r\n") {
		return fmt.Errorf("%w: id and name are required", ErrInvalidGroup)
	}
	switch g.Type {
	case "select", "url-test", "fallback", "load-balance", "chain":
	default:
		return fmt.Errorf("%w: unsupported type %q", ErrInvalidGroup, g.Type)
	}
	members := len(g.NodeIDs) + len(g.GroupIDs)
	if members == 0 {
		return fmt.Errorf("%w: at least one member is required", ErrInvalidGroup)
	}
	if members > maxGroupMembers {
		return fmt.Errorf("%w: group exceeds %d members", ErrInvalidGroup, maxGroupMembers)
	}
	if err := validateUniqueMembers(g); err != nil {
		return err
	}
	if g.Type == "chain" {
		if len(g.NodeIDs) < 2 || len(g.NodeIDs) > maxChainHops || len(g.GroupIDs) != 0 {
			return fmt.Errorf("%w: chain requires 2 to %d ordered nodes and cannot contain groups", ErrInvalidGroup, maxChainHops)
		}
		if g.URL != "" || g.Interval != 0 || g.Tolerance != 0 || g.Strategy != "" {
			return fmt.Errorf("%w: chain does not accept health-check or balancing options", ErrInvalidGroup)
		}
		return nil
	}
	if (g.Type == "url-test" || g.Type == "fallback" || g.Type == "load-balance") && strings.TrimSpace(g.URL) == "" {
		return fmt.Errorf("%w: health check url is required", ErrInvalidGroup)
	}
	if g.URL != "" {
		target, err := url.Parse(g.URL)
		if len(g.URL) > 2048 || strings.TrimSpace(g.URL) != g.URL || err != nil || !target.IsAbs() || target.Hostname() == "" || target.User != nil || (target.Scheme != "http" && target.Scheme != "https") {
			return fmt.Errorf("%w: health check url must be an HTTP(S) URL without credentials", ErrInvalidGroup)
		}
	}
	if g.Interval < 0 || g.Interval > 86400 || (g.Interval > 0 && g.Interval < 10) {
		return fmt.Errorf("%w: health check interval must be 0 or between 10 and 86400 seconds", ErrInvalidGroup)
	}
	if g.Tolerance < 0 || g.Tolerance > 10000 {
		return fmt.Errorf("%w: tolerance must be between 0 and 10000 milliseconds", ErrInvalidGroup)
	}
	if (g.Interval != 0 || g.Tolerance != 0) && g.URL == "" {
		return fmt.Errorf("%w: health check options require a URL", ErrInvalidGroup)
	}
	if g.Tolerance != 0 && g.Type != "url-test" {
		return fmt.Errorf("%w: tolerance is only valid for url-test groups", ErrInvalidGroup)
	}
	if g.Type == "load-balance" {
		switch g.Strategy {
		case "", "consistent-hashing", "round-robin", "sticky-sessions":
		default:
			return fmt.Errorf("%w: unsupported load-balance strategy", ErrInvalidGroup)
		}
	} else if g.Strategy != "" {
		return fmt.Errorf("%w: strategy is only valid for load-balance groups", ErrInvalidGroup)
	}
	return nil
}

func validateUniqueMembers(group Group) error {
	seenNodes := make(map[string]struct{}, len(group.NodeIDs))
	for _, id := range group.NodeIDs {
		if !validGroupText(id, 128) || id == "" {
			return fmt.Errorf("%w: node member id is required", ErrInvalidGroup)
		}
		if _, exists := seenNodes[id]; exists {
			return fmt.Errorf("%w: duplicate node member %q", ErrInvalidGroup, id)
		}
		seenNodes[id] = struct{}{}
	}
	seenGroups := make(map[string]struct{}, len(group.GroupIDs))
	for _, id := range group.GroupIDs {
		if !validGroupText(id, 128) || id == "" || id == group.ID {
			return fmt.Errorf("%w: invalid group member %q", ErrInvalidGroup, id)
		}
		if _, exists := seenGroups[id]; exists {
			return fmt.Errorf("%w: duplicate group member %q", ErrInvalidGroup, id)
		}
		seenGroups[id] = struct{}{}
	}
	return nil
}

func validGroupText(value string, limit int) bool {
	return strings.TrimSpace(value) == value && len(value) <= limit && !strings.ContainsFunc(value, unicode.IsControl)
}
