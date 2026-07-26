package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/foxc888/foxos/internal/confirmation"
	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/routeros"
)

type LeaseReader interface {
	Leases(context.Context) ([]routeros.Lease, error)
}
type BindingExecutor interface {
	Execute(context.Context, routeros.Plan, string) error
}
type AuditStore interface {
	SaveAudit(context.Context, domain.AuditEvent) error
}

type bindingInput struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	MACAddress string            `json:"macAddress"`
	StaticIP   string            `json:"staticIp"`
	DHCPServer string            `json:"dhcpServer"`
	Egress     domain.EgressType `json:"egress"`
	TargetID   string            `json:"targetId,omitempty"`
}
type bindingExecution struct {
	Plan              routeros.Plan `json:"plan"`
	ConfirmationToken string        `json:"confirmationToken"`
}

func (s *Server) RegisterBindingPlan(mux *http.ServeMux, reader LeaseReader, signer *confirmation.Signer, executor BindingExecutor, guard *confirmation.ReplayGuard, audit AuditStore) {
	mux.Handle("POST /api/v1/routeros/plans/device-binding", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reader == nil || signer == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"configured": false, "error": "routeros_not_configured"})
			return
		}
		var input bindingInput
		if err := decode(r, &input); err != nil {
			problem(w, http.StatusBadRequest, "invalid_json", err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		leases, err := reader.Leases(ctx)
		if err != nil {
			problem(w, http.StatusServiceUnavailable, "routeros_leases", err)
			return
		}
		plan, err := routeros.PlanDeviceBinding(domain.DevicePolicy{ID: input.ID, Name: input.Name, MACAddress: input.MACAddress, StaticIP: input.StaticIP, DHCPServer: input.DHCPServer, Egress: input.Egress, TargetID: input.TargetID}, leases)
		if err != nil {
			problem(w, http.StatusConflict, "binding_conflict", err)
			return
		}
		token := ""
		if plan.RequiresConfirmation {
			token, err = signer.Issue(plan, 5*time.Minute)
			if err != nil {
				problem(w, 500, "confirmation_failed", err)
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"plan": plan, "confirmationToken": token, "expiresInSeconds": 300})
	})))
	mux.Handle("POST /api/v1/routeros/plans/device-binding/execute", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if executor == nil || signer == nil || guard == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"configured": false, "error": "routeros_writer_not_configured"})
			return
		}
		var input bindingExecution
		if err := decode(r, &input); err != nil {
			problem(w, 400, "invalid_json", err)
			return
		}
		if err := signer.Verify(input.ConfirmationToken, input.Plan); err != nil {
			problem(w, 409, "confirmation_invalid", err)
			return
		}
		if err := guard.Consume(input.ConfirmationToken); err != nil {
			problem(w, 409, "confirmation_replayed", err)
			return
		}
		if audit == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"configured": false, "error": "audit_store_not_configured"})
			return
		}
		event := domain.AuditEvent{ID: randomID(), Action: "routeros.device-binding", TargetID: input.Plan.PolicyID, Outcome: domain.AuditStarted, Details: map[string]any{"operationCount": len(input.Plan.Operations)}}
		if err := audit.SaveAudit(r.Context(), event); err != nil {
			problem(w, 500, "audit_start_failed", err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		if err := executor.Execute(ctx, input.Plan, input.ConfirmationToken); err != nil {
			event.Outcome = domain.AuditFailed
			if errors.Is(err, routeros.ErrWriteVerification) {
				event.Details["errorClass"] = "routeros_verification_failed"
			} else {
				event.Details["errorClass"] = "routeros_write_failed"
			}
			_ = audit.SaveAudit(r.Context(), event)
			if errors.Is(err, routeros.ErrWriteVerification) {
				problem(w, 502, "routeros_verification_failed", err)
				return
			}
			problem(w, 502, "routeros_write_failed", err)
			return
		}
		event.Outcome = domain.AuditSucceeded
		if err := audit.SaveAudit(r.Context(), event); err != nil {
			writeJSON(w, 500, map[string]any{"status": "applied", "error": "audit_finalize_failed", "policyId": input.Plan.PolicyID})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "applied", "policyId": input.Plan.PolicyID, "auditId": event.ID})
	})))
}
