package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
)

type healthStoreStub struct{ err error }

func (s healthStoreStub) Ping(context.Context) error { return s.err }

func TestHealthReadinessReflectsRequiredChecks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		storeErr   error
		dependency error
		want       int
	}{
		{name: "ready", want: http.StatusOK},
		{name: "database unavailable", storeErr: errors.New("down"), want: http.StatusServiceUnavailable},
		{name: "dependency unavailable", dependency: errors.New("down"), want: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			app, err := New(&memoryNodes{nodes: map[string]domain.Node{}}, "01234567890123456789012345678901")
			if err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			app.RegisterHealth(mux, HealthOptions{Store: healthStoreStub{err: test.storeErr}, Dependencies: []HealthDependency{{Name: "routeros", Configured: true, Check: func(context.Context) error { return test.dependency }}}})
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/health/ready", nil))
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
