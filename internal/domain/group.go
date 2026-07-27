package domain

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

var ErrInvalidGroup = errors.New("invalid proxy group")

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
	if strings.TrimSpace(g.ID) == "" || strings.TrimSpace(g.Name) == "" {
		return fmt.Errorf("%w: id and name are required", ErrInvalidGroup)
	}
	switch g.Type {
	case "select", "url-test", "fallback", "load-balance", "chain":
	default:
		return fmt.Errorf("%w: unsupported type %q", ErrInvalidGroup, g.Type)
	}
	if len(g.NodeIDs)+len(g.GroupIDs) == 0 {
		return fmt.Errorf("%w: at least one member is required", ErrInvalidGroup)
	}
	if err := validateUniqueMembers(g); err != nil {
		return err
	}
	if g.Type == "chain" {
		if len(g.NodeIDs) < 2 || len(g.GroupIDs) != 0 {
			return fmt.Errorf("%w: chain requires at least two ordered nodes and cannot contain groups", ErrInvalidGroup)
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
		if err != nil || target.Host == "" || target.User != nil || (target.Scheme != "http" && target.Scheme != "https") {
			return fmt.Errorf("%w: health check url must be an HTTP(S) URL without credentials", ErrInvalidGroup)
		}
	}
	return nil
}

func validateUniqueMembers(group Group) error {
	seenNodes := make(map[string]struct{}, len(group.NodeIDs))
	for _, id := range group.NodeIDs {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("%w: node member id is required", ErrInvalidGroup)
		}
		if _, exists := seenNodes[id]; exists {
			return fmt.Errorf("%w: duplicate node member %q", ErrInvalidGroup, id)
		}
		seenNodes[id] = struct{}{}
	}
	seenGroups := make(map[string]struct{}, len(group.GroupIDs))
	for _, id := range group.GroupIDs {
		if strings.TrimSpace(id) == "" || id == group.ID {
			return fmt.Errorf("%w: invalid group member %q", ErrInvalidGroup, id)
		}
		if _, exists := seenGroups[id]; exists {
			return fmt.Errorf("%w: duplicate group member %q", ErrInvalidGroup, id)
		}
		seenGroups[id] = struct{}{}
	}
	return nil
}
