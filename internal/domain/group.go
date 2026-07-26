package domain

import (
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidGroup = errors.New("invalid proxy group")

type Group struct {
	ID        string
	Name      string
	Type      string
	NodeIDs   []string
	GroupIDs  []string
	URL       string
	Interval  int
	Tolerance int
	Strategy  string
}

func (g Group) Validate() error {
	if strings.TrimSpace(g.ID) == "" || strings.TrimSpace(g.Name) == "" {
		return fmt.Errorf("%w: id and name are required", ErrInvalidGroup)
	}
	switch g.Type {
	case "select", "url-test", "fallback", "load-balance":
	default:
		return fmt.Errorf("%w: unsupported type %q", ErrInvalidGroup, g.Type)
	}
	if len(g.NodeIDs)+len(g.GroupIDs) == 0 {
		return fmt.Errorf("%w: at least one member is required", ErrInvalidGroup)
	}
	if (g.Type == "url-test" || g.Type == "fallback" || g.Type == "load-balance") && strings.TrimSpace(g.URL) == "" {
		return fmt.Errorf("%w: health check url is required", ErrInvalidGroup)
	}
	return nil
}
