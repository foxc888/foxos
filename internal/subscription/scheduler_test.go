package subscription

import (
	"context"
	"testing"
	"time"

	"github.com/foxc888/foxos/internal/domain"
)

type scheduleStore []domain.Subscription

func (s scheduleStore) Subscriptions(context.Context) ([]domain.Subscription, error) { return s, nil }

type scheduleJobs struct {
	items []domain.Job
}

func (s *scheduleJobs) Submit(_ context.Context, kind, key string, request map[string]any) (domain.Job, error) {
	job := domain.Job{ID: key, Kind: kind, IdempotencyKey: key, Request: request}
	s.items = append(s.items, job)
	return job, nil
}

func TestEnqueueDueSchedulesOnlyEnabledDueSources(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	store := scheduleStore{
		{ID: "due", Enabled: true, Interval: 300, LastAttemptAt: now.Add(-301 * time.Second)},
		{ID: "not-due", Enabled: true, Interval: 300, LastAttemptAt: now.Add(-100 * time.Second)},
		{ID: "disabled", Enabled: false, Interval: 300, LastAttemptAt: now.Add(-time.Hour)},
	}
	jobs := &scheduleJobs{}
	queued, err := EnqueueDue(context.Background(), store, jobs, now)
	if err != nil {
		t.Fatal(err)
	}
	if queued != 1 || len(jobs.items) != 1 || jobs.items[0].Request["subscriptionId"] != "due" || jobs.items[0].Request["scheduled"] != true {
		t.Fatalf("queued=%d jobs=%+v", queued, jobs.items)
	}
}
