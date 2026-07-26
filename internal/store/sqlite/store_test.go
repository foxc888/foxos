package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
)

func TestNodeLifecycle(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil { t.Fatal(err) }
	defer store.Close()
	ctx := context.Background()
	node := domain.Node{ID:"node-1",Name:"HK-01",Type:"vless",Server:"example.com",Port:443,UUID:"00000000-0000-0000-0000-000000000001"}
	if err := store.SaveNode(ctx, node); err != nil { t.Fatal(err) }
	got, err := store.Node(ctx, node.ID)
	if err != nil || got.Name != node.Name { t.Fatalf("got=%+v err=%v", got, err) }
	got.Name = "HK-02"
	if err := store.SaveNode(ctx, got); err != nil { t.Fatal(err) }
	list, err := store.Nodes(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "HK-02" { t.Fatalf("list=%+v err=%v", list, err) }
	if err := store.DeleteNode(ctx, node.ID); err != nil { t.Fatal(err) }
	if _, err := store.Node(ctx, node.ID); !errors.Is(err, ErrNotFound) { t.Fatalf("err=%v", err) }
}
