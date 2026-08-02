package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/foxc888/foxos/internal/routeros"
)

type ContainerCommandReader interface {
	ManagedContainer(context.Context, string, string) (routeros.Container, error)
}

func (s *Server) RegisterContainerCommands(mux *http.ServeMux, reader ContainerCommandReader, jobs MihomoJobSubmitter) {
	mux.Handle("POST /api/v1/routeros/containers/{id}/commands/{command}", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reader == nil || jobs == nil {
			problemCode(w, http.StatusServiceUnavailable, "container_commands_unavailable")
			return
		}
		command := strings.ToLower(strings.TrimSpace(r.PathValue("command")))
		if command != "start" && command != "stop" && command != "restart" {
			problemCode(w, http.StatusNotFound, "container_command_unknown")
			return
		}
		var input struct {
			Owner          string `json:"owner"`
			IdempotencyKey string `json:"idempotencyKey"`
		}
		if err := decode(r, &input); err != nil {
			problem(w, http.StatusBadRequest, "invalid_json", err)
			return
		}
		input.Owner = strings.TrimSpace(input.Owner)
		input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
		if !safeClientOperationID(input.IdempotencyKey) {
			problemCode(w, http.StatusUnprocessableEntity, "idempotency_key_invalid")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		container, err := reader.ManagedContainer(ctx, r.PathValue("id"), input.Owner)
		if err != nil {
			problem(w, http.StatusConflict, "container_not_managed", err)
			return
		}
		auditDetails := requestAuditDetails(r, nil)
		job, err := jobs.Submit(r.Context(), "routeros.container-command", input.IdempotencyKey, map[string]any{
			"containerId": container.ID, "owner": container.Comment, "command": command,
			"actor": auditDetails["actor"], "source": auditDetails["source"],
		})
		if err != nil {
			problemCode(w, http.StatusInternalServerError, "container_command_submit_failed")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"status": job.Status, "job": renderJob(job)})
	})))
}

func safeClientOperationID(value string) bool {
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_') {
			return false
		}
	}
	return true
}
