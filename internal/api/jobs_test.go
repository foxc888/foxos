package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/foxc888/foxos/internal/domain"
)

type listJobManager struct {
	items []domain.Job
}

func (m *listJobManager) Job(context.Context, string) (domain.Job, error) {
	return domain.Job{}, domain.ErrNotFound
}

func (m *listJobManager) Jobs(_ context.Context, limit int) ([]domain.Job, error) {
	if limit < len(m.items) {
		return m.items[:limit], nil
	}
	return m.items, nil
}

func (m *listJobManager) Retry(context.Context, string) (domain.Job, error) {
	return domain.Job{}, domain.ErrNotFound
}

func TestJobListOmitsRequestAndIdempotencyMaterial(t *testing.T) {
	t.Parallel()
	const token = "01234567890123456789012345678901"
	app, err := New(&memoryNodes{nodes: map[string]domain.Node{}}, token)
	if err != nil {
		t.Fatal(err)
	}
	manager := &listJobManager{items: []domain.Job{{
		ID: "job-1", Kind: "subscription.update", Status: domain.JobFailed, Progress: 100,
		IdempotencyKey: "secret-derived-material", Request: map[string]any{"url": "https://user:password@example.invalid"},
		Result: map[string]any{"phase": "readback_failed"}, ErrorClass: "subscription_update_failed",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}}}
	mux := http.NewServeMux()
	app.RegisterJobs(mux, manager)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs?limit=10", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"phase":"readback_failed"`) ||
		strings.Contains(response.Body.String(), "secret-derived-material") || strings.Contains(response.Body.String(), "password") || strings.Contains(response.Body.String(), `"request"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestJobListRejectsUnboundedLimit(t *testing.T) {
	t.Parallel()
	const token = "01234567890123456789012345678901"
	app, _ := New(&memoryNodes{nodes: map[string]domain.Node{}}, token)
	mux := http.NewServeMux()
	app.RegisterJobs(mux, &listJobManager{})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs?limit=501", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
