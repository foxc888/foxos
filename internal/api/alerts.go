package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/foxc888/foxos/internal/domain"
)

type AlertStore interface {
	Alerts(context.Context, bool) ([]domain.Alert, error)
	AcknowledgeAlert(context.Context, string) error
}

type alertOutput struct {
	ID           string `json:"id"`
	Key          string `json:"key"`
	Severity     string `json:"severity"`
	Title        string `json:"title"`
	Message      string `json:"message"`
	Acknowledged bool   `json:"acknowledged"`
	FirstSeen    string `json:"firstSeen"`
	LastSeen     string `json:"lastSeen"`
	ResolvedAt   string `json:"resolvedAt,omitempty"`
}

func renderAlert(item domain.Alert) alertOutput {
	return alertOutput{ID: item.ID, Key: item.Key, Severity: string(item.Severity), Title: item.Title, Message: item.Message, Acknowledged: item.Acknowledged, FirstSeen: formatTime(item.FirstSeen), LastSeen: formatTime(item.LastSeen), ResolvedAt: formatTime(item.ResolvedAt)}
}

func (s *Server) RegisterAlerts(mux *http.ServeMux, store AlertStore) {
	if store == nil {
		return
	}
	mux.Handle("GET /api/v1/alerts", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items, err := store.Alerts(r.Context(), r.URL.Query().Get("includeResolved") == "true")
		if err != nil {
			problemCode(w, http.StatusInternalServerError, "alerts_failed")
			return
		}
		out := make([]alertOutput, 0, len(items))
		for _, item := range items {
			out = append(out, renderAlert(item))
		}
		writeJSON(w, http.StatusOK, out)
	})))
	mux.Handle("POST /api/v1/alerts/{id}/acknowledge", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := store.AcknowledgeAlert(r.Context(), r.PathValue("id")); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				problemCode(w, http.StatusNotFound, "alert_not_found")
				return
			}
			problemCode(w, http.StatusInternalServerError, "alert_update_failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "acknowledged", "id": r.PathValue("id")})
	})))
}
