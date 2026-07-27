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

type DHCPExpansionExecutor interface {
	Execute(context.Context, routeros.DHCPExpansionPlan) error
}

type dhcpExpansionExecution struct {
	Plan              routeros.DHCPExpansionPlan `json:"plan"`
	ConfirmationToken string                     `json:"confirmationToken"`
}

func (s *Server) RegisterDHCPPlanning(
	mux *http.ServeMux,
	reader BindingStateReader,
	signer *confirmation.Signer,
	replay ReplayStore,
	executor DHCPExpansionExecutor,
	audit AuditStore,
) {
	mux.Handle("GET /api/v1/routeros/dhcp/address-plan", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reader == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"configured": false, "error": "routeros_not_configured"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		state, err := reader.BindingState(ctx)
		if err != nil {
			problem(w, http.StatusServiceUnavailable, "routeros_dhcp_state", err)
			return
		}
		plan, err := routeros.PlanDHCPAddresses(state)
		if err != nil {
			problem(w, http.StatusUnprocessableEntity, "dhcp_plan_invalid", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"configured": true, "plan": plan})
	})))

	mux.Handle("POST /api/v1/routeros/plans/dhcp-expansion", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reader == nil || signer == nil {
			problemCode(w, http.StatusServiceUnavailable, "dhcp_planner_not_configured")
			return
		}
		var input routeros.DHCPExpansionRequest
		if err := decode(r, &input); err != nil {
			problem(w, http.StatusBadRequest, "invalid_json", err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		state, err := reader.BindingState(ctx)
		if err != nil {
			problem(w, http.StatusServiceUnavailable, "routeros_dhcp_state", err)
			return
		}
		plan, err := routeros.PreviewDHCPExpansion(state, input)
		if err != nil {
			problem(w, http.StatusConflict, "dhcp_expansion_conflict", err)
			return
		}
		token := ""
		if plan.Executable {
			token, err = signer.Issue(plan, 5*time.Minute)
			if err != nil {
				problemCode(w, http.StatusInternalServerError, "confirmation_failed")
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"plan": plan, "confirmationToken": token, "expiresInSeconds": 300})
	})))

	mux.Handle("POST /api/v1/routeros/plans/dhcp-expansion/execute", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if signer == nil || replay == nil || executor == nil || audit == nil {
			problemCode(w, http.StatusServiceUnavailable, "dhcp_expansion_executor_not_configured")
			return
		}
		var input dhcpExpansionExecution
		if err := decode(r, &input); err != nil {
			problem(w, http.StatusBadRequest, "invalid_json", err)
			return
		}
		if !input.Plan.Executable || !input.Plan.RequiresConfirmation || input.Plan.Operation == nil || input.Plan.After == nil || input.ConfirmationToken == "" {
			problemCode(w, http.StatusConflict, "confirmation_invalid")
			return
		}
		if err := signer.Verify(input.ConfirmationToken, input.Plan); err != nil {
			problem(w, http.StatusConflict, "confirmation_invalid", err)
			return
		}
		replayDigest := confirmationDigest(input.ConfirmationToken)
		if err := replay.ConsumeReplay(r.Context(), replayDigest, time.Now().UTC().Add(15*time.Minute)); err != nil {
			problem(w, http.StatusConflict, "confirmation_replayed", err)
			return
		}
		event := domain.AuditEvent{
			ID: randomID(), Action: "routeros.dhcp-expansion", TargetID: input.Plan.PoolName, Outcome: domain.AuditStarted,
			Details: requestAuditDetails(r, map[string]any{
				"serverName": input.Plan.ServerName, "network": input.Plan.Network,
				"beforeRanges": input.Plan.CurrentRanges, "afterRanges": input.Plan.ProposedRanges,
				"beforeCapacity": input.Plan.Before.DynamicCapacity, "afterCapacity": input.Plan.After.DynamicCapacity,
			}),
		}
		if err := audit.SaveAudit(r.Context(), event); err != nil {
			problemCode(w, http.StatusInternalServerError, "audit_start_failed")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if err := executor.Execute(ctx, input.Plan); err != nil {
			event.Outcome = domain.AuditFailed
			event.Details["errorClass"] = dhcpExpansionErrorClass(err)
			event.Details["rolledBack"] = errors.Is(err, routeros.ErrDHCPExpansionRollback)
			_ = audit.SaveAudit(r.Context(), event)
			status := http.StatusBadGateway
			if errors.Is(err, routeros.ErrDHCPExpansionStale) || errors.Is(err, routeros.ErrCompensationStateChanged) {
				status = http.StatusConflict
			}
			problem(w, status, event.Details["errorClass"].(string), err)
			return
		}
		event.Outcome = domain.AuditSucceeded
		event.Details["rolledBack"] = false
		if err := audit.SaveAudit(r.Context(), event); err != nil {
			problemCode(w, http.StatusInternalServerError, "audit_finalize_failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "SUCCEEDED", "serverName": input.Plan.ServerName,
			"poolName": input.Plan.PoolName, "ranges": input.Plan.ProposedRanges, "auditId": event.ID,
		})
	})))
}

func dhcpExpansionErrorClass(err error) string {
	switch {
	case errors.Is(err, routeros.ErrDHCPExpansionStale):
		return "dhcp_expansion_stale"
	case errors.Is(err, routeros.ErrCompensationStateChanged):
		return "dhcp_expansion_external_change"
	case errors.Is(err, routeros.ErrCompensationFailed):
		return "dhcp_expansion_rollback_failed"
	case errors.Is(err, routeros.ErrDHCPExpansionRollback):
		return "dhcp_expansion_rolled_back"
	default:
		return "dhcp_expansion_failed"
	}
}
