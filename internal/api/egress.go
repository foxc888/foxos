package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/foxc888/foxos/internal/confirmation"
	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/routeros"
)

type EgressExecutor interface {
	Execute(context.Context, routeros.EgressPlan) error
	Compensate(context.Context, routeros.EgressPlan) error
}

type EgressPlanner interface {
	PlanDeviceEgress(context.Context, domain.DevicePolicy) (routeros.EgressPlan, error)
}

type EgressJobSubmitter interface {
	Submit(context.Context, string, string, map[string]any) (domain.Job, error)
}

type egressExecution struct {
	Plan              routeros.EgressPlan `json:"plan"`
	ConfirmationToken string              `json:"confirmationToken"`
}

func (s *Server) RegisterEgress(mux *http.ServeMux, policies DevicePolicyStore, planner EgressPlanner, signer *confirmation.Signer, replay ReplayStore, executor EgressExecutor, jobs EgressJobSubmitter, audit AuditStore) {
	mux.Handle("POST /api/v1/routeros/plans/egress/{id}", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if policies == nil || planner == nil || signer == nil {
			problemCode(w, http.StatusServiceUnavailable, "egress_not_configured")
			return
		}
		id := r.PathValue("id")
		var previous *domain.DevicePolicy
		stored, storedErr := policies.DevicePolicy(r.Context(), id)
		if storedErr == nil {
			previous = &stored
		} else if !errors.Is(storedErr, domain.ErrNotFound) {
			problemCode(w, http.StatusInternalServerError, "policy_read_failed")
			return
		}
		policy := stored
		if r.Body != nil && r.ContentLength != 0 {
			var input devicePolicyPayload
			if err := decode(r, &input); err != nil {
				problem(w, http.StatusBadRequest, "invalid_json", err)
				return
			}
			input.ID = id
			policy = input.domain()
		} else if previous == nil {
			problemCode(w, http.StatusNotFound, "policy_not_found")
			return
		}
		if validator, ok := policies.(interface {
			ValidateDevicePolicyTarget(context.Context, domain.DevicePolicy) error
		}); ok {
			if err := validator.ValidateDevicePolicyTarget(r.Context(), policy); err != nil {
				problem(w, http.StatusUnprocessableEntity, "invalid_policy_target", err)
				return
			}
		}
		if err := policy.Validate(); err != nil {
			problem(w, http.StatusUnprocessableEntity, "invalid_policy", err)
			return
		}
		plan, err := planner.PlanDeviceEgress(r.Context(), policy)
		if err != nil {
			problem(w, http.StatusConflict, "egress_plan_conflict", err)
			return
		}
		plan = FinalizeEgressPlan(plan, previous)
		token := ""
		if plan.RequiresConfirmation {
			token, err = signer.Issue(plan, 5*time.Minute)
			if err != nil {
				problemCode(w, http.StatusInternalServerError, "confirmation_failed")
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"plan": plan, "confirmationToken": token, "expiresInSeconds": 300})
	})))
	mux.Handle("POST /api/v1/routeros/plans/egress/{id}/execute", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if executor == nil || signer == nil || replay == nil || policies == nil || planner == nil || audit == nil {
			problemCode(w, http.StatusServiceUnavailable, "egress_executor_not_configured")
			return
		}
		var input egressExecution
		if err := decode(r, &input); err != nil {
			problem(w, http.StatusBadRequest, "invalid_json", err)
			return
		}
		if len(input.Plan.Operations) > routeros.MaxEgressOperations {
			problemCode(w, http.StatusUnprocessableEntity, "egress_plan_too_large")
			return
		}
		if input.Plan.PolicyID != r.PathValue("id") || input.Plan.Policy.ID != input.Plan.PolicyID || !input.Plan.RequiresConfirmation || strings.TrimSpace(input.ConfirmationToken) == "" {
			problemCode(w, http.StatusConflict, "confirmation_invalid")
			return
		}
		if err := signer.Verify(input.ConfirmationToken, input.Plan); err != nil {
			problem(w, http.StatusConflict, "confirmation_invalid", err)
			return
		}
		if err := ValidateEgressPolicyTransition(r.Context(), policies, input.Plan); err != nil {
			problem(w, http.StatusConflict, "egress_plan_stale", routeros.ErrEgressPlanStale)
			return
		}
		latest, err := planner.PlanDeviceEgress(r.Context(), input.Plan.Policy)
		if err != nil {
			problem(w, http.StatusConflict, "egress_plan_stale", err)
			return
		}
		latest = FinalizeEgressPlan(latest, input.Plan.PreviousPolicy)
		if !routeros.EqualEgressPlans(input.Plan, latest) {
			problem(w, http.StatusConflict, "egress_plan_stale", routeros.ErrEgressPlanStale)
			return
		}
		if err := replay.ConsumeReplay(r.Context(), confirmationDigest(input.ConfirmationToken), time.Now().UTC().Add(15*time.Minute)); err != nil {
			problem(w, http.StatusConflict, "confirmation_replayed", err)
			return
		}
		changes := make([]map[string]string, 0, len(input.Plan.Operations))
		for _, operation := range input.Plan.Operations {
			changes = append(changes, map[string]string{"method": operation.Method, "path": operation.Path, "summary": operation.Summary})
		}
		if input.Plan.PreviousPolicy == nil || !domain.EqualDevicePolicies(*input.Plan.PreviousPolicy, input.Plan.Policy) {
			changes = append(changes, map[string]string{"method": "UPSERT", "path": "foxos/device-policies/" + input.Plan.PolicyID, "summary": "回读成功后保存设备出口策略"})
		}
		event := domain.AuditEvent{ID: randomID(), Action: "routeros.egress", TargetID: input.Plan.PolicyID, Outcome: domain.AuditStarted, Details: requestAuditDetails(r, map[string]any{"egress": input.Plan.Egress, "operationCount": len(input.Plan.Operations), "changes": changes, "policyBefore": input.Plan.PreviousPolicy, "policyAfter": input.Plan.Policy})}
		if err := audit.SaveAudit(r.Context(), event); err != nil {
			problemCode(w, http.StatusInternalServerError, "audit_start_failed")
			return
		}
		if jobs != nil {
			job, err := jobs.Submit(r.Context(), "routeros.egress", confirmationDigest(input.ConfirmationToken), map[string]any{"plan": input.Plan, "auditId": event.ID, "actor": event.Details["actor"], "source": event.Details["source"]})
			if err != nil {
				event.Outcome = domain.AuditFailed
				event.Details["errorClass"] = "egress_job_failed"
				_ = audit.SaveAudit(r.Context(), event)
				problemCode(w, http.StatusInternalServerError, "egress_job_failed")
				return
			}
			event.Details["jobId"] = job.ID
			_ = audit.SaveAudit(r.Context(), event)
			writeJSON(w, http.StatusAccepted, map[string]any{"status": domain.JobQueued, "job": renderJob(job), "auditId": event.ID})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if len(input.Plan.Operations) > 0 {
			err = executor.Execute(ctx, input.Plan)
		}
		if err != nil {
			event.Outcome = domain.AuditFailed
			event.Details["rolledBack"] = errors.Is(err, routeros.ErrEgressRolledBack)
			event.Details["errorClass"] = egressErrorClass(err)
			_ = audit.SaveAudit(r.Context(), event)
			problem(w, http.StatusBadGateway, "egress_apply_failed", err)
			return
		}
		if err := policies.SaveDevicePolicy(ctx, input.Plan.Policy); err != nil {
			event.Outcome = domain.AuditFailed
			event.Details["errorClass"] = "egress_policy_persist_failed"
			event.Details["rolledBack"] = false
			if len(input.Plan.Operations) > 0 {
				if compensationErr := executor.Compensate(ctx, input.Plan); compensationErr != nil {
					event.Details["errorClass"] = "egress_policy_persist_rollback_failed"
				} else {
					event.Details["rolledBack"] = true
				}
			}
			_ = audit.SaveAudit(r.Context(), event)
			problemCode(w, http.StatusInternalServerError, event.Details["errorClass"].(string))
			return
		}
		event.Outcome = domain.AuditSucceeded
		event.Details["rolledBack"] = false
		_ = audit.SaveAudit(r.Context(), event)
		writeJSON(w, http.StatusOK, map[string]any{"status": "SUCCEEDED", "policyId": input.Plan.PolicyID, "auditId": event.ID})
	})))
}

