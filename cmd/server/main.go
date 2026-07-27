package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/foxc888/foxos/internal/alerting"
	"github.com/foxc888/foxos/internal/api"
	"github.com/foxc888/foxos/internal/backup"
	"github.com/foxc888/foxos/internal/config"
	"github.com/foxc888/foxos/internal/confirmation"
	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/mihomo"
	"github.com/foxc888/foxos/internal/mosdns"
	"github.com/foxc888/foxos/internal/routeros"
	"github.com/foxc888/foxos/internal/store/sqlite"
	"github.com/foxc888/foxos/internal/subscription"
	"github.com/foxc888/foxos/internal/task"
)

var version = "dev"

func main() {
	address := flag.String("listen", ":8090", "HTTP listen address")
	staticDir := flag.String("static", "web/dist", "built frontend directory")
	databasePath := flag.String("database", "data/foxos.db", "SQLite database path")
	flag.Parse()

	runtimeConfig, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	signer, err := confirmation.New([]byte(runtimeConfig.ConfirmationKey))
	if err != nil {
		log.Fatal(err)
	}
	store, err := sqlite.Open(*databasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	jobManager, err := task.New(context.Background(), store)
	if err != nil {
		log.Fatal(err)
	}
	defer jobManager.Close()
	app, err := api.New(store, runtimeConfig.APIToken)
	if err != nil {
		log.Fatal(err)
	}

	var ros api.RouterOSReader
	var leases api.BindingStateReader
	var l2tp api.L2TPReader
	var bindingExecutor api.BindingExecutor
	var egressPlanner api.EgressPlanner
	var egressExecutor api.EgressExecutor
	var routerMonitor alerting.RouterReader
	if runtimeConfig.RouterOS.URL != "" {
		client, err := routeros.NewClient(runtimeConfig.RouterOS.URL, runtimeConfig.RouterOS.Username, runtimeConfig.RouterOS.Password)
		if err != nil {
			log.Fatal(err)
		}
		ros = client
		routerMonitor = client
		leases = client
		l2tp = client
		executor, err := routeros.NewBindingExecutor(client, signer)
		if err != nil {
			log.Fatal(err)
		}
		bindingExecutor = executor.WithVerifier(client)
		egressPlanner = client
		egressExecutor = routeros.EgressExecutor{Writer: client}
	}
	var clash api.MihomoReader
	var mihomoService api.MihomoService
	var mihomoApplier *mihomo.Applier
	var mihomoMonitor alerting.MihomoReader
	var mihomoNodeProbe api.MihomoNodeProber
	var mihomoExitProbe api.MihomoExitProber
	if runtimeConfig.Mihomo.URL != "" {
		baseConfig, err := os.ReadFile(runtimeConfig.Mihomo.BaseConfigPath)
		if err != nil {
			log.Fatalf("read trusted Mihomo base config: %v", err)
		}
		if err := mihomo.ValidateYAML(baseConfig); err != nil {
			log.Fatalf("validate trusted Mihomo base config: %v", err)
		}
		controller, err := mihomo.NewController(runtimeConfig.Mihomo.URL, runtimeConfig.Mihomo.Secret, runtimeConfig.Mihomo.RuntimeConfigPath)
		if err != nil {
			log.Fatal(err)
		}
		clash = controller
		mihomoMonitor = controller
		mihomoNodeProbe = controller
		validatedRuntime := &mihomo.ValidatedRuntime{
			Runtime: controller,
			Validator: mihomo.CommandValidator{
				BinaryPath: runtimeConfig.Mihomo.ValidatorBinary,
				DataDir:    filepath.Dir(runtimeConfig.Mihomo.BaseConfigPath),
			},
		}
		mihomoApplier = &mihomo.Applier{ConfigPath: runtimeConfig.Mihomo.LocalConfigPath, BackupDir: runtimeConfig.Mihomo.BackupDir, Runtime: validatedRuntime}
		mihomoService = &mihomo.Service{Store: store, Applier: mihomoApplier, BaseConfig: baseConfig}
	}
	if runtimeConfig.Mihomo.ProxyURL != "" {
		mihomoExitProbe, err = mihomo.NewExitProbe(runtimeConfig.Mihomo.ProxyURL)
		if err != nil {
			log.Fatal(err)
		}
	}
	var mosdnsReader api.MosDNSReader
	if runtimeConfig.MosDNSURL != "" {
		client, err := mosdns.NewClient(runtimeConfig.MosDNSURL)
		if err != nil {
			log.Fatal(err)
		}
		mosdnsReader = client
	}

	mux := http.NewServeMux()
	paths := []string{filepath.Dir(*databasePath)}
	dependencies := []api.HealthDependency{
		{Name: "routeros", Configured: ros != nil, Check: func(ctx context.Context) error { _, err := ros.Resource(ctx); return err }},
		{Name: "mihomo", Configured: clash != nil, Check: clashCheck(clash)},
		{Name: "mosdns", Configured: mosdnsReader != nil, Check: func(ctx context.Context) error {
			status := mosdnsReader.Status(ctx)
			if !status.Online {
				return errors.New("MosDNS unavailable")
			}
			return nil
		}},
	}
	if runtimeConfig.Mihomo.URL != "" {
		paths = append(paths, runtimeConfig.Mihomo.LocalConfigPath, runtimeConfig.Mihomo.BackupDir)
	}
	app.RegisterHealth(mux, api.HealthOptions{Store: store, RequiredPaths: paths, Dependencies: dependencies, Version: version})
	app.Register(mux)
	app.RegisterGroups(mux, store)
	app.RegisterDevicePolicies(mux, store)
	app.RegisterAudit(mux, store)
	app.RegisterStatus(mux, ros, clash)
	app.RegisterDevices(mux, ros, store, store)
	app.RegisterMosDNS(mux, mosdnsReader)
	app.RegisterL2TP(mux, l2tp)
	app.RegisterBindingPlan(mux, leases, signer, bindingExecutor, confirmation.NewReplayGuard(), store, store)
	var egressReadiness api.EgressReadinessReader
	if client, ok := ros.(api.EgressReadinessReader); ok {
		egressReadiness = client
	}
	app.RegisterEgressCapabilities(mux, egressReadiness)
	app.RegisterEgress(mux, store, egressPlanner, signer, store, egressExecutor, jobManager, store)
	app.RegisterMihomo(mux, mihomoService, store, signer, store, jobManager, store)
	app.RegisterMihomoProbes(mux, mihomoNodeProbe, mihomoExitProbe)
	app.RegisterJobs(mux, jobManager)
	subscriptionFetcher := subscription.Fetcher{UserAgent: "FoxOS subscription updater/1"}
	subscriptionUpdater := subscription.Updater{Sources: store, Nodes: store, Fetcher: subscriptionFetcher}
	app.RegisterSubscriptions(mux, store, store, subscriptionFetcher, signer, store, jobManager, store)
	app.RegisterAlerts(mux, store)
	backupService := backup.Service{Database: store, MihomoPath: runtimeConfig.Mihomo.LocalConfigPath, Directory: runtimeConfig.BackupDir, Retention: 20}
	if mihomoApplier != nil {
		backupService.MihomoRestore = func(ctx context.Context, body []byte) error {
			_, err := mihomoApplier.Apply(ctx, body)
			return err
		}
	}
	app.RegisterBackups(mux, backupService, signer, store, jobManager, store)
	registerMihomoJobs(jobManager, mihomoService, store)
	registerEgressJobs(jobManager, store, egressPlanner, egressExecutor, store)
	registerBackupJobs(jobManager, backupService, store)
	registerSubscriptionJobs(jobManager, subscriptionUpdater, store)
	alertMonitor, err := alerting.Start(context.Background(), alerting.Evaluator{Store: store, RouterOS: routerMonitor, Mihomo: mihomoMonitor, MosDNS: mosdnsReader}, time.Minute, func(err error) {
		log.Printf("alert evaluation incomplete: %v", err)
	})
	if err != nil {
		log.Fatal(err)
	}
	defer alertMonitor.Close()
	subscriptionScheduler, err := subscription.NewScheduler(context.Background(), store, jobManager, time.Minute)
	if err != nil {
		log.Fatal(err)
	}
	defer subscriptionScheduler.Close()
	if info, err := os.Stat(*staticDir); err == nil && info.IsDir() {
		mux.Handle("/", http.FileServer(http.Dir(*staticDir)))
	}

	server := &http.Server{Addr: *address, Handler: securityHeaders(mux), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	log.Printf("FoxOS %s listening on %s", version, *address)
	log.Fatal(server.ListenAndServe())
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func clashCheck(reader api.MihomoReader) func(context.Context) error {
	return func(ctx context.Context) error {
		if reader == nil {
			return errors.New("Mihomo unavailable")
		}
		return reader.Healthy(ctx)
	}
}

func registerMihomoJobs(manager *task.Manager, service api.MihomoService, audit api.AuditStore) {
	if manager == nil || service == nil {
		return
	}
	_ = manager.Register("mihomo.apply", func(ctx context.Context, job domain.Job, progress task.Progress) (map[string]any, domain.JobStatus, error) {
		event := mihomoAuditEvent(job, "mihomo.apply")
		draft, err := decodeDraftRequest(job.Request["draft"])
		if err != nil {
			return failMihomoJobAudit(ctx, audit, event, "mihomo_request_invalid", mihomo.ApplyResult{}, err)
		}
		preview, err := service.Preview(ctx, draft)
		if err != nil {
			return failMihomoJobAudit(ctx, audit, event, "mihomo_preview_failed", mihomo.ApplyResult{}, err)
		}
		diff, truncated := boundedAuditDiff(preview.Diff)
		event.Details["digest"] = preview.Digest
		event.Details["diff"] = diff
		event.Details["diffTruncated"] = truncated
		event.Details["containsRedactedSecrets"] = preview.HasSecret
		digest, _ := job.Request["digest"].(string)
		digest = strings.ToLower(strings.TrimSpace(digest))
		if digest == "" || !strings.EqualFold(digest, preview.Digest) {
			return failMihomoJobAudit(ctx, audit, event, "mihomo_apply_stale", mihomo.ApplyResult{}, errors.New("Mihomo configuration changed since preview"))
		}
		if audit == nil {
			return map[string]any{"errorClass": "mihomo_audit_unavailable"}, domain.JobFailed, errors.New("Mihomo audit store is unavailable")
		}
		if err := audit.SaveAudit(ctx, event); err != nil {
			return map[string]any{"errorClass": "mihomo_audit_failed"}, domain.JobFailed, errors.New("Mihomo audit start failed")
		}
		progress(domain.JobVerifying, 35)
		label, _ := job.Request["label"].(string)
		result, snapshot, err := service.ApplyPreview(ctx, draft, digest, label)
		if err != nil {
			status, errorClass := mihomoFailure("mihomo_apply", result, err)
			event.Outcome = domain.AuditFailed
			event.Details["rolledBack"] = result.RolledBack
			event.Details["errorClass"] = errorClass
			_ = audit.SaveAudit(ctx, event)
			return map[string]any{"rolledBack": result.RolledBack, "errorClass": errorClass}, status, err
		}
		progress(domain.JobVerifying, 95)
		event.Outcome = domain.AuditSucceeded
		event.Details["snapshotId"] = snapshot.ID
		event.Details["rolledBack"] = result.RolledBack
		if err := audit.SaveAudit(ctx, event); err != nil {
			return map[string]any{"snapshotId": snapshot.ID, "rolledBack": result.RolledBack, "errorClass": "mihomo_audit_finalize_failed"}, domain.JobFailed, errors.New("Mihomo audit finalization failed")
		}
		return map[string]any{"snapshotId": snapshot.ID, "rolledBack": result.RolledBack}, domain.JobSucceeded, nil
	})
	_ = manager.Register("mihomo.restore", func(ctx context.Context, job domain.Job, progress task.Progress) (map[string]any, domain.JobStatus, error) {
		event := mihomoAuditEvent(job, "mihomo.restore")
		id, _ := job.Request["snapshotId"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			return failMihomoJobAudit(ctx, audit, event, "mihomo_restore_request_invalid", mihomo.ApplyResult{}, errors.New("Mihomo snapshot ID is required"))
		}
		event.Details["snapshotId"] = id
		if audit == nil {
			return map[string]any{"errorClass": "mihomo_restore_audit_unavailable"}, domain.JobFailed, errors.New("Mihomo audit store is unavailable")
		}
		if err := audit.SaveAudit(ctx, event); err != nil {
			return map[string]any{"errorClass": "mihomo_restore_audit_failed"}, domain.JobFailed, errors.New("Mihomo audit start failed")
		}
		progress(domain.JobVerifying, 40)
		label, _ := job.Request["label"].(string)
		result, snapshot, err := service.Restore(ctx, id, label)
		if err != nil {
			status, errorClass := mihomoFailure("mihomo_restore", result, err)
			event.Outcome = domain.AuditFailed
			event.Details["rolledBack"] = result.RolledBack
			event.Details["errorClass"] = errorClass
			_ = audit.SaveAudit(ctx, event)
			return map[string]any{"rolledBack": result.RolledBack, "errorClass": errorClass}, status, err
		}
		event.Outcome = domain.AuditSucceeded
		event.Details["restoredSnapshotId"] = snapshot.ID
		event.Details["rolledBack"] = result.RolledBack
		if err := audit.SaveAudit(ctx, event); err != nil {
			return map[string]any{"snapshotId": snapshot.ID, "rolledBack": result.RolledBack, "errorClass": "mihomo_restore_audit_finalize_failed"}, domain.JobFailed, errors.New("Mihomo audit finalization failed")
		}
		return map[string]any{"snapshotId": snapshot.ID, "rolledBack": result.RolledBack}, domain.JobSucceeded, nil
	})
}

func mihomoAuditEvent(job domain.Job, action string) domain.AuditEvent {
	return domain.AuditEvent{
		ID:       job.ID + "-audit",
		Action:   action,
		TargetID: job.ID,
		Outcome:  domain.AuditStarted,
		Details: map[string]any{
			"jobId":  job.ID,
			"actor":  job.Request["actor"],
			"source": job.Request["source"],
		},
	}
}

func failMihomoJobAudit(ctx context.Context, audit api.AuditStore, event domain.AuditEvent, errorClass string, result mihomo.ApplyResult, cause error) (map[string]any, domain.JobStatus, error) {
	event.Outcome = domain.AuditFailed
	event.Details["errorClass"] = errorClass
	event.Details["rolledBack"] = result.RolledBack
	if audit != nil {
		_ = audit.SaveAudit(ctx, event)
	}
	return map[string]any{"rolledBack": result.RolledBack, "errorClass": errorClass}, domain.JobFailed, cause
}

func boundedAuditDiff(value string) (string, bool) {
	const limit = 64 << 10
	if len(value) <= limit {
		return value, false
	}
	value = value[:limit]
	for value != "" && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "\n# diff truncated by FoxOS\n", true
}

func mihomoFailure(prefix string, result mihomo.ApplyResult, err error) (domain.JobStatus, string) {
	if errors.Is(err, mihomo.ErrRollbackFailed) {
		return domain.JobFailed, prefix + "_rollback_failed"
	}
	if result.RolledBack {
		return domain.JobRolledBack, prefix + "_rolled_back"
	}
	return domain.JobFailed, prefix + "_failed"
}

func registerEgressJobs(manager *task.Manager, policies api.DevicePolicyStore, planner api.EgressPlanner, executor api.EgressExecutor, audit api.AuditStore) {
	if manager == nil || policies == nil || planner == nil || executor == nil {
		return
	}
	_ = manager.Register("routeros.egress", func(ctx context.Context, job domain.Job, progress task.Progress) (map[string]any, domain.JobStatus, error) {
		auditID, _ := job.Request["auditId"].(string)
		if auditID == "" {
			auditID = job.ID + "-audit"
		}
		plan, err := decodeEgressPlan(job.Request["plan"])
		if err != nil {
			event := domain.AuditEvent{ID: auditID, Action: "routeros.egress", TargetID: job.ID, Outcome: domain.AuditFailed, Details: map[string]any{"jobId": job.ID, "errorClass": "egress_request_invalid", "actor": job.Request["actor"], "source": job.Request["source"]}}
			if audit != nil {
				_ = audit.SaveAudit(ctx, event)
			}
			return map[string]any{"auditId": auditID, "errorClass": "egress_request_invalid"}, domain.JobFailed, err
		}
		changes := make([]map[string]string, 0, len(plan.Operations)+1)
		for _, operation := range plan.Operations {
			changes = append(changes, map[string]string{"method": operation.Method, "path": operation.Path, "summary": operation.Summary})
		}
		if plan.PreviousPolicy == nil || !domain.EqualDevicePolicies(*plan.PreviousPolicy, plan.Policy) {
			changes = append(changes, map[string]string{"method": "UPSERT", "path": "foxos/device-policies/" + plan.PolicyID, "summary": "回读成功后保存设备出口策略"})
		}
		event := domain.AuditEvent{ID: auditID, Action: "routeros.egress", TargetID: plan.PolicyID, Outcome: domain.AuditStarted, Details: map[string]any{"jobId": job.ID, "egress": plan.Egress, "operationCount": len(plan.Operations), "changes": changes, "policyBefore": plan.PreviousPolicy, "policyAfter": plan.Policy, "actor": job.Request["actor"], "source": job.Request["source"]}}
		if audit == nil {
			return map[string]any{"auditId": auditID, "errorClass": "egress_audit_unavailable"}, domain.JobFailed, errors.New("egress audit store is unavailable")
		}
		if err := audit.SaveAudit(ctx, event); err != nil {
			return map[string]any{"auditId": auditID, "errorClass": "egress_audit_failed"}, domain.JobFailed, errors.New("egress audit start failed")
		}
		progress(domain.JobRunning, 15)
		err = api.ValidateEgressPolicyTransition(ctx, policies, plan)
		if err == nil {
			var latest routeros.EgressPlan
			latest, err = planner.PlanDeviceEgress(ctx, plan.Policy)
			latest = api.FinalizeEgressPlan(latest, plan.PreviousPolicy)
			if err == nil && !routeros.EqualEgressPlans(plan, latest) {
				err = routeros.ErrEgressPlanStale
			}
		}
		if err == nil && len(plan.Operations) > 0 {
			progress(domain.JobVerifying, 35)
			err = executor.Execute(ctx, plan)
		}
		if err != nil {
			status := domain.JobFailed
			errorClass := egressJobErrorClass(err)
			if errors.Is(err, routeros.ErrEgressRolledBack) {
				status = domain.JobRolledBack
			}
			event.Outcome = domain.AuditFailed
			event.Details["errorClass"] = errorClass
			event.Details["rolledBack"] = status == domain.JobRolledBack
			_ = audit.SaveAudit(ctx, event)
			return map[string]any{"auditId": auditID, "rolledBack": status == domain.JobRolledBack, "errorClass": errorClass}, status, err
		}
		progress(domain.JobVerifying, 85)
		if err := policies.SaveDevicePolicy(ctx, plan.Policy); err != nil {
			status := domain.JobFailed
			errorClass := "egress_policy_persist_failed"
			rolledBack := false
			if len(plan.Operations) > 0 {
				if compensationErr := executor.Compensate(ctx, plan); compensationErr != nil {
					errorClass = "egress_policy_persist_rollback_failed"
					err = errors.Join(err, compensationErr)
				} else {
					status = domain.JobRolledBack
					errorClass = "egress_policy_persist_rolled_back"
					rolledBack = true
				}
			}
			event.Outcome = domain.AuditFailed
			event.Details["errorClass"] = errorClass
			event.Details["rolledBack"] = rolledBack
			_ = audit.SaveAudit(ctx, event)
			return map[string]any{"auditId": auditID, "rolledBack": rolledBack, "errorClass": errorClass}, status, err
		}
		event.Outcome = domain.AuditSucceeded
		event.Details["rolledBack"] = false
		if err := audit.SaveAudit(ctx, event); err != nil {
			return map[string]any{"policyId": plan.PolicyID, "auditId": auditID, "rolledBack": false, "errorClass": "egress_audit_finalize_failed"}, domain.JobFailed, errors.New("egress audit finalization failed")
		}
		return map[string]any{"policyId": plan.PolicyID, "auditId": auditID, "rolledBack": false}, domain.JobSucceeded, nil
	})
}

func egressJobErrorClass(err error) string {
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

func registerBackupJobs(manager *task.Manager, service api.BackupService, audit api.AuditStore) {
	if manager == nil || service == nil {
		return
	}
	_ = manager.Register("backup.create", func(ctx context.Context, job domain.Job, progress task.Progress) (map[string]any, domain.JobStatus, error) {
		progress(domain.JobRunning, 10)
		label, _ := job.Request["label"].(string)
		item, err := service.Create(ctx, label)
		event := domain.AuditEvent{ID: job.ID + "-audit", Action: "backup.create", TargetID: job.ID, Outcome: domain.AuditSucceeded, Details: map[string]any{"jobId": job.ID, "actor": job.Request["actor"], "source": job.Request["source"]}}
		if err != nil {
			event.Outcome = domain.AuditFailed
			event.Details["errorClass"] = "backup_create_failed"
			if audit != nil {
				_ = audit.SaveAudit(ctx, event)
			}
			return map[string]any{"errorClass": "backup_create_failed"}, domain.JobFailed, err
		}
		event.TargetID = item.ID
		event.Details["digestCount"] = item.FileCount
		if audit != nil {
			_ = audit.SaveAudit(ctx, event)
		}
		return map[string]any{"manifest": item}, domain.JobSucceeded, nil
	})
	_ = manager.Register("backup.restore", func(ctx context.Context, job domain.Job, progress task.Progress) (map[string]any, domain.JobStatus, error) {
		id, _ := job.Request["backupId"].(string)
		digest, _ := job.Request["digest"].(string)
		auditID, _ := job.Request["auditId"].(string)
		if auditID == "" {
			auditID = job.ID + "-audit"
		}
		event := domain.AuditEvent{ID: auditID, Action: "backup.restore", TargetID: id, Outcome: domain.AuditStarted, Details: map[string]any{"jobId": job.ID, "digest": digest, "actor": job.Request["actor"], "source": job.Request["source"]}}
		progress(domain.JobVerifying, 20)
		err := service.Restore(ctx, id, digest)
		if err != nil {
			event.Outcome = domain.AuditFailed
			event.Details["errorClass"] = "backup_restore_failed"
			if audit != nil {
				_ = audit.SaveAudit(ctx, event)
			}
			return map[string]any{"backupId": id, "errorClass": "backup_restore_failed"}, domain.JobFailed, err
		}
		event.Outcome = domain.AuditSucceeded
		if audit != nil {
			_ = audit.SaveAudit(ctx, event)
		}
		return map[string]any{"backupId": id}, domain.JobSucceeded, nil
	})
}

func registerSubscriptionJobs(manager *task.Manager, updater subscription.Updater, audit api.AuditStore) {
	if manager == nil || updater.Sources == nil || updater.Nodes == nil || updater.Fetcher == nil {
		return
	}
	_ = manager.Register("subscription.update", func(ctx context.Context, job domain.Job, progress task.Progress) (map[string]any, domain.JobStatus, error) {
		id, _ := job.Request["subscriptionId"].(string)
		scheduled, _ := job.Request["scheduled"].(bool)
		auditID, _ := job.Request["auditId"].(string)
		if auditID == "" {
			auditID = job.ID + "-audit"
		}
		action := "subscription.update"
		if scheduled {
			action = "subscription.update.scheduled"
		}
		actor := job.Request["actor"]
		source := job.Request["source"]
		if scheduled {
			actor = "scheduler"
			source = "local"
		}
		event := domain.AuditEvent{ID: auditID, Action: action, TargetID: id, Outcome: domain.AuditStarted, Details: map[string]any{"jobId": job.ID, "scheduled": scheduled, "actor": actor, "source": source}}
		var expected *subscription.UpdatePlan
		if value, found := job.Request["plan"]; found && value != nil {
			body, err := json.Marshal(value)
			if err != nil {
				return nil, domain.JobFailed, err
			}
			var plan subscription.UpdatePlan
			if err := json.Unmarshal(body, &plan); err != nil {
				return nil, domain.JobFailed, err
			}
			expected = &plan
		}
		progress(domain.JobVerifying, 20)
		result, err := updater.Apply(ctx, id, expected)
		if err != nil {
			event.Outcome = domain.AuditFailed
			event.Details["errorClass"] = "subscription_update_failed"
			if errors.Is(err, subscription.ErrContentChanged) {
				event.Details["errorClass"] = "subscription_content_changed"
			}
			if audit != nil {
				_ = audit.SaveAudit(ctx, event)
			}
			return map[string]any{"subscriptionId": id, "oldNodesRetained": true, "errorClass": event.Details["errorClass"]}, domain.JobFailed, err
		}
		event.Outcome = domain.AuditSucceeded
		event.Details["digest"] = result.Plan.Digest
		event.Details["nodeCount"] = result.Plan.NodeCount
		event.Details["addCount"] = result.Plan.AddCount
		event.Details["updateCount"] = result.Plan.UpdateCount
		event.Details["removeCount"] = result.Plan.RemoveCount
		if audit != nil {
			_ = audit.SaveAudit(ctx, event)
		}
		return map[string]any{"subscriptionId": id, "digest": result.Plan.Digest, "nodeCount": result.Plan.NodeCount, "oldNodesRetained": false}, domain.JobSucceeded, nil
	})
}

func decodeDraftRequest(value any) (domain.MihomoDraft, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return domain.MihomoDraft{}, err
	}
	var input struct {
		ID        string   `json:"id"`
		Mode      string   `json:"mode"`
		MixedPort int      `json:"mixedPort"`
		AllowLAN  bool     `json:"allowLan"`
		Rules     []string `json:"rules"`
	}
	if err := json.Unmarshal(body, &input); err != nil {
		return domain.MihomoDraft{}, err
	}
	return domain.MihomoDraft{ID: input.ID, Mode: input.Mode, MixedPort: input.MixedPort, AllowLAN: input.AllowLAN, Rules: input.Rules}, nil
}

func decodeEgressPlan(value any) (routeros.EgressPlan, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return routeros.EgressPlan{}, err
	}
	var plan routeros.EgressPlan
	if err := json.Unmarshal(body, &plan); err != nil {
		return routeros.EgressPlan{}, err
	}
	if plan.PolicyID == "" || plan.Policy.ID != plan.PolicyID || plan.StateDigest == "" || !plan.RequiresConfirmation || plan.Policy.Validate() != nil {
		return routeros.EgressPlan{}, errors.New("invalid RouterOS egress job plan")
	}
	return plan, nil
}
