package upgrade

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMutationGateDrainsRequestsAndKeepsControlPlaneAvailable(t *testing.T) {
	gate := NewMutationGate()
	started := make(chan struct{})
	release := make(chan struct{})
	handler := gate.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/work" && r.Method == http.MethodPost {
			close(started)
			<-release
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	done := make(chan int, 1)
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/work", nil))
		done <- response.Code
	}()
	<-started
	frozen := make(chan error, 1)
	go func() { frozen <- gate.Freeze(context.Background()) }()
	select {
	case err := <-frozen:
		t.Fatalf("freeze returned while mutation was active: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if code := <-done; code != http.StatusNoContent {
		t.Fatalf("in-flight mutation status=%d", code)
	}
	if err := <-frozen; err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{name: "ordinary mutation rejected", method: http.MethodPost, path: "/work", want: http.StatusServiceUnavailable},
		{name: "read remains available", method: http.MethodGet, path: "/read", want: http.StatusNoContent},
		{name: "promote control remains available", method: http.MethodPost, path: "/api/v1/system/upgrade/promoted", want: http.StatusNoContent},
		{name: "abort control remains available", method: http.MethodPost, path: "/api/v1/system/upgrade/aborted", want: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.want, response.Body.String())
			}
		})
	}
	gate.Resume()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/other", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("mutation did not resume: status=%d", response.Code)
	}
}
