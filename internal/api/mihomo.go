package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/foxc888/foxos/internal/confirmation"
	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/mihomo"
)

type MihomoService interface {
	Draft(context.Context) (domain.MihomoDraft, error)
	SaveDraft(context.Context, domain.MihomoDraft) (domain.MihomoDraft, error)
	Preview(context.Context, domain.MihomoDraft) (mihomo.Preview, error)
	ApplyPreview(context.Context, domain.MihomoDraft, string, string) (mihomo.ApplyResult, domain.MihomoSnapshot, error)
	Restore(context.Context, string, string) (mihomo.ApplyResult, domain.MihomoSnapshot, error)
}

type MihomoSnapshotReader interface {
	MihomoSnapshots(context.Context, int) ([]domain.MihomoSnapshot, error)
}

type MihomoReplay interface {
	ConsumeReplay(context.Context, string, time.Time) error
}

type MihomoJobSubmitter interface {
	Submit(context.Context, string, string, map[string]any) (domain.Job, error)
}

type mihomoDraftPayload struct {
	ID        string   `json:"id,omitempty"`
	Mode      string   `json:"mode"`
	MixedPort int      `json:"mixedPort"`
	AllowLAN  bool     `json:"allowLan"`
	Rules     []string `json:"rules"`
	Revision  int64    `json:"revision,omitempty"`
	UpdatedAt string   `json:"updatedAt,omitempty"`
}

type mihomoPreviewPayload struct {
	Draft     mihomoDraftPayload `json:"draft"`
	Digest    string             `json:"digest"`
	YAML      string             `json:"yaml"`
	Diff      string             `json:"diff"`
	HasSecret bool               `json:"hasSecret"`
}

type mihomoApplyInput struct {
	Draft             mihomoDraftPayload `json:"draft"`
	Digest            string             `json:"digest"`
	ConfirmationToken string             `json:"confirmationToken"`
	Label             string             `json:"label,omitempty"`
}

type mihomoRestoreInput struct {
	ConfirmationToken string `json:"confirmationToken"`
	Label             string `json:"label,omitempty"`
}

type mihomoConfirmationPlan struct {
	Action     string             `json:"action"`
	Digest     string             `json:"digest,omitempty"`
	SnapshotID string             `json:"snapshotId,omitempty"`
	Draft      mihomoDraftPayload `json:"draft,omitempty"`
}

