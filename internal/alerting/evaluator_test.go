package alerting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/mihomo"
	"github.com/foxc888/foxos/internal/mosdns"
	"github.com/foxc888/foxos/internal/routeros"
	"github.com/foxc888/foxos/internal/store/sqlite"
)

type fakeRouter struct {
	resource   routeros.Resource
	routes     []routeros.Route
	containers []routeros.Container
	err        error
}

func (f *fakeRouter) Resource(context.Context) (routeros.Resource, error) { return f.resource, f.err }
func (f *fakeRouter) Routes(context.Context) ([]routeros.Route, error)    { return f.routes, f.err }
func (f *fakeRouter) Containers(context.Context) ([]routeros.Container, error) {
	return f.containers, f.err
}

type fakeMihomo struct {
	status mihomo.RuntimeStatus
	err    error
}

func (f *fakeMihomo) Status(context.Context) (mihomo.RuntimeStatus, error) { return f.status, f.err }

type fakeMosDNS struct{ status mosdns.Status }

func (f *fakeMosDNS) Status(context.Context) mosdns.Status { return f.status }

func TestEvaluatorCoversOperationalSignalsAndResolvesRecovery(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	router := &fakeRouter{
		resource:   routeros.Resource{FreeHDD: "67108864", TotalHDD: "10737418240"},
		containers: []routeros.Container{{ID: "*1", Name: "foxos", Comment: "foxos:container:foxos", Status: "stopped"}},
	}
	clash := &fakeMihomo{status: mihomo.RuntimeStatus{Proxies: map[string]any{
		"Node A": map[string]any{"type": "VLESS", "alive": false},
		"GLOBAL": map[string]any{"type": "Selector", "alive": false},
	}}}
	dns := &fakeMosDNS{status: mosdns.Status{Configured: true, Online: false}}
	failedJob, _, err := store.CreateJob(ctx, domain.Job{ID: "job-failed", Kind: "mihomo.apply", Status: domain.JobFailed, ErrorClass: "mihomo_apply_failed"})
	if err != nil {
		t.Fatal(err)
	}
	rolledBackJob, _, err := store.CreateJob(ctx, domain.Job{ID: "job-rollback", Kind: "routeros.egress", Status: domain.JobRolledBack, ErrorClass: "egress_rolled_back"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	if err := store.SaveSubscription(ctx, domain.Subscription{ID: "source-a", Name: "Primary", URL: "https://example.com/nodes", Enabled: true, Interval: 300, CreatedAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	evaluator := Evaluator{Store: store, RouterOS: router, Mihomo: clash, MosDNS: dns, Now: func() time.Time { return now }}
	for index := 0; index < 3; index++ {
		now = now.Add(time.Minute)
		if err := evaluator.Evaluate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	alerts, err := store.Alerts(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	keys := make(map[string]bool, len(alerts))
	for _, alert := range alerts {
		keys[alert.Key] = true
	}
	for _, key := range []string{
		"wan.default_route",
		"disk.routeros.low",
		"dns.mosdns.offline",
		"config.apply_failed.mihomo_apply",
		"config.apply_failed.routeros_egress",
		"config.auto_rollback.routeros_egress",
	} {
		if !keys[key] {
			t.Errorf("missing alert %s in %+v", key, keys)
		}
	}
	if !hasPrefix(keys, "container.offline.") || !hasPrefix(keys, "node.failure.") || !hasPrefix(keys, "subscription.expired.") {
		t.Fatalf("missing container, node, or subscription alert: %+v", keys)
	}

	router.routes = []routeros.Route{{Dst: "0.0.0.0/0", Gateway: "10.0.0.1", Active: "true", Disabled: "false"}}
	router.containers[0].Status = "running"
	router.resource = routeros.Resource{FreeHDD: "8589934592", TotalHDD: "10737418240"}
	clash.status.Proxies["Node A"] = map[string]any{"type": "VLESS", "alive": true}
	dns.status.Online = true
	subscription, err := store.Subscription(ctx, "source-a")
	if err != nil {
		t.Fatal(err)
	}
	subscription.LastSuccessAt = now
	if err := store.SaveSubscription(ctx, subscription); err != nil {
		t.Fatal(err)
	}
	failedJob.Status = domain.JobSucceeded
	rolledBackJob.Status = domain.JobSucceeded
	if err := store.UpdateJob(ctx, failedJob); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateJob(ctx, rolledBackJob); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if err := evaluator.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	alerts, err = store.Alerts(ctx, false)
	if err != nil || len(alerts) != 0 {
		t.Fatalf("active alerts=%+v err=%v", alerts, err)
	}
}

func TestEvaluatorDoesNotCallTelemetryFailureAWANOutage(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	evaluator := Evaluator{Store: store, RouterOS: &fakeRouter{err: errors.New("unavailable")}}
	if err := evaluator.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	alerts, err := store.Alerts(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, alert := range alerts {
		if alert.Key == "wan.default_route" || stringsHasPrefix(alert.Key, "container.offline.") || alert.Key == "disk.routeros.low" {
			t.Fatalf("telemetry failure produced outage alert: %+v", alert)
		}
	}
	if len(alerts) != 3 {
		t.Fatalf("telemetry alerts=%+v", alerts)
	}
}

func hasPrefix(values map[string]bool, prefix string) bool {
	for value := range values {
		if stringsHasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func stringsHasPrefix(value, prefix string) bool {
	return len(value) >= len(prefix) && value[:len(prefix)] == prefix
}
