package api

import (
	"context"
	"net/http"
	"time"

	"github.com/foxc888/foxos/internal/routeros"
)

type EgressReadinessReader interface {
	EgressState(context.Context) (routeros.EgressState, error)
}

func (s *Server) RegisterEgressCapabilities(mux *http.ServeMux, reader EgressReadinessReader) {
	mux.Handle("GET /api/v1/egress/capabilities", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reader == nil {
			writeJSON(w, http.StatusOK, map[string]any{"routerosConfigured": false, "modes": routeros.EgressCapabilities(nil)})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		state, err := reader.EgressState(ctx)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"routerosConfigured": true, "routerosOnline": false, "modes": routeros.EgressCapabilities(nil), "error": "routeros_egress_state_unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"routerosConfigured": true, "routerosOnline": true, "modes": routeros.EgressCapabilities(&state)})
	})))
}