func FinalizeEgressPlan(plan routeros.EgressPlan, previous *domain.DevicePolicy) routeros.EgressPlan {
	if previous != nil {
		copy := *previous
		plan.PreviousPolicy = &copy
	}
	policyChanged := previous == nil || !domain.EqualDevicePolicies(*previous, plan.Policy)
	plan.RequiresConfirmation = len(plan.Operations) > 0 || policyChanged
	if policyChanged {
		plan.Warnings = append(plan.Warnings, "成功回读 RouterOS 后才会持久化目标设备策略")
	}
	return plan
}

func ValidateEgressPolicyTransition(ctx context.Context, policies DevicePolicyStore, plan routeros.EgressPlan) error {
	current, err := policies.DevicePolicy(ctx, plan.PolicyID)
	if plan.PreviousPolicy == nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return routeros.ErrEgressPlanStale
	}
	if err != nil || !domain.EqualDevicePolicies(current, *plan.PreviousPolicy) {
		return routeros.ErrEgressPlanStale
	}
	return nil
}

func egressErrorClass(err error) string {
	switch {
	case errors.Is(err, routeros.ErrCompensationFailed):
		return "egress_compensation_failed"
	case errors.Is(err, routeros.ErrEgressRolledBack):
		return "egress_rolled_back"
	case errors.Is(err, routeros.ErrEgressPlanStale):
		return "egress_plan_stale"
	default:
		return "egress_apply_failed"
	}
}
