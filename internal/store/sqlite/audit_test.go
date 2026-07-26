package sqlite

import (
	"context"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
)

func TestAuditLifecycle(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	event := domain.AuditEvent{ID: "audit-1", Action: "routeros.device-binding", TargetID: "phone", Outcome: domain.AuditStarted, Details: map[string]any{"operationCount": 1}}
	if err := store.SaveAudit(ctx, event); err != nil {
		t.Fatal(err)
	}
	event.Outcome = domain.AuditSucceeded
	if err := store.SaveAudit(ctx, event); err != nil {
		t.Fatal(err)
	}
	events, err := store.AuditEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Outcome != domain.AuditSucceeded {
		t.Fatalf("events=%+v", events)
	}
}
