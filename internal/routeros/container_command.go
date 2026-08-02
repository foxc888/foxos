package routeros

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

var (
	ErrContainerNotManaged = errors.New("RouterOS container is not managed by FoxOS")
	ErrContainerState      = errors.New("RouterOS container state does not allow the command")
)

func managedContainerOwner(value string) bool {
	switch strings.TrimSpace(value) {
	case "foxos:active", "foxos:pending", "foxos:rollback", "foxos:failed", "foxos:mihomo", "foxos:mosdns":
		return true
	default:
		return false
	}
}

func (c *Client) ManagedContainer(ctx context.Context, id, owner string) (Container, error) {
	if !safeRouterOSID(id) || !managedContainerOwner(owner) {
		return Container{}, ErrContainerNotManaged
	}
	items, err := c.Containers(ctx)
	if err != nil {
		return Container{}, err
	}
	var found *Container
	for index := range items {
		if items[index].ID != id || items[index].Comment != owner {
			continue
		}
		if found != nil {
			return Container{}, ErrContainerNotManaged
		}
		copy := items[index]
		found = &copy
	}
	if found == nil {
		return Container{}, ErrContainerNotManaged
	}
	return *found, nil
}

func (c *Client) StartContainer(ctx context.Context, id, owner string) error {
	container, err := c.ManagedContainer(ctx, id, owner)
	if err != nil {
		return err
	}
	switch strings.ToLower(container.Status) {
	case "running":
		return nil
	case "stopped":
		return c.writeBindingOperation(ctx, http.MethodPost, "/rest/container/start", map[string]string{".id": id})
	default:
		return ErrContainerState
	}
}

func (c *Client) StopContainer(ctx context.Context, id, owner string) error {
	container, err := c.ManagedContainer(ctx, id, owner)
	if err != nil {
		return err
	}
	switch strings.ToLower(container.Status) {
	case "stopped":
		return nil
	case "running":
		return c.writeBindingOperation(ctx, http.MethodPost, "/rest/container/stop", map[string]string{".id": id})
	default:
		return ErrContainerState
	}
}
