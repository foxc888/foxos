package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/foxc888/foxos/internal/domain"
)

type JobManager interface {
	Job(context.Context, string) (domain.Job, error)
	Retry(context.Context, string) (domain.Job, error)
}

type jobOutput struct {
	ID             string           `json:"id"`
	Kind           string           `json:"kind"`
	Status         domain.JobStatus `json:"status"`
	Progress       int              `json:"progress"`
	IdempotencyKey string           `json:"idempotencyKey,omitempty"`
	Request        map[string]any   `json:"request,omitempty"`
	Result         map[string]any   `json:"result,omitempty"`
	ErrorClass     string           `json:"errorClass,omitempty"`
	ErrorMessage   string           `json:"errorMessage,omitempty"`
	Attempts       int              `json:"attempts"`
	CreatedAt      string           `json:"createdAt"`
	StartedAt      string           `json:"startedAt,omitempty"`
	FinishedAt     string           `json:"finishedAt,omitempty"`
	UpdatedAt      string           `json:"updatedAt"`
}

func (s *Server) RegisterJobs(mux *http.ServeMux, manager JobManager) {
	if manager == nil {
		return
	}
	mux.Handle("GET /api/v1/jobs/{id}", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		job, err := manager.Job(r.Context(), r.PathValue("id"))
		if errors.Is(err, domain.ErrNotFound) {
			problemCode(w, http.StatusNotFound, "job_not_found")
			return
		}
		if err != nil {
			problemCode(w, http.StatusInternalServerError, "job_read_failed")
			return
		}
		writeJSON(w, http.StatusOK, renderJob(job))
	})))
	mux.Handle("POST /api/v1/jobs/{id}/retry", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		job, err := manager.Retry(r.Context(), r.PathValue("id"))
		if errors.Is(err, domain.ErrNotFound) {
			problemCode(w, http.StatusNotFound, "job_not_found")
			return
		}
		if err != nil {
			problemCode(w, http.StatusConflict, "job_not_retryable")
			return
		}
		writeJSON(w, http.StatusAccepted, renderJob(job))
	})))
}

func renderJob(job domain.Job) jobOutput {
	return jobOutput{ID: job.ID, Kind: job.Kind, Status: job.Status, Progress: job.Progress, IdempotencyKey: job.IdempotencyKey, Request: job.Request, Result: job.Result, ErrorClass: job.ErrorClass, ErrorMessage: job.ErrorMessage, Attempts: job.Attempts, CreatedAt: formatTime(job.CreatedAt), StartedAt: formatOptionalTime(job.StartedAt), FinishedAt: formatOptionalTime(job.FinishedAt), UpdatedAt: formatTime(job.UpdatedAt)}
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func formatOptionalTime(value time.Time) string { return formatTime(value) }
