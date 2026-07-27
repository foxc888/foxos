package api

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/foxc888/foxos/internal/confirmation"
	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/subscription"
)

type SubscriptionStore interface {
	SaveSubscription(context.Context, domain.Subscription) error
	Subscription(context.Context, string) (domain.Subscription, error)
	Subscriptions(context.Context) ([]domain.Subscription, error)
	DeleteSubscription(context.Context, string) error
	UpdateSubscriptionResult(context.Context, string, string, string, bool) error
}

type SubscriptionFetcher interface {
	Fetch(context.Context, string) (subscription.Result, error)
}

type subscriptionInput struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Enabled  bool   `json:"enabled"`
	Interval int    `json:"interval"`
}

type subscriptionOutput struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	Enabled       bool   `json:"enabled"`
	Interval      int    `json:"interval"`
	LastDigest    string `json:"lastDigest,omitempty"`
	LastSuccessAt string `json:"lastSuccessAt,omitempty"`
	LastAttemptAt string `json:"lastAttemptAt,omitempty"`
	LastError     string `json:"lastError,omitempty"`
}

func renderSubscription(item domain.Subscription) subscriptionOutput {
	return subscriptionOutput{ID: item.ID, Name: item.Name, URL: displaySubscriptionURL(item.URL), Enabled: item.Enabled, Interval: item.Interval, LastDigest: item.LastDigest, LastSuccessAt: formatTime(item.LastSuccessAt), LastAttemptAt: formatTime(item.LastAttemptAt), LastError: item.LastError}
}

type subscriptionDeleteStore interface {
	DeleteSubscriptionWithNodes(context.Context, string) error
}

type subscriptionDeletePlan struct {
	Action         string   `json:"action"`
	SubscriptionID string   `json:"subscriptionId"`
	NodeIDs        []string `json:"nodeIds"`
	NodeCount      int      `json:"nodeCount"`
}

