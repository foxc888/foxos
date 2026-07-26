package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/foxc888/foxos/internal/domain"
	storepkg "github.com/foxc888/foxos/internal/store/sqlite"
)

type GroupStore interface {
	SaveGroup(context.Context, domain.Group) error
	Group(context.Context, string) (domain.Group, error)
	Groups(context.Context) ([]domain.Group, error)
	DeleteGroup(context.Context, string) error
}

type groupInput struct {
	ID        string   `json:"id,omitempty"`
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	NodeIDs   []string `json:"nodeIds"`
	GroupIDs  []string `json:"groupIds,omitempty"`
	URL       string   `json:"url,omitempty"`
	Interval  int      `json:"interval,omitempty"`
	Tolerance int      `json:"tolerance,omitempty"`
	Strategy  string   `json:"strategy,omitempty"`
}

func (in groupInput) domain() domain.Group {
	return domain.Group{ID: in.ID, Name: strings.TrimSpace(in.Name), Type: strings.ToLower(strings.TrimSpace(in.Type)), NodeIDs: in.NodeIDs, GroupIDs: in.GroupIDs, URL: in.URL, Interval: in.Interval, Tolerance: in.Tolerance, Strategy: in.Strategy}
}

func (s *Server) RegisterGroups(mux *http.ServeMux, groups GroupStore) {
	mux.Handle("GET /api/v1/proxy-groups", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items, err := groups.Groups(r.Context())
		if err != nil {
			problem(w, 500, "list_failed", err)
			return
		}
		writeJSON(w, 200, items)
	})))
	mux.Handle("POST /api/v1/proxy-groups", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input groupInput
		if err := decode(r, &input); err != nil {
			problem(w, 400, "invalid_json", err)
			return
		}
		if input.ID == "" {
			input.ID = randomID()
		}
		group := input.domain()
		if err := groups.SaveGroup(r.Context(), group); err != nil {
			problem(w, 422, "invalid_group", err)
			return
		}
		writeJSON(w, 201, group)
	})))
	mux.Handle("PUT /api/v1/proxy-groups/{id}", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input groupInput
		if err := decode(r, &input); err != nil {
			problem(w, 400, "invalid_json", err)
			return
		}
		input.ID = r.PathValue("id")
		group := input.domain()
		if err := groups.SaveGroup(r.Context(), group); err != nil {
			problem(w, 422, "invalid_group", err)
			return
		}
		writeJSON(w, 200, group)
	})))
	mux.Handle("DELETE /api/v1/proxy-groups/{id}", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := groups.DeleteGroup(r.Context(), r.PathValue("id"))
		if errors.Is(err, storepkg.ErrNotFound) {
			problem(w, 404, "not_found", err)
			return
		}
		if err != nil {
			problem(w, 500, "delete_failed", err)
			return
		}
		w.WriteHeader(204)
	})))
}
