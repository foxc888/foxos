package sqlite

import (
	"context"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
)

func TestGroupLifecycle(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	group := domain.Group{ID: "group-1", Name: "Proxy", Type: "select", NodeIDs: []string{"node-1"}}
	if err := store.SaveGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	got, err := store.Group(ctx, group.ID)
	if err != nil || got.Name != "Proxy" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	got.Name = "Main Proxy"
	if err := store.SaveGroup(ctx, got); err != nil {
		t.Fatal(err)
	}
	groups, err := store.Groups(ctx)
	if err != nil || len(groups) != 1 || groups[0].Name != "Main Proxy" {
		t.Fatalf("groups=%+v err=%v", groups, err)
	}
	if err := store.DeleteGroup(ctx, group.ID); err != nil {
		t.Fatal(err)
	}
}
