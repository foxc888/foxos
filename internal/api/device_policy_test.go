package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/foxc888/foxos/internal/domain"
)

func TestDevicePolicyWritesRequireEgressWorkflow(t *testing.T) {
	t.Parallel()
	const token = "01234567890123456789012345678901"
	app, err := New(&memoryNodes{nodes: map[string]domain.Node{}}, token)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeDevicePolicies{}
	mux := http.NewServeMux()
	app.RegisterDevicePolicies(mux, store)
	for _, test := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/v1/device-policies"},
		{method: http.MethodPut, path: "/api/v1/device-policies/phone"},
		{method: http.MethodDelete, path: "/api/v1/device-policies/phone"},
	} {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusMethodNotAllowed || !strings.Contains(response.Body.String(), "egress_workflow_required") {
			t.Fatalf("%s %s status=%d body=%s", test.method, test.path, response.Code, response.Body.String())
		}
	}
	if store.saveCalls != 0 {
		t.Fatalf("direct policy endpoint persisted %d writes", store.saveCalls)
	}
}
