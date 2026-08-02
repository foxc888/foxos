package sqlite

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
)

func TestNodeDeleteAndGroupCreateAreAtomic(t *testing.T) {
	t.Parallel()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	node := domain.Node{ID: "node-race", Name: "Node race", Type: "http", Server: "node.example", Port: 8080}
	if err := store.SaveNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	group := domain.Group{ID: "group-race", Name: "Group race", Type: "select", NodeIDs: []string{node.ID}}

	deleteErr, createErr := runConcurrently(
		func() error { return store.DeleteNode(ctx, node.ID) },
		func() error { return store.CreateGroup(ctx, group) },
	)
	if (deleteErr == nil) == (createErr == nil) {
		t.Fatalf("delete error=%v create error=%v, want exactly one success", deleteErr, createErr)
	}
	_, nodeErr := store.Node(ctx, node.ID)
	_, groupErr := store.Group(ctx, group.ID)
	if groupErr == nil && errors.Is(nodeErr, ErrNotFound) {
		t.Fatal("group committed with a dangling node reference")
	}
}

func TestConcurrentGroupUpdatesCannotCommitCycle(t *testing.T) {
	t.Parallel()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	node := domain.Node{ID: "node-cycle", Name: "Node cycle", Type: "http", Server: "node.example", Port: 8080}
	if err := store.SaveNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	for _, group := range []domain.Group{
		{ID: "group-a", Name: "Group A", Type: "select", NodeIDs: []string{node.ID}},
		{ID: "group-b", Name: "Group B", Type: "select", NodeIDs: []string{node.ID}},
	} {
		if err := store.SaveGroup(ctx, group); err != nil {
			t.Fatal(err)
		}
	}
	a := domain.Group{ID: "group-a", Name: "Group A", Type: "select", NodeIDs: []string{node.ID}, GroupIDs: []string{"group-b"}}
	b := domain.Group{ID: "group-b", Name: "Group B", Type: "select", NodeIDs: []string{node.ID}, GroupIDs: []string{"group-a"}}

	aErr, bErr := runConcurrently(
		func() error { return store.UpdateGroup(ctx, a) },
		func() error { return store.UpdateGroup(ctx, b) },
	)
	if (aErr == nil) == (bErr == nil) {
		t.Fatalf("group-a error=%v group-b error=%v, want exactly one success", aErr, bErr)
	}
	storedA, err := store.Group(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	storedB, err := store.Group(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if containsString(storedA.GroupIDs, storedB.ID) && containsString(storedB.GroupIDs, storedA.ID) {
		t.Fatal("concurrent group updates committed a reference cycle")
	}
}

func TestGroupDeleteAndProxyChainPolicySaveAreAtomic(t *testing.T) {
	t.Parallel()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for _, node := range []domain.Node{
		{ID: "entry", Name: "Entry", Type: "http", Server: "entry.example", Port: 8080},
		{ID: "exit", Name: "Exit", Type: "socks5", Server: "exit.example", Port: 1080},
	} {
		if err := store.SaveNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	chain := domain.Group{ID: "chain-race", Name: "Chain race", Type: "chain", NodeIDs: []string{"entry", "exit"}}
	if err := store.SaveGroup(ctx, chain); err != nil {
		t.Fatal(err)
	}
	policy := domain.DevicePolicy{ID: "phone", Name: "Phone", MACAddress: "AA:BB:CC:DD:EE:FF", StaticIP: "192.168.1.20", DHCPServer: "dhcp-lan", Egress: domain.EgressProxyChain, TargetID: chain.ID}

	deleteErr, saveErr := runConcurrently(
		func() error { return store.DeleteGroup(ctx, chain.ID) },
		func() error { return store.SaveDevicePolicy(ctx, policy) },
	)
	if (deleteErr == nil) == (saveErr == nil) {
		t.Fatalf("delete error=%v save error=%v, want exactly one success", deleteErr, saveErr)
	}
	_, groupErr := store.Group(ctx, chain.ID)
	_, policyErr := store.DevicePolicy(ctx, policy.ID)
	if policyErr == nil && errors.Is(groupErr, ErrNotFound) {
		t.Fatal("device policy committed with a dangling proxy-chain reference")
	}
}

func TestReferencedProxyChainCannotChangeType(t *testing.T) {
	t.Parallel()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for _, node := range []domain.Node{
		{ID: "entry", Name: "Entry", Type: "http", Server: "entry.example", Port: 8080},
		{ID: "exit", Name: "Exit", Type: "socks5", Server: "exit.example", Port: 1080},
	} {
		if err := store.SaveNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	chain := domain.Group{ID: "chain-used", Name: "Chain used", Type: "chain", NodeIDs: []string{"entry", "exit"}}
	if err := store.SaveGroup(ctx, chain); err != nil {
		t.Fatal(err)
	}
	policy := domain.DevicePolicy{ID: "phone", Name: "Phone", MACAddress: "AA:BB:CC:DD:EE:FF", StaticIP: "192.168.1.20", DHCPServer: "dhcp-lan", Egress: domain.EgressProxyChain, TargetID: chain.ID}
	if err := store.SaveDevicePolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	changed := chain
	changed.Type = "select"
	if err := store.UpdateGroup(ctx, changed); !errors.Is(err, ErrReferenceConflict) {
		t.Fatalf("type change error=%v, want reference conflict", err)
	}
	stored, err := store.Group(ctx, chain.ID)
	if err != nil || stored.Type != "chain" {
		t.Fatalf("stored group=%+v error=%v", stored, err)
	}
}

func runConcurrently(first, second func() error) (error, error) {
	start := make(chan struct{})
	results := make(chan struct {
		index int
		err   error
	}, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for index, operation := range []func() error{first, second} {
		go func(index int, operation func() error) {
			ready.Done()
			<-start
			results <- struct {
				index int
				err   error
			}{index: index, err: operation()}
		}(index, operation)
	}
	ready.Wait()
	close(start)
	errorsByIndex := make([]error, 2)
	for range 2 {
		result := <-results
		errorsByIndex[result.index] = result.err
	}
	return errorsByIndex[0], errorsByIndex[1]
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
