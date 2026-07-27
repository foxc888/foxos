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

type readinessReader struct{ state routeros.EgressState }

func (r readinessReader) EgressState(context.Context) (routeros.EgressState, error) {
	return r.state, nil
}

func TestEgressCapabilitiesAPIReportsUnavailableTransparentModes(t *testing.T) {
	t.Parallel()
	const token = "01234567890123456789012345678901"
	app, err := New(&memoryNodes{nodes: map[string]domain.Node{}}, token)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	app.RegisterEgressCapabilities(mux, readinessReader{state: routeros.EgressState{}})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/egress/capabilities", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"mode":"mihomo-node","available":false`) || !strings.Contains(response.Body.String(), "transparent_ingress_unverified") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
