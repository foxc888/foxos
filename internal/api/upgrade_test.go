package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/upgrade"
)

type fakeUpgradeService struct {
	checkpoint upgrade.Checkpoint
	created    int
	promoted   int
}

func (s *fakeUpgradeService) Create(_ context.Context, operationID string) (upgrade.Checkpoint, error) {
	s.created++
	s.checkpoint.OperationID = operationID
	s.checkpoint.Status = "checkpoint_ready"
	return s.checkpoint, nil
}

func (s *fakeUpgradeService) MarkPromoted(operationID string) (upgrade.Checkpoint, error) {
	s.promoted++
	s.checkpoint.OperationID = operationID
	s.checkpoint.Status = "promoted"
	return s.checkpoint, nil
}

func TestUpgradeCheckpointAndPromoteAPI(t *testing.T) {
	const token = "01234567890123456789012345678901"
	app, err := New(&memoryNodes{nodes: map[string]domain.Node{}}, token)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 27, 1, 2, 3, 0, time.UTC)
	service := &fakeUpgradeService{checkpoint: upgrade.Checkpoint{SourceVersion: "v1", SchemaVersion: 4, CreatedAt: now, UpdatedAt: now}}
	audit := &fakeAudit{}
	mux := http.NewServeMux()
	app.RegisterUpgrade(mux, service, audit)
	for _, path := range []string{"/api/v1/system/upgrade/checkpoint", "/api/v1/system/upgrade/promoted"} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"operationId":"*A"}`))
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"operationId":"*A"`) {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	if service.created != 1 || service.promoted != 1 || len(audit.events) != 2 {
		t.Fatalf("created=%d promoted=%d audits=%d", service.created, service.promoted, len(audit.events))
	}
}

func TestUpgradeAPIRequiresAuthentication(t *testing.T) {
	const token = "01234567890123456789012345678901"
	app, err := New(&memoryNodes{nodes: map[string]domain.Node{}}, token)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	app.RegisterUpgrade(mux, &fakeUpgradeService{}, &fakeAudit{})
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/system/upgrade/checkpoint", strings.NewReader(`{"operationId":"*A"}`)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
}