func (s *Server) RegisterMihomo(mux *http.ServeMux, service MihomoService, snapshots MihomoSnapshotReader, signer *confirmation.Signer, replay MihomoReplay, jobs MihomoJobSubmitter, audit AuditStore) {
	if service == nil {
		mux.Handle("GET /api/v1/mihomo/draft", s.auth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			problemCode(w, http.StatusServiceUnavailable, "mihomo_not_configured")
		})))
		return
	}
	mux.Handle("GET /api/v1/mihomo/draft", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		draft, err := service.Draft(r.Context())
		if err != nil {
			problem(w, http.StatusServiceUnavailable, "mihomo_draft_unavailable", err)
			return
		}
		writeJSON(w, http.StatusOK, renderMihomoDraft(draft))
	})))
	mux.Handle("PUT /api/v1/mihomo/draft", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input mihomoDraftPayload
		if err := decode(r, &input); err != nil {
			problem(w, http.StatusBadRequest, "invalid_json", err)
			return
		}
		stored, err := service.SaveDraft(r.Context(), parseMihomoDraft(input))
		if err != nil {
			problem(w, http.StatusUnprocessableEntity, "mihomo_draft_invalid", err)
			return
		}
		writeJSON(w, http.StatusOK, renderMihomoDraft(stored))
	})))
	mux.Handle("POST /api/v1/mihomo/config/preview", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		input := mihomoDraftPayload{}
		if r.Body != nil && r.ContentLength != 0 {
			if err := decode(r, &input); err != nil {
				problem(w, http.StatusBadRequest, "invalid_json", err)
				return
			}
		} else if draft, err := service.Draft(r.Context()); err == nil {
			input = renderMihomoDraft(draft)
		}
		preview, err := service.Preview(r.Context(), parseMihomoDraft(input))
		if err != nil {
			problem(w, http.StatusUnprocessableEntity, "mihomo_preview_failed", err)
			return
		}
		token := ""
		if signer != nil {
			token, err = signer.Issue(mihomoConfirmationPlan{Action: "mihomo.apply", Digest: preview.Digest, Draft: renderMihomoDraft(preview.Draft)}, 5*time.Minute)
			if err != nil {
				problemCode(w, http.StatusInternalServerError, "confirmation_failed")
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"preview": renderMihomoPreview(preview), "confirmationToken": token, "expiresInSeconds": 300})
	})))
	mux.Handle("POST /api/v1/mihomo/config/validate", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			YAML string `json:"yaml"`
		}
		if err := decode(r, &input); err != nil {
			problem(w, http.StatusBadRequest, "invalid_json", err)
			return
		}
		if err := mihomo.ValidateYAML([]byte(input.YAML)); err != nil {
			problem(w, http.StatusUnprocessableEntity, "mihomo_config_invalid", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"valid": true})
	})))
	mux.Handle("GET /api/v1/mihomo/snapshots", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if snapshots == nil {
			problemCode(w, http.StatusServiceUnavailable, "mihomo_snapshots_unavailable")
			return
		}
		items, err := snapshots.MihomoSnapshots(r.Context(), 100)
		if err != nil {
			problemCode(w, http.StatusInternalServerError, "mihomo_snapshots_failed")
			return
		}
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			out = append(out, map[string]any{"id": item.ID, "digest": item.Digest, "label": item.Label, "createdAt": formatTime(item.CreatedAt)})
		}
		writeJSON(w, http.StatusOK, out)
	})))
	mux.Handle("POST /api/v1/mihomo/config/apply", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input mihomoApplyInput
		if err := decode(r, &input); err != nil {
			problem(w, http.StatusBadRequest, "invalid_json", err)
			return
		}
		digest := strings.ToLower(strings.TrimSpace(input.Digest))
		plan := mihomoConfirmationPlan{Action: "mihomo.apply", Digest: digest, Draft: input.Draft}
		if err := verifyAndConsumeMihomo(r.Context(), signer, replay, input.ConfirmationToken, plan); err != nil {
			problem(w, http.StatusConflict, "confirmation_invalid", err)
			return
		}
		if jobs != nil {
			metadata := requestAuditDetails(r, nil)
			job, err := jobs.Submit(r.Context(), "mihomo.apply", digest, map[string]any{"draft": input.Draft, "digest": digest, "label": input.Label, "actor": metadata["actor"], "source": metadata["source"]})
			if err != nil {
				problemCode(w, http.StatusInternalServerError, "mihomo_job_failed")
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]any{"status": "QUEUED", "job": renderJob(job)})
			return
		}
		result, snapshot, err := service.ApplyPreview(r.Context(), parseMihomoDraft(input.Draft), digest, input.Label)
		if err != nil {
			writeMihomoApplyError(w, result, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "SUCCEEDED", "snapshotId": snapshot.ID, "rolledBack": result.RolledBack})
	})))
	mux.Handle("POST /api/v1/mihomo/snapshots/{id}/restore/plan", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if signer == nil {
			problemCode(w, http.StatusServiceUnavailable, "confirmation_unavailable")
			return
		}
		plan := mihomoConfirmationPlan{Action: "mihomo.restore", SnapshotID: r.PathValue("id")}
		token, err := signer.Issue(plan, 5*time.Minute)
		if err != nil {
			problemCode(w, http.StatusInternalServerError, "confirmation_failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"plan": plan, "confirmationToken": token, "expiresInSeconds": 300})
	})))
	mux.Handle("POST /api/v1/mihomo/snapshots/{id}/restore", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input mihomoRestoreInput
		if err := decode(r, &input); err != nil {
			problem(w, http.StatusBadRequest, "invalid_json", err)
			return
		}
		plan := mihomoConfirmationPlan{Action: "mihomo.restore", SnapshotID: r.PathValue("id")}
		if err := verifyAndConsumeMihomo(r.Context(), signer, replay, input.ConfirmationToken, plan); err != nil {
			problem(w, http.StatusConflict, "confirmation_invalid", err)
			return
		}
		if jobs != nil {
			metadata := requestAuditDetails(r, nil)
			job, err := jobs.Submit(r.Context(), "mihomo.restore", r.PathValue("id"), map[string]any{"snapshotId": r.PathValue("id"), "label": input.Label, "actor": metadata["actor"], "source": metadata["source"]})
			if err != nil {
				problemCode(w, http.StatusInternalServerError, "mihomo_job_failed")
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]any{"status": "QUEUED", "job": renderJob(job)})
			return
		}
		result, snapshot, err := service.Restore(r.Context(), r.PathValue("id"), input.Label)
		if err != nil {
			writeMihomoApplyError(w, result, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "SUCCEEDED", "snapshotId": snapshot.ID, "rolledBack": result.RolledBack})
	})))
	_ = audit
}

func verifyAndConsumeMihomo(ctx context.Context, signer *confirmation.Signer, replay MihomoReplay, token string, plan mihomoConfirmationPlan) error {
	if signer == nil || strings.TrimSpace(token) == "" {
		return errors.New("confirmation is required")
	}
	if err := signer.Verify(token, plan); err != nil {
		return err
	}
	if replay != nil {
		if err := replay.ConsumeReplay(ctx, confirmationDigest(token), time.Now().UTC().Add(15*time.Minute)); err != nil {
			return err
		}
	}
	return nil
}

func writeMihomoApplyError(w http.ResponseWriter, result mihomo.ApplyResult, err error) {
	status := http.StatusBadGateway
	code := "mihomo_apply_failed"
	if result.RolledBack {
		status = http.StatusConflict
		code = "mihomo_rolled_back"
	}
	problem(w, status, code, err)
}

func renderMihomoDraft(draft domain.MihomoDraft) mihomoDraftPayload {
	return mihomoDraftPayload{ID: draft.ID, Mode: draft.Mode, MixedPort: draft.MixedPort, AllowLAN: draft.AllowLAN, Rules: append([]string(nil), draft.Rules...), Revision: draft.Revision, UpdatedAt: formatTime(draft.UpdatedAt)}
}

func parseMihomoDraft(input mihomoDraftPayload) domain.MihomoDraft {
	return domain.MihomoDraft{ID: input.ID, Mode: strings.TrimSpace(strings.ToLower(input.Mode)), MixedPort: input.MixedPort, AllowLAN: input.AllowLAN, Rules: append([]string(nil), input.Rules...), Revision: input.Revision}
}

func renderMihomoPreview(preview mihomo.Preview) mihomoPreviewPayload {
	return mihomoPreviewPayload{Draft: renderMihomoDraft(preview.Draft), Digest: preview.Digest, YAML: preview.YAML, Diff: preview.Diff, HasSecret: preview.HasSecret}
}