func (s *Server) RegisterSubscriptions(mux *http.ServeMux, store SubscriptionStore, nodes subscription.NodeStore, fetcher SubscriptionFetcher, signer *confirmation.Signer, replay MihomoReplay, jobs MihomoJobSubmitter, audit AuditStore) {
	if store == nil {
		return
	}
	updater := subscription.Updater{Sources: store, Nodes: nodes, Fetcher: fetcher}
	mux.Handle("GET /api/v1/subscriptions", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items, err := store.Subscriptions(r.Context())
		if err != nil {
			problemCode(w, http.StatusInternalServerError, "subscriptions_failed")
			return
		}
		out := make([]subscriptionOutput, 0, len(items))
		for _, item := range items {
			out = append(out, renderSubscription(item))
		}
		writeJSON(w, http.StatusOK, out)
	})))
	mux.Handle("POST /api/v1/subscriptions", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input subscriptionInput
		if err := decode(r, &input); err != nil {
			problem(w, http.StatusBadRequest, "invalid_json", err)
			return
		}
		if _, err := subscription.ValidateURL(input.URL); err != nil {
			problem(w, http.StatusUnprocessableEntity, "subscription_url_blocked", err)
			return
		}
		if input.ID == "" {
			input.ID = randomID()
		} else {
			problemCode(w, http.StatusUnprocessableEntity, "subscription_id_server_generated")
			return
		}
		if input.Interval == 0 {
			input.Interval = 6 * 60 * 60
		}
		item := domain.Subscription{ID: input.ID, Name: strings.TrimSpace(input.Name), URL: input.URL, Enabled: input.Enabled, Interval: input.Interval}
		if err := store.SaveSubscription(r.Context(), item); err != nil {
			problem(w, http.StatusUnprocessableEntity, "subscription_invalid", err)
			return
		}
		if audit != nil {
			_ = audit.SaveAudit(r.Context(), domain.AuditEvent{ID: randomID(), Action: "subscription.create", TargetID: item.ID, Outcome: domain.AuditSucceeded, Details: requestAuditDetails(r, map[string]any{"enabled": item.Enabled, "interval": item.Interval})})
		}
		writeJSON(w, http.StatusCreated, renderSubscription(item))
	})))
	mux.Handle("PUT /api/v1/subscriptions/{id}", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input subscriptionInput
		if err := decode(r, &input); err != nil {
			problem(w, http.StatusBadRequest, "invalid_json", err)
			return
		}
		if _, err := subscription.ValidateURL(input.URL); err != nil {
			problem(w, http.StatusUnprocessableEntity, "subscription_url_blocked", err)
			return
		}
		input.ID = r.PathValue("id")
		if input.Interval == 0 {
			input.Interval = 6 * 60 * 60
		}
		item, err := store.Subscription(r.Context(), input.ID)
		if err != nil {
			problemCode(w, http.StatusNotFound, "subscription_not_found")
			return
		}
		item.Name = strings.TrimSpace(input.Name)
		item.URL = input.URL
		item.Enabled = input.Enabled
		item.Interval = input.Interval
		if err := store.SaveSubscription(r.Context(), item); err != nil {
			problem(w, http.StatusUnprocessableEntity, "subscription_invalid", err)
			return
		}
		if audit != nil {
			_ = audit.SaveAudit(r.Context(), domain.AuditEvent{ID: randomID(), Action: "subscription.settings", TargetID: item.ID, Outcome: domain.AuditSucceeded, Details: requestAuditDetails(r, map[string]any{"enabled": item.Enabled, "interval": item.Interval})})
		}
		stored, err := store.Subscription(r.Context(), input.ID)
		if err != nil {
			problemCode(w, http.StatusInternalServerError, "subscription_state_failed")
			return
		}
		writeJSON(w, http.StatusOK, renderSubscription(stored))
	})))
	mux.Handle("DELETE /api/v1/subscriptions/{id}", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		problemCode(w, http.StatusConflict, "confirmation_required")
	})))
	mux.Handle("POST /api/v1/subscriptions/{id}/preview", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fetcher == nil || nodes == nil || signer == nil {
			problemCode(w, http.StatusServiceUnavailable, "subscription_fetch_unavailable")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		preview, err := updater.Preview(ctx, r.PathValue("id"))
		if err != nil {
			problem(w, http.StatusBadGateway, "subscription_preview_failed", err)
			return
		}
		token, err := signer.Issue(preview.Plan, 5*time.Minute)
		if err != nil {
			problemCode(w, http.StatusInternalServerError, "confirmation_failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"digest": preview.Plan.Digest, "nodeCount": preview.Plan.NodeCount, "nodes": redactNodeOutputs(preview.Nodes), "plan": preview.Plan, "confirmationToken": token, "expiresInSeconds": 300})
	})))
	mux.Handle("POST /api/v1/subscriptions/{id}/update", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fetcher == nil || nodes == nil || signer == nil || replay == nil || audit == nil {
			problemCode(w, http.StatusServiceUnavailable, "subscription_update_unavailable")
			return
		}
		var input struct {
			Plan              subscription.UpdatePlan `json:"plan"`
			ConfirmationToken string                  `json:"confirmationToken"`
		}
		if err := decode(r, &input); err != nil {
			problem(w, http.StatusBadRequest, "invalid_json", err)
			return
		}
		if input.Plan.Action != "subscription.update" || input.Plan.SubscriptionID != r.PathValue("id") || strings.TrimSpace(input.ConfirmationToken) == "" {
			problemCode(w, http.StatusConflict, "confirmation_invalid")
			return
		}
		if err := signer.Verify(input.ConfirmationToken, input.Plan); err != nil {
			problem(w, http.StatusConflict, "confirmation_invalid", err)
			return
		}
		event := domain.AuditEvent{ID: randomID(), Action: "subscription.update", TargetID: input.Plan.SubscriptionID, Outcome: domain.AuditStarted, Details: requestAuditDetails(r, map[string]any{"digest": input.Plan.Digest, "addCount": input.Plan.AddCount, "updateCount": input.Plan.UpdateCount, "removeCount": input.Plan.RemoveCount, "removedNodeIds": input.Plan.RemovedNodeIDs})}
		if err := audit.SaveAudit(r.Context(), event); err != nil {
			problemCode(w, http.StatusInternalServerError, "audit_start_failed")
			return
		}
		if err := replay.ConsumeReplay(r.Context(), confirmationDigest(input.ConfirmationToken), time.Now().UTC().Add(15*time.Minute)); err != nil {
			event.Outcome = domain.AuditFailed
			event.Details["errorClass"] = "confirmation_replayed"
			_ = audit.SaveAudit(r.Context(), event)
			problem(w, http.StatusConflict, "confirmation_replayed", err)
			return
		}
		if jobs != nil {
			job, err := jobs.Submit(r.Context(), "subscription.update", confirmationDigest(input.ConfirmationToken), map[string]any{"subscriptionId": input.Plan.SubscriptionID, "plan": input.Plan, "auditId": event.ID, "scheduled": false, "actor": event.Details["actor"], "source": event.Details["source"]})
			if err != nil {
				event.Outcome = domain.AuditFailed
				event.Details["errorClass"] = "subscription_job_failed"
				_ = audit.SaveAudit(r.Context(), event)
				problemCode(w, http.StatusInternalServerError, "subscription_job_failed")
				return
			}
			event.Details["jobId"] = job.ID
			_ = audit.SaveAudit(r.Context(), event)
			writeJSON(w, http.StatusAccepted, map[string]any{"status": domain.JobQueued, "job": renderJob(job)})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		result, err := updater.Apply(ctx, input.Plan.SubscriptionID, &input.Plan)
		if err != nil {
			event.Outcome = domain.AuditFailed
			event.Details["errorClass"] = "subscription_update_failed"
			_ = audit.SaveAudit(r.Context(), event)
			problem(w, http.StatusConflict, "subscription_update_failed", err)
			return
		}
		event.Outcome = domain.AuditSucceeded
		_ = audit.SaveAudit(r.Context(), event)
		writeJSON(w, http.StatusOK, map[string]any{"status": "updated", "digest": result.Plan.Digest, "nodeCount": result.Plan.NodeCount})
	})))
	mux.Handle("POST /api/v1/subscriptions/{id}/delete/plan", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if signer == nil || nodes == nil {
			problemCode(w, http.StatusServiceUnavailable, "confirmation_unavailable")
			return
		}
		if _, err := store.Subscription(r.Context(), r.PathValue("id")); err != nil {
			problemCode(w, http.StatusNotFound, "subscription_not_found")
			return
		}
		items, err := nodes.SubscriptionNodes(r.Context(), r.PathValue("id"))
		if err != nil {
			problemCode(w, http.StatusInternalServerError, "subscription_nodes_failed")
			return
		}
		plan := subscriptionDeletePlan{Action: "subscription.delete", SubscriptionID: r.PathValue("id"), NodeCount: len(items)}
		for _, node := range items {
			plan.NodeIDs = append(plan.NodeIDs, node.ID)
		}
		sort.Strings(plan.NodeIDs)
		token, err := signer.Issue(plan, 5*time.Minute)
		if err != nil {
			problemCode(w, http.StatusInternalServerError, "confirmation_failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"plan": plan, "confirmationToken": token, "expiresInSeconds": 300, "warnings": []string{"订阅定义和未被引用的来源节点会被删除", "若节点仍被代理组或设备策略引用，删除会整体失败"}})
	})))
	mux.Handle("POST /api/v1/subscriptions/{id}/delete", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deleteStore, ok := store.(subscriptionDeleteStore)
		if !ok || signer == nil || replay == nil || audit == nil || nodes == nil {
			problemCode(w, http.StatusServiceUnavailable, "subscription_delete_unavailable")
			return
		}
		var input struct {
			ConfirmationToken string `json:"confirmationToken"`
		}
		if err := decode(r, &input); err != nil {
			problem(w, http.StatusBadRequest, "invalid_json", err)
			return
		}
		items, err := nodes.SubscriptionNodes(r.Context(), r.PathValue("id"))
		if err != nil {
			problemCode(w, http.StatusConflict, "subscription_delete_stale")
			return
		}
		plan := subscriptionDeletePlan{Action: "subscription.delete", SubscriptionID: r.PathValue("id"), NodeCount: len(items)}
		for _, node := range items {
			plan.NodeIDs = append(plan.NodeIDs, node.ID)
		}
		sort.Strings(plan.NodeIDs)
		if err := signer.Verify(input.ConfirmationToken, plan); err != nil {
			problem(w, http.StatusConflict, "confirmation_invalid", err)
			return
		}
		event := domain.AuditEvent{ID: randomID(), Action: "subscription.delete", TargetID: plan.SubscriptionID, Outcome: domain.AuditStarted, Details: requestAuditDetails(r, map[string]any{"nodeCount": plan.NodeCount, "nodeIds": plan.NodeIDs})}
		if err := audit.SaveAudit(r.Context(), event); err != nil {
			problemCode(w, http.StatusInternalServerError, "audit_start_failed")
			return
		}
		if err := replay.ConsumeReplay(r.Context(), confirmationDigest(input.ConfirmationToken), time.Now().UTC().Add(15*time.Minute)); err != nil {
			event.Outcome = domain.AuditFailed
			event.Details["errorClass"] = "confirmation_replayed"
			_ = audit.SaveAudit(r.Context(), event)
			problem(w, http.StatusConflict, "confirmation_replayed", err)
			return
		}
		if err := deleteStore.DeleteSubscriptionWithNodes(r.Context(), plan.SubscriptionID); err != nil {
			event.Outcome = domain.AuditFailed
			event.Details["errorClass"] = "subscription_delete_failed"
			_ = audit.SaveAudit(r.Context(), event)
			problem(w, http.StatusConflict, "subscription_delete_failed", err)
			return
		}
		event.Outcome = domain.AuditSucceeded
		_ = audit.SaveAudit(r.Context(), event)
		writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "subscriptionId": plan.SubscriptionID, "nodeCount": plan.NodeCount})
	})))
}

func redactNodeOutputs(nodes []domain.Node) []nodeOutput {
	out := make([]nodeOutput, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, output(node))
	}
	return out
}

func displaySubscriptionURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "invalid subscription URL"
	}
	if parsed.RawQuery != "" {
		parsed.RawQuery = "redacted"
	}
	return parsed.String()
}
