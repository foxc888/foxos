package subscription

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/foxc888/foxos/internal/domain"
)

type ScheduleStore interface {
	Subscriptions(context.Context) ([]domain.Subscription, error)
}

type JobSubmitter interface {
	Submit(context.Context, string, string, map[string]any) (domain.Job, error)
}

type Scheduler struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewScheduler(parent context.Context, store ScheduleStore, jobs JobSubmitter, interval time.Duration) (*Scheduler, error) {
	if store == nil || jobs == nil {
		return nil, errors.New("subscription scheduler dependencies are required")
	}
	if parent == nil {
		parent = context.Background()
	}
	if interval <= 0 {
		interval = time.Minute
	}
	ctx, cancel := context.WithCancel(parent)
	if _, err := EnqueueDue(ctx, store, jobs, time.Now().UTC()); err != nil {
		cancel()
		return nil, err
	}
	scheduler := &Scheduler{cancel: cancel}
	scheduler.wg.Add(1)
	go func() {
		defer scheduler.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				_, _ = EnqueueDue(ctx, store, jobs, now.UTC())
			}
		}
	}()
	return scheduler, nil
}

func (s *Scheduler) Close() error {
	if s == nil {
		return nil
	}
	s.cancel()
	s.wg.Wait()
	return nil
}

func EnqueueDue(ctx context.Context, store ScheduleStore, jobs JobSubmitter, now time.Time) (int, error) {
	if store == nil || jobs == nil {
		return 0, errors.New("subscription scheduler dependencies are required")
	}
	items, err := store.Subscriptions(ctx)
	if err != nil {
		return 0, err
	}
	queued := 0
	for _, item := range items {
		if !item.Enabled || item.Interval < 300 {
			continue
		}
		reference := item.LastAttemptAt
		if reference.IsZero() {
			reference = item.CreatedAt
		}
		if reference.IsZero() {
			reference = item.UpdatedAt
		}
		dueAt := reference.Add(time.Duration(item.Interval) * time.Second)
		if reference.IsZero() {
			dueAt = now
		}
		if now.Before(dueAt) {
			continue
		}
		key := fmt.Sprintf("%s:%s", item.ID, strconv.FormatInt(dueAt.Unix(), 10))
		if _, err := jobs.Submit(ctx, "subscription.update", key, map[string]any{"subscriptionId": item.ID, "scheduled": true}); err != nil {
			return queued, err
		}
		queued++
	}
	return queued, nil
}
