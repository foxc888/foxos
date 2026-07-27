package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/routeros"
)

type containerCommandReaderStub struct {
	container routeros.Container
}

type containerCommandJobsStub struct {
	calls   int
	kind    string
	request map[string]any
}

func (s *containerCommandJobsStub) Submit(_ context.Context, kind, _ string, request map[string]any) (domain.Job, error) {
	s.calls++
	s.kind = kind
	s.request = request
	return domain.Job{ID: "job-container-command", Kind: kind, Status: domain.JobQueued, Request: request}, nil
}

func (s containerCommandReaderStub) ManagedContainer(_ context.Context, id, owner string) (routeros.Container, error) {
	if id != s.container.ID || owner != s.container.Comment {
		return routeros.Container{}, routeros.ErrContainerNotManaged
	}
	return s.container, nil
}

func TestContainerCommandRequiresOwnedReadbackAndIdempotencyKey(t *testing.T) {
	t.Parallel()
	const token = "01234567890123456789012345678901"
	app, _ := New(&memoryNodes{nodes: map[string]domain.Node{}}, token)
	jobs := &containerCommandJobsStub{}
	mux := http.NewServeMux()
	app.RegisterContainerCommands(mux, containerCommandReaderStub{container: routeros.Container{ID: "*1", Comment: "foxos:active", Status: "running"}}, jobs)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/routeros/containers/*1/commands/restart", strings.NewReader(`{"owner":"foxos:active","idempotencyKey":"container-command-0123456789"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || jobs.calls != 1 || jobs.kind != "routeros.container-command" || jobs.request["containerId"] != "*1" || jobs.request["command"] != "restart" {
		t.Fatalf("status=%d body=%s jobs=%+v", response.Code, response.Body.String(), jobs)
	}

	unsafe := httptest.NewRequest(http.MethodPost, "/api/v1/routeros/containers/*1/commands/start", strings.NewReader(`{"owner":"user-container","idempotencyKey":"container-command-0123456789"}`))
	unsafe.Header.Set("Authorization", "Bearer "+token)
	unsafeResponse := httptest.NewRecorder()
	mux.ServeHTTP(unsafeResponse, unsafe)
	if unsafeResponse.Code != http.StatusConflict || jobs.calls != 1 {
		t.Fatalf("status=%d body=%s calls=%d", unsafeResponse.Code, unsafeResponse.Body.String(), jobs.calls)
	}
}
