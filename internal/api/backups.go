package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/foxc888/foxos/internal/backup"
	"github.com/foxc888/foxos/internal/confirmation"
	"github.com/foxc888/foxos/internal/domain"
)

type BackupService interface {
	Create(context.Context, string) (backup.Manifest, error)
	List() ([]backup.Manifest, error)
	Inspect(string) (backup.Preview, error)
	Restore(context.Context, string, string) error
}

type backupConfirmationPlan struct {
	Action    string `json:"action"`
	BackupID  string `json:"backupId"`
	Digest    string `json:"digest"`
	FileCount int    `json:"fileCount"`
	Mihomo    bool   `json:"mihomo"`
}

func (s *Server) RegisterBackups(mux *http.ServeMux, service BackupService, signer *confirmation.Signer, replay MihomoReplay, jobs MihomoJobSubmitter, audit AuditStore) {
	if service == nil {
		return
	}
	mux.Handle("GET /api/v1/backups", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items, err := service.List()
		if err != nil {
			problemCode(w, http.StatusInternalServerError, "backups_failed")
			return
		}
		writeJSON(w, http.StatusOK, items)
	})))
	mux.Handle("POST /api/v1/backups", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Label string `json:"label"`
		}
		if r.ContentLength != 0 {
			if err := decode(r, &input); err != nil {
				problem(w, http.StatusBadRequest, "invalid_json", err)
				return
			}
		}
		if len(strings.TrimSpace(input.Label)) > 120 {
			problemCode(w, http.StatusUnprocessableEntity, "backup_label_invalid")
			return
		}
		if jobs != nil {
			key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
			if key == "" {
				key = randomID()
			}
			if len(key) > 128 {
				problemCode(w, http.StatusBadRequest, "idempotency_key_invalid")
				return
			}
			metadata := requestAuditDetails(r, nil)
			job, err := jobs.Submit(r.Context(), "backup.create", key, map[string]any{"label": input.Label, "actor": metadata["actor"], "source": metadata["source"]})
			if err != nil {
				problemCode(w, http.StatusInternalServerError, "backup_job_failed")
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]any{"status": domain.JobQueued, "job": renderJob(job)})
			return
		}
		item, err := service.Create(r.Context(), input.Label)
		if err != nil {
			problemCode(w, http.StatusInternalServerError, "backup_create_failed")
			return
		}
		writeJSON(w, http.StatusCreated, item)
	})))
	mux.Handle("POST /api/v1/backups/{id}/restore/plan", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if signer == nil {
			problemCode(w, http.StatusServiceUnavailable, "confirmation_unavailable")
			return
		}
		preview, err := service.Inspect(r.PathValue("id"))
		if err != nil {
			problemCode(w, http.StatusNotFound, "backup_not_found_or_invalid")
			return
		}
		plan := backupConfirmationPlan{Action: "backup.restore", BackupID: r.PathValue("id"), Digest: preview.Digest, FileCount: preview.Manifest.FileCount, Mihomo: preview.Manifest.Mihomo != ""}
		token, err := signer.Issue(plan, 5*time.Minute)
		if err != nil {
			problemCode(w, http.StatusInternalServerError, "confirmation_failed")
			return
		}
		warnings := []string{"当前节点、代理组、设备策略、订阅和告警状态会被备份内容替换", "任务、确认令牌和审计记录不会被旧备份覆盖"}
		if plan.Mihomo {
			warnings = append(warnings, "Mihomo 配置会执行校验、原子替换、热重载和健康检查；失败时回滚")
		}
		writeJSON(w, http.StatusOK, map[string]any{"plan": plan, "confirmationToken": token, "expiresInSeconds": 300, "warnings": warnings})
	})))
	mux.Handle("POST /api/v1/backups/{id}/restore", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ConfirmationToken string `json:"confirmationToken"`
		}
		if err := decode(r, &input); err != nil {
			problem(w, http.StatusBadRequest, "invalid_json", err)
			return
		}
		preview, err := service.Inspect(r.PathValue("id"))
		if err != nil {
			problemCode(w, http.StatusConflict, "backup_invalid")
			return
		}
		plan := backupConfirmationPlan{Action: "backup.restore", BackupID: r.PathValue("id"), Digest: preview.Digest, FileCount: preview.Manifest.FileCount, Mihomo: preview.Manifest.Mihomo != ""}
		if signer == nil || strings.TrimSpace(input.ConfirmationToken) == "" {
			problemCode(w, http.StatusConflict, "confirmation_invalid")
			return
		}
		if err := signer.Verify(input.ConfirmationToken, plan); err != nil {
			problem(w, http.StatusConflict, "confirmation_invalid", err)
			return
		}
		if replay == nil {
			problemCode(w, http.StatusServiceUnavailable, "confirmation_replay_unavailable")
			return
		}
		if err := replay.ConsumeReplay(r.Context(), confirmationDigest(input.ConfirmationToken), time.Now().UTC().Add(15*time.Minute)); err != nil {
			problem(w, http.StatusConflict, "confirmation_replayed", err)
			return
		}
		event := domain.AuditEvent{ID: randomID(), Action: "backup.restore", TargetID: plan.BackupID, Outcome: domain.AuditStarted, Details: requestAuditDetails(r, map[string]any{"digest": plan.Digest, "fileCount": plan.FileCount, "mihomo": plan.Mihomo})}
		if audit == nil || audit.SaveAudit(r.Context(), event) != nil {
			problemCode(w, http.StatusServiceUnavailable, "audit_store_not_configured")
			return
		}
		if jobs != nil {
			job, err := jobs.Submit(r.Context(), "backup.restore", confirmationDigest(input.ConfirmationToken), map[string]any{"backupId": plan.BackupID, "digest": plan.Digest, "auditId": event.ID, "actor": event.Details["actor"], "source": event.Details["source"]})
			if err != nil {
				event.Outcome = domain.AuditFailed
				event.Details["errorClass"] = "backup_job_failed"
				_ = audit.SaveAudit(r.Context(), event)
				problemCode(w, http.StatusInternalServerError, "backup_job_failed")
				return
			}
			event.Details["jobId"] = job.ID
			_ = audit.SaveAudit(r.Context(), event)
			writeJSON(w, http.StatusAccepted, map[string]any{"status": domain.JobQueued, "job": renderJob(job), "backupId": plan.BackupID})
			return
		}
		if err := service.Restore(r.Context(), plan.BackupID, plan.Digest); err != nil {
			event.Outcome = domain.AuditFailed
			event.Details["errorClass"] = "backup_restore_failed"
			_ = audit.SaveAudit(r.Context(), event)
			problemCode(w, http.StatusConflict, "backup_restore_failed")
			return
		}
		event.Outcome = domain.AuditSucceeded
		_ = audit.SaveAudit(r.Context(), event)
		writeJSON(w, http.StatusOK, map[string]any{"status": "restored", "backupId": plan.BackupID})
	})))
}
