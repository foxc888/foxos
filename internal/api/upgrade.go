package api

import (
	"context"
	"net/http"
	"time"

	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/upgrade"
)

type UpgradeCheckpointService interface {
	Create(context.Context, string) (upgrade.Checkpoint, error)
	MarkPromoted(string) (upgrade.Checkpoint, error)
}

type upgradeCheckpointInput struct {
	OperationID string `json:"operationId"`
}

type upgradeCheckpointOutput struct {
	OperationID   string `json:"operationId"`
	SourceVersion string `json:"sourceVersion"`
	SchemaVersion int    `json:"schemaVersion"`
	Status        string `json:"status"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

func (s *Server) RegisterUpgrade(mux *http.ServeMux, service UpgradeCheckpointService, audit AuditStore) {
	mux.Handle("POST /api/v1/system/upgrade/checkpoint", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if service == nil || audit == nil {
			problemCode(w, http.StatusServiceUnavailable, "upgrade_checkpoint_unavailable")
			return
		}
		var input upgradeCheckpointInput
		if err := decode(r, &input); err != nil {
			problem(w, http.StatusBadRequest, "invalid_json", err)
			return
		}
		checkpoint, err := service.Create(r.Context(), input.OperationID)
		if err != nil {
			problem(w, http.StatusConflict, "upgrade_checkpoint_failed", err)
			return
		}
		event := domain.AuditEvent{ID: "upgrade-checkpoint-" + checkpoint.OperationID, Action: "upgrade.checkpoint", TargetID: checkpoint.OperationID, Outcome: domain.AuditSucceeded, Details: requestAuditDetails(r, map[string]any{"schemaVersion": checkpoint.SchemaVersion, "sourceVersion": checkpoint.SourceVersion, "status": checkpoint.Status})}
		if err := audit.SaveAudit(r.Context(), event); err != nil {
			problemCode(w, http.StatusInternalServerError, "upgrade_checkpoint_audit_failed")
			return
		}
		writeJSON(w, http.StatusOK, renderUpgradeCheckpoint(checkpoint))
	})))

	mux.Handle("POST /api/v1/system/upgrade/promoted", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if service == nil || audit == nil {
			problemCode(w, http.StatusServiceUnavailable, "upgrade_checkpoint_unavailable")
			return
		}
		var input upgradeCheckpointInput
		if err := decode(r, &input); err != nil {
			problem(w, http.StatusBadRequest, "invalid_json", err)
			return
		}
		checkpoint, err := service.MarkPromoted(input.OperationID)
		if err != nil {
			problem(w, http.StatusConflict, "upgrade_promote_failed", err)
			return
		}
		event := domain.AuditEvent{ID: "upgrade-promoted-" + checkpoint.OperationID, Action: "upgrade.promoted", TargetID: checkpoint.OperationID, Outcome: domain.AuditSucceeded, Details: requestAuditDetails(r, map[string]any{"schemaVersion": checkpoint.SchemaVersion, "sourceVersion": checkpoint.SourceVersion})}
		if err := audit.SaveAudit(r.Context(), event); err != nil {
			problemCode(w, http.StatusInternalServerError, "upgrade_promote_audit_failed")
			return
		}
		writeJSON(w, http.StatusOK, renderUpgradeCheckpoint(checkpoint))
	})))
}

func renderUpgradeCheckpoint(checkpoint upgrade.Checkpoint) upgradeCheckpointOutput {
	return upgradeCheckpointOutput{
		OperationID:   checkpoint.OperationID,
		SourceVersion: checkpoint.SourceVersion,
		SchemaVersion: checkpoint.SchemaVersion,
		Status:        checkpoint.Status,
		CreatedAt:     checkpoint.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:     checkpoint.UpdatedAt.Format(time.RFC3339Nano),
	}
}
