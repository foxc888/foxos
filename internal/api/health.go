package api

import (
	"context"
	"net/http"
	"os"
	"time"
)

type HealthStore interface{ Ping(context.Context) error }
type HealthDependency struct {
	Name       string
	Configured bool
	Check      func(context.Context) error
}

type HealthOptions struct {
	Store         HealthStore
	RequiredPaths []string
	Dependencies  []HealthDependency
	Version       string
}

func (s *Server) RegisterHealth(mux *http.ServeMux, options HealthOptions) {
	mux.HandleFunc("GET /api/v1/health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": options.Version, "time": time.Now().UTC().Format(time.RFC3339Nano)})
	})
	mux.HandleFunc("GET /api/v1/health/ready", func(w http.ResponseWriter, r *http.Request) {
		checks := make(map[string]string)
		ready := true
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if options.Store == nil {
			checks["sqlite"] = "not_configured"
			ready = false
		} else if err := options.Store.Ping(ctx); err != nil {
			checks["sqlite"] = "unavailable"
			ready = false
		} else {
			checks["sqlite"] = "ok"
		}
		for _, path := range options.RequiredPaths {
			if path == "" {
				continue
			}
			key := "path:" + path
			if _, err := os.Stat(path); err != nil {
				checks[key] = "unavailable"
				ready = false
			} else {
				checks[key] = "ok"
			}
		}
		for _, dependency := range options.Dependencies {
			if !dependency.Configured {
				checks[dependency.Name] = "not_configured"
				continue
			}
			if dependency.Check == nil {
				checks[dependency.Name] = "unavailable"
				ready = false
				continue
			}
			if err := dependency.Check(ctx); err != nil {
				checks[dependency.Name] = "unavailable"
				ready = false
			} else {
				checks[dependency.Name] = "ok"
			}
		}
		status := http.StatusOK
		state := "ready"
		if !ready {
			status = http.StatusServiceUnavailable
			state = "not_ready"
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, status, map[string]any{"status": state, "version": options.Version, "time": time.Now().UTC().Format(time.RFC3339Nano), "checks": checks})
	})
}
