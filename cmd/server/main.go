package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/foxc888/foxos/internal/alerting"
	"github.com/foxc888/foxos/internal/api"
	"github.com/foxc888/foxos/internal/backup"
	"github.com/foxc888/foxos/internal/config"
	"github.com/foxc888/foxos/internal/confirmation"
	"github.com/foxc888/foxos/internal/domain"
	"github.com/foxc888/foxos/internal/gateway"
	"github.com/foxc888/foxos/internal/mihomo"
	"github.com/foxc888/foxos/internal/mosdns"
	"github.com/foxc888/foxos/internal/routeros"
	"github.com/foxc888/foxos/internal/store/sqlite"
	"github.com/foxc888/foxos/internal/subscription"
	"github.com/foxc888/foxos/internal/task"
	"github.com/foxc888/foxos/internal/upgrade"
)

var version = "dev"

const defaultHTTPListen = "127.0.0.1:8090"

func main() {
	address := flag.String("listen", defaultHTTPListen, "HTTP listen address")
	staticDir := flag.String("static", "web/dist", "built frontend directory")
	databasePath := flag.String("database", "data/foxos.db", "SQLite database path")
	flag.Parse()
	processContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runtimeConfig, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	if err := validateDatabasePath(*databasePath, runtimeConfig.Production); err != nil {
		log.Fatal(err)
	}
	if err := validateHTTPListen(*address, runtimeConfig.HTTPS.Enabled, os.Getenv("FOXOS_ENV") != ""); err != nil {
		log.Fatal(err)
	}
	signer, err := confirmation.New([]byte(runtimeConfig.ConfirmationKey))
	if err != nil {
		log.Fatal(err)
	}
	subscriptionIdentityHasher, err := subscription.NewIdentityHasher([]byte(runtimeConfig.ConfirmationKey))
	if err != nil {
		log.Fatal(err)
	}
	recovery, err := upgrade.RecoverDatabase(*databasePath, runtimeConfig.UpgradeStatePath, filepath.Join(runtimeConfig.BackupDir, "upgrade"), version, sqlite.CurrentSchemaVersion())
	if err != nil {
		log.Fatal(err)
	}
	store, err := sqlite.Open(*databasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	if err := upgrade.ReconcileAudits(context.Background(), store, recovery.Checkpoint); err != nil {
		log.Fatal(err)
	}
	jobManager, err := task.NewPaused(processContext, store)
	if err != nil {
		log.Fatal(err)
	}
	defer jobManager.Close()
	mutationGate := upgrade.NewMutationGate()
	upgradeCoordinator := &upgrade.Coordinator{Requests: mutationGate, Tasks: jobManager, Database: store}
	if recovery.Quiesce {
		if _, err := upgradeCoordinator.Quiesce(processContext); err != nil {
			log.Fatal(err)
		}
	}
	app, err := api.New(store, runtimeConfig.APIToken)
	if err != nil {
		log.Fatal(err)
	}
	var httpsAccess api.HTTPSAccess
	var certificates gateway.Certificates
	var proxyToken string
	if runtimeConfig.HTTPS.Enabled {
		certificates, err = gateway.EnsureCertificates(runtimeConfig.HTTPS.CertDir, runtimeConfig.Site.PublicHostname, runtimeConfig.Site.FoxOSAddress, time.Now())
		if err != nil {
			log.Fatal(err)
		}
		proxyToken, err = gateway.NewProxyToken()
		if err != nil {
			log.Fatal(err)
		}
		if err := app.RequireSecureTransport(gateway.InternalProxyHeader, proxyToken); err != nil {
			log.Fatal(err)
		}
		httpsAccess = api.HTTPSAccess{Enabled: true, CAFingerprint: certificates.CAFingerprint, CACertificate: certificates.CACertificatePEM}
	}

	var ros api.RouterOSReader
	var leases api.BindingStateReader
	var l2tp api.L2TPReader
	var bindingExecutor api.BindingExecutor
	var egressPlanner api.EgressPlanner
	var egressExecutor api.EgressExecutor
	var dhcpExpansionExecutor api.DHCPExpansionExecutor
	var containerCommands containerCommandService
	var routerMonitor alerting.RouterReader
	if runtimeConfig.RouterOS.URL != "" {
		client, err := routeros.NewClient(runtimeConfig.RouterOS.URL, runtimeConfig.RouterOS.Username, runtimeConfig.RouterOS.Password, runtimeConfig.Site)
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
		dhcpExpansionExecutor = routeros.DHCPExpansionExecutor{Writer: client}
		containerCommands = client
	}
	var clash api.MihomoReader
	var mihomoService api.MihomoService
	var configuredMihomoService *mihomo.Service
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
		configuredMihomoService = &mihomo.Service{Store: store, Applier: mihomoApplier, BaseConfig: baseConfig, DigestKey: []byte(runtimeConfig.ConfirmationKey), ProtectedAddresses: runtimeConfig.Site.ProtectedAddresses()}
		if recovery.Quiesce {
			if _, found, err := mihomoApplier.Pending(); err != nil || found {
				log.Fatalf("upgrade candidate cannot reconcile a pending Mihomo apply before promotion: found=%t err=%v", found, err)
			}
		} else if err := configuredMihomoService.RecoverPending(processContext); err != nil {
			log.Fatalf("recover pending Mihomo apply: %v", err)
		}
		mihomoService = configuredMihomoService
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

	subscriptionFetcher := subscription.Fetcher{UserAgent: "FoxOS subscription updater/1", AllowedPrivate: runtimeConfig.SubscriptionPrivateCIDRs}
	subscriptionUpdater := subscription.Updater{Sources: store, Nodes: store, Atomic: store, Fetcher: subscriptionFetcher, IdentityHasher: subscriptionIdentityHasher}
	backupService := backup.Service{Database: store, MihomoPath: runtimeConfig.Mihomo.LocalConfigPath, Directory: runtimeConfig.BackupDir, Retention: 20}
	if mihomoApplier != nil {
		backupService.MihomoRestore = func(ctx context.Context, body []byte) error {
			_, err := mihomoApplier.Apply(ctx, body)
			return err
		}
		backupService.MihomoHealthy = clash.Healthy
	}
	upgradeService := &upgrade.Service{Store: store, DatabasePath: *databasePath, StatePath: runtimeConfig.UpgradeStatePath, BackupDir: filepath.Join(runtimeConfig.BackupDir, "upgrade"), Version: version, Quiescer: upgradeCoordinator}
	if err := registerMihomoJobs(jobManager, mihomoService, store); err != nil {
		log.Fatal(err)
	}
	if err := registerEgressJobs(jobManager, store, egressPlanner, egressExecutor, store); err != nil {
		log.Fatal(err)
	}
	if err := registerBackupJobs(jobManager, backupService, store); err != nil {
		log.Fatal(err)
	}
	if err := registerSubscriptionJobs(jobManager, subscriptionUpdater, store); err != nil {
		log.Fatal(err)
	}
	if err := registerContainerCommandJobs(jobManager, containerCommands, store); err != nil {
		log.Fatal(err)
	}
	if err := jobManager.Start(); err != nil {
		log.Fatal(err)
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
	app.RegisterSite(mux, runtimeConfig.Site, httpsAccess)
	app.Register(mux)
	app.RegisterGroups(mux, store)
	app.RegisterDevicePolicies(mux, store)
	app.RegisterAudit(mux, store)
	app.RegisterStatus(mux, ros, clash)
	app.RegisterContainerCommands(mux, containerCommands, jobManager)
	app.RegisterDevices(mux, ros, store, store)
	app.RegisterMosDNS(mux, mosdnsReader)
	app.RegisterL2TP(mux, l2tp)
	app.RegisterBindingPlan(mux, leases, signer, bindingExecutor, confirmation.NewReplayGuard(), store, store)
	app.RegisterDHCPPlanning(mux, leases, signer, store, dhcpExpansionExecutor, store)
	var egressReadiness api.EgressReadinessReader
	if client, ok := ros.(api.EgressReadinessReader); ok {
		egressReadiness = client
	}
	app.RegisterEgressCapabilities(mux, egressReadiness)
	app.RegisterEgress(mux, store, egressPlanner, signer, store, egressExecutor, jobManager, store)
	app.RegisterMihomo(mux, mihomoService, store, signer, store, jobManager, store)
	app.RegisterMihomoProbes(mux, mihomoNodeProbe, mihomoExitProbe)
	app.RegisterJobs(mux, jobManager)
	app.RegisterSubscriptions(mux, store, store, subscriptionFetcher, signer, subscriptionIdentityHasher, store, jobManager, store)
	app.RegisterAlerts(mux, store)
	app.RegisterBackups(mux, backupService, signer, store, jobManager, store)
	app.RegisterUpgrade(mux, upgradeService)
	alertMonitor, err := alerting.Start(processContext, alerting.Evaluator{Store: store, RouterOS: routerMonitor, Mihomo: mihomoMonitor, MosDNS: mosdnsReader}, time.Minute, func(err error) {
		log.Printf("alert evaluation incomplete: %v", err)
	})
	if err != nil {
		log.Fatal(err)
	}
	defer alertMonitor.Close()
	subscriptionScheduler, err := subscription.NewScheduler(processContext, store, jobManager, time.Minute)
	if err != nil {
		log.Fatal(err)
	}
	defer subscriptionScheduler.Close()
	if info, err := os.Stat(*staticDir); err == nil && info.IsDir() {
		mux.Handle("/", http.FileServer(http.Dir(*staticDir)))
	}

	handler := mutationGate.Middleware(securityHeaders(mux))
	if runtimeConfig.HTTPS.Enabled {
		log.Printf("FoxOS %s listening on https://%s and redirecting HTTP", version, runtimeConfig.Site.PublicHostname)
		if err := gateway.Serve(processContext, gateway.ServerConfig{
			InternalListen:     runtimeConfig.HTTPS.InternalListen,
			PublicListen:       runtimeConfig.HTTPS.PublicListen,
			HTTPRedirectListen: runtimeConfig.HTTPS.HTTPRedirectListen,
			PublicHostname:     runtimeConfig.Site.PublicHostname,
			PublicAddress:      runtimeConfig.Site.FoxOSAddress,
			CertificatePath:    certificates.CertificatePath,
			KeyPath:            certificates.KeyPath,
			ProxyToken:         proxyToken,
			Backend:            handler,
		}); err != nil {
			log.Fatal(err)
		}
		return
	}
	server := &http.Server{Addr: *address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	log.Printf("FoxOS %s listening on %s", version, *address)
	if err := serveHTTP(processContext, server); err != nil {
		log.Fatal(err)
	}
}

func validateDatabasePath(path string, production bool) error {
	if strings.TrimSpace(path) != path || path == "" || strings.ContainsRune(path, '\x00') {
		return errors.New("database path is invalid")
	}
	if !production {
		return nil
	}
	if path == ":memory:" || !filepath.IsAbs(path) {
		return errors.New("production database path must be an absolute persistent file path")
	}
	parent := filepath.Dir(filepath.Clean(path))
	info, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("inspect production database directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("production database directory must be a real directory")
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("production database path must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect production database path: %w", err)
	}
	return nil
}

func validateHTTPListen(address string, httpsEnabled, environmentExplicit bool) error {
	if httpsEnabled {
		return nil
	}
	if strings.TrimSpace(address) != address || address == "" {
		return errors.New("HTTP listen address is invalid")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return errors.New("HTTP listen address is invalid")
	}
	if environmentExplicit {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("FOXOS_ENV must be explicit before HTTP may listen beyond loopback")
	}
	return nil
}

func serveHTTP(ctx context.Context, server *http.Server) error {
	if ctx == nil || server == nil {
		return errors.New("HTTP server and context are required")
	}
	errorsChannel := make(chan error, 1)
	go func() { errorsChannel <- server.ListenAndServe() }()
	select {
	case err := <-errorsChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return err
		}
		err := <-errorsChannel
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
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

func registerMihomoJobs(manager *task.Manager, service api.MihomoService, audit api.AuditStore) error {
	if manager == nil {
		return errors.New("job manager is unavailable")
	}
	if service == nil {
		return manager.RequireNoRecoverableJobs("mihomo.apply", "mihomo.restore")
	}
	if err := manager.RegisterWithRecovery("mihomo.apply", func(ctx context.Context, job domain.Job, progress task.Progress) (map[string]any, domain.JobStatus, error) {
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
		if err := task.Checkpoint(ctx, "validated", map[string]any{"digest": preview.Digest}); err != nil {
			return failMihomoJobAudit(ctx, audit, event, "mihomo_checkpoint_failed", mihomo.ApplyResult{}, err)
		}
		if audit == nil {
			return map[string]any{"errorClass": "mihomo_audit_unavailable"}, domain.JobFailed, errors.New("Mihomo audit store is unavailable")
		}
		if err := audit.SaveAudit(ctx, event); err != nil {
			return map[string]any{"errorClass": "mihomo_audit_failed"}, domain.JobFailed, errors.New("Mihomo audit start failed")
		}
		progress(domain.JobVerifying, 35)
		if err := task.Checkpoint(ctx, "applying", map[string]any{"digest": preview.Digest}); err != nil {
			return failMihomoJobAudit(ctx, audit, event, "mihomo_checkpoint_failed", mihomo.ApplyResult{}, err)
		}
		label, _ := job.Request["label"].(string)
		result, snapshot, err := service.ApplyPreview(mihomo.WithApplyOperation(ctx, job.Kind, job.ID), draft, digest, label)
		if err != nil {
			status, errorClass := mihomoFailure("mihomo_apply", result, err)
			event.Outcome = domain.AuditFailed
			event.Details["rolledBack"] = result.RolledBack
			event.Details["errorClass"] = errorClass
			_ = audit.SaveAudit(ctx, event)
			return map[string]any{"rolledBack": result.RolledBack, "errorClass": errorClass}, status, err
		}
		if err := task.Checkpoint(ctx, "external_and_state_applied", map[string]any{"snapshotId": snapshot.ID}); err != nil {
			return failMihomoJobAudit(ctx, audit, event, "mihomo_checkpoint_failed", result, err)
		}
		progress(domain.JobVerifying, 95)
		event.Outcome = domain.AuditSucceeded
		event.Details["snapshotId"] = snapshot.ID
		event.Details["rolledBack"] = result.RolledBack
		if err := audit.SaveAudit(ctx, event); err != nil {
			return map[string]any{"snapshotId": snapshot.ID, "rolledBack": result.RolledBack, "errorClass": "mihomo_audit_finalize_failed"}, domain.JobFailed, errors.New("Mihomo audit finalization failed")
		}
		return map[string]any{"snapshotId": snapshot.ID, "rolledBack": result.RolledBack}, domain.JobSucceeded, nil
	}, recoverMihomoApply(service)); err != nil {
		return fmt.Errorf("register Mihomo apply jobs: %w", err)
	}
	if err := manager.RegisterWithRecovery("mihomo.restore", func(ctx context.Context, job domain.Job, progress task.Progress) (map[string]any, domain.JobStatus, error) {
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
		if err := task.Checkpoint(ctx, "restoring", map[string]any{"snapshotId": id}); err != nil {
			return failMihomoJobAudit(ctx, audit, event, "mihomo_restore_checkpoint_failed", mihomo.ApplyResult{}, err)
		}
		label, _ := job.Request["label"].(string)
		result, snapshot, err := service.Restore(mihomo.WithApplyOperation(ctx, job.Kind, job.ID), id, label)
		if err != nil {
			status, errorClass := mihomoFailure("mihomo_restore", result, err)
			event.Outcome = domain.AuditFailed
			event.Details["rolledBack"] = result.RolledBack
			event.Details["errorClass"] = errorClass
			_ = audit.SaveAudit(ctx, event)
			return map[string]any{"rolledBack": result.RolledBack, "errorClass": errorClass}, status, err
		}
		if err := task.Checkpoint(ctx, "external_and_state_restored", map[string]any{"snapshotId": snapshot.ID}); err != nil {
			return failMihomoJobAudit(ctx, audit, event, "mihomo_restore_checkpoint_failed", result, err)
		}
		event.Outcome = domain.AuditSucceeded
		event.Details["restoredSnapshotId"] = snapshot.ID
		event.Details["rolledBack"] = result.RolledBack
		if err := audit.SaveAudit(ctx, event); err != nil {
			return map[string]any{"snapshotId": snapshot.ID, "rolledBack": result.RolledBack, "errorClass": "mihomo_restore_audit_finalize_failed"}, domain.JobFailed, errors.New("Mihomo audit finalization failed")
		}
		return map[string]any{"snapshotId": snapshot.ID, "rolledBack": result.RolledBack}, domain.JobSucceeded, nil
	}, recoverMihomoRestore(service)); err != nil {
		return fmt.Errorf("register Mihomo restore jobs: %w", err)
	}
	return nil
}

type mihomoRecoveryService interface {
	ReconcileApplied(context.Context, string, string) (domain.MihomoSnapshot, bool, error)
	ReconcileRestore(context.Context, string) (domain.MihomoSnapshot, bool, error)
}

func recoverMihomoApply(service api.MihomoService) task.Recoverer {
	return func(ctx context.Context, job domain.Job) (task.RecoveryDecision, error) {
		draft, err := decodeDraftRequest(job.Request["draft"])
		if err != nil {
			return task.RecoveryDecision{Status: domain.JobFailed, ErrorClass: "mihomo_recovery_request_invalid", ErrorMessage: "operation recovery failed", Result: map[string]any{"phase": "recovery_request_invalid"}}, nil
		}
		expected, _ := job.Request["digest"].(string)
		expected = strings.ToLower(strings.TrimSpace(expected))
		label, _ := job.Request["label"].(string)
		if reconciler, ok := service.(mihomoRecoveryService); ok {
			snapshot, applied, reconcileErr := reconciler.ReconcileApplied(ctx, expected, label)
			if reconcileErr != nil {
				return task.RecoveryDecision{}, reconcileErr
			}
			if applied {
				return task.RecoveryDecision{Status: domain.JobSucceeded, Result: map[string]any{"phase": "readback_succeeded", "snapshotId": snapshot.ID, "readback": true}}, nil
			}
		}
		preview, err := service.Preview(ctx, draft)
		if err != nil {
			return task.RecoveryDecision{}, err
		}
		if expected == "" || !strings.EqualFold(expected, preview.Digest) {
			return task.RecoveryDecision{Status: domain.JobFailed, ErrorClass: "mihomo_apply_stale", ErrorMessage: "operation recovery failed", Result: map[string]any{"phase": "recovery_stale"}}, nil
		}
		return task.RecoveryDecision{Status: domain.JobQueued, Result: map[string]any{"phase": "readback_not_applied", "readback": true}}, nil
	}
}

func recoverMihomoRestore(service api.MihomoService) task.Recoverer {
	return func(ctx context.Context, job domain.Job) (task.RecoveryDecision, error) {
		id, _ := job.Request["snapshotId"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			return task.RecoveryDecision{Status: domain.JobFailed, ErrorClass: "mihomo_restore_request_invalid", ErrorMessage: "operation recovery failed", Result: map[string]any{"phase": "recovery_request_invalid"}}, nil
		}
		if reconciler, ok := service.(mihomoRecoveryService); ok {
			snapshot, applied, err := reconciler.ReconcileRestore(ctx, id)
			if err != nil {
				return task.RecoveryDecision{}, err
			}
			if applied {
				return task.RecoveryDecision{Status: domain.JobSucceeded, Result: map[string]any{"phase": "readback_succeeded", "snapshotId": snapshot.ID, "readback": true}}, nil
			}
		}
		return task.RecoveryDecision{Status: domain.JobQueued, Result: map[string]any{"phase": "readback_not_applied", "readback": true}}, nil
	}
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

func registerEgressJobs(manager *task.Manager, policies api.DevicePolicyStore, planner api.EgressPlanner, executor api.EgressExecutor, audit api.AuditStore) error {
	if manager == nil {
		return errors.New("job manager is unavailable")
	}
	if policies == nil || planner == nil || executor == nil {
		return manager.RequireNoRecoverableJobs("routeros.egress")
	}
	if err := manager.RegisterWithRecovery("routeros.egress", func(ctx context.Context, job domain.Job, progress task.Progress) (map[string]any, domain.JobStatus, error) {
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
		changes := make([]map[string]string, 0, routeros.MaxEgressOperations+1)
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
		if err == nil {
			err = task.Checkpoint(ctx, "preconditions_verified", map[string]any{"policyId": plan.PolicyID, "stateDigest": plan.StateDigest})
		}
		if err == nil && len(plan.Operations) > 0 {
			progress(domain.JobVerifying, 35)
			err = executor.Execute(ctx, plan)
		}
		if err == nil {
			var readback routeros.EgressPlan
			readback, err = planner.PlanDeviceEgress(ctx, plan.Policy)
			if err == nil && len(readback.Operations) != 0 {
				err = routeros.ErrWriteVerification
			}
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
		if err := task.Checkpoint(ctx, "external_applied", map[string]any{"policyId": plan.PolicyID}); err != nil {
			event.Outcome = domain.AuditFailed
			event.Details["errorClass"] = "egress_checkpoint_failed"
			_ = audit.SaveAudit(ctx, event)
			return map[string]any{"auditId": auditID, "errorClass": "egress_checkpoint_failed"}, domain.JobFailed, err
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
		if err := task.Checkpoint(ctx, "policy_persisted", map[string]any{"policyId": plan.PolicyID}); err != nil {
			event.Outcome = domain.AuditFailed
			event.Details["errorClass"] = "egress_checkpoint_failed"
			_ = audit.SaveAudit(ctx, event)
			return map[string]any{"policyId": plan.PolicyID, "auditId": auditID, "errorClass": "egress_checkpoint_failed"}, domain.JobFailed, err
		}
		event.Outcome = domain.AuditSucceeded
		event.Details["rolledBack"] = false
		if err := audit.SaveAudit(ctx, event); err != nil {
			return map[string]any{"policyId": plan.PolicyID, "auditId": auditID, "rolledBack": false, "errorClass": "egress_audit_finalize_failed"}, domain.JobFailed, errors.New("egress audit finalization failed")
		}
		return map[string]any{"policyId": plan.PolicyID, "auditId": auditID, "rolledBack": false}, domain.JobSucceeded, nil
	}, recoverEgress(policies, planner)); err != nil {
		return fmt.Errorf("register RouterOS egress jobs: %w", err)
	}
	return nil
}

func recoverEgress(policies api.DevicePolicyStore, planner api.EgressPlanner) task.Recoverer {
	return func(ctx context.Context, job domain.Job) (task.RecoveryDecision, error) {
		plan, err := decodeEgressPlan(job.Request["plan"])
		if err != nil {
			return task.RecoveryDecision{Status: domain.JobFailed, ErrorClass: "egress_recovery_request_invalid", ErrorMessage: "operation recovery failed", Result: map[string]any{"phase": "recovery_request_invalid"}}, nil
		}
		currentPlan, err := planner.PlanDeviceEgress(ctx, plan.Policy)
		if err != nil {
			return task.RecoveryDecision{}, err
		}
		policyState, err := recoveredPolicyState(ctx, policies, plan)
		if err != nil {
			return task.RecoveryDecision{}, err
		}
		if len(currentPlan.Operations) == 0 {
			switch policyState {
			case "desired":
				return task.RecoveryDecision{Status: domain.JobSucceeded, Result: map[string]any{"phase": "readback_succeeded", "policyId": plan.PolicyID, "readback": true}}, nil
			case "previous":
				if err := policies.SaveDevicePolicy(ctx, plan.Policy); err != nil {
					return task.RecoveryDecision{}, err
				}
				return task.RecoveryDecision{Status: domain.JobSucceeded, Result: map[string]any{"phase": "policy_reconciled", "policyId": plan.PolicyID, "readback": true}}, nil
			default:
				return egressPartialRecovery(plan.PolicyID), nil
			}
		}
		currentPlan = api.FinalizeEgressPlan(currentPlan, plan.PreviousPolicy)
		if policyState == "previous" && routeros.EqualEgressPlans(plan, currentPlan) {
			return task.RecoveryDecision{Status: domain.JobQueued, Result: map[string]any{"phase": "prestate_verified", "policyId": plan.PolicyID, "readback": true}}, nil
		}
		return egressPartialRecovery(plan.PolicyID), nil
	}
}

func recoveredPolicyState(ctx context.Context, policies api.DevicePolicyStore, plan routeros.EgressPlan) (string, error) {
	current, err := policies.DevicePolicy(ctx, plan.PolicyID)
	if err == nil {
		if domain.EqualDevicePolicies(current, plan.Policy) {
			return "desired", nil
		}
		if plan.PreviousPolicy != nil && domain.EqualDevicePolicies(current, *plan.PreviousPolicy) {
			return "previous", nil
		}
		return "other", nil
	}
	if errors.Is(err, domain.ErrNotFound) {
		if plan.PreviousPolicy == nil {
			return "previous", nil
		}
		return "other", nil
	}
	return "", err
}

func egressPartialRecovery(policyID string) task.RecoveryDecision {
	return task.RecoveryDecision{
		Status:       domain.JobFailed,
		ErrorClass:   "egress_recovery_partial_state",
		ErrorMessage: "operation recovery requires manual reconciliation",
		Result:       map[string]any{"phase": "recovery_partial_state", "policyId": policyID, "readback": true},
	}
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

type containerCommandService interface {
	ManagedContainer(context.Context, string, string) (routeros.Container, error)
	StartContainer(context.Context, string, string) error
	StopContainer(context.Context, string, string) error
}

type containerCommandRequest struct {
	ID      string
	Owner   string
	Command string
}

func registerContainerCommandJobs(manager *task.Manager, service containerCommandService, audit api.AuditStore) error {
	if manager == nil {
		return errors.New("job manager is unavailable")
	}
	if service == nil {
		return manager.RequireNoRecoverableJobs("routeros.container-command")
	}
	if err := manager.RegisterWithRecovery("routeros.container-command", func(ctx context.Context, job domain.Job, progress task.Progress) (map[string]any, domain.JobStatus, error) {
		request, err := decodeContainerCommandRequest(job.Request)
		if err != nil {
			return map[string]any{"errorClass": "container_command_request_invalid"}, domain.JobFailed, err
		}
		event := containerCommandAuditEvent(job, request)
		if audit == nil {
			return map[string]any{"errorClass": "container_command_audit_unavailable"}, domain.JobFailed, errors.New("container command audit store is unavailable")
		}
		if err := audit.SaveAudit(ctx, event); err != nil {
			return map[string]any{"errorClass": "container_command_audit_failed"}, domain.JobFailed, errors.New("container command audit start failed")
		}

		current, err := service.ManagedContainer(ctx, request.ID, request.Owner)
		if err != nil {
			return failContainerCommand(ctx, audit, event, "container_command_readback_failed", false, err)
		}
		initialStatus, _ := job.Result["initialStatus"].(string)
		initialStatus = normalizedContainerStatus(initialStatus)
		currentStatus := normalizedContainerStatus(current.Status)
		if initialStatus == "" {
			initialStatus = currentStatus
		}
		if !containerCommandPrecondition(request.Command, initialStatus, currentStatus) {
			return failContainerCommand(ctx, audit, event, "container_command_state_invalid", false, routeros.ErrContainerState)
		}
		if err := task.Checkpoint(ctx, "preconditions_verified", map[string]any{"containerId": request.ID, "owner": request.Owner, "command": request.Command, "initialStatus": initialStatus}); err != nil {
			return failContainerCommand(ctx, audit, event, "container_command_checkpoint_failed", false, err)
		}
		progress(domain.JobRunning, 25)

		rolledBack := false
		switch request.Command {
		case "start":
			err = service.StartContainer(ctx, request.ID, request.Owner)
		case "stop":
			err = service.StopContainer(ctx, request.ID, request.Owner)
		case "restart":
			if currentStatus == "running" {
				err = service.StopContainer(ctx, request.ID, request.Owner)
				if err == nil {
					err = requireContainerStatus(ctx, service, request, "stopped")
				}
				if err == nil {
					err = task.Checkpoint(ctx, "restart_stopped", map[string]any{"containerId": request.ID, "initialStatus": initialStatus})
				}
			}
			if err == nil {
				progress(domain.JobVerifying, 60)
				err = service.StartContainer(ctx, request.ID, request.Owner)
			}
			if err != nil {
				compensationErr := service.StartContainer(ctx, request.ID, request.Owner)
				if compensationErr == nil {
					compensationErr = requireContainerStatus(ctx, service, request, "running")
				}
				if compensationErr != nil {
					return failContainerCommand(ctx, audit, event, "container_restart_rollback_failed", false, errors.Join(err, compensationErr))
				}
				rolledBack = true
			}
		}
		if rolledBack {
			return failContainerCommand(ctx, audit, event, "container_restart_rolled_back", true, errors.New("container restart failed and the original running state was restored"))
		}
		if err != nil {
			return failContainerCommand(ctx, audit, event, "container_command_apply_failed", false, err)
		}

		progress(domain.JobVerifying, 80)
		desiredStatus := "running"
		if request.Command == "stop" {
			desiredStatus = "stopped"
		}
		if err := requireContainerStatus(ctx, service, request, desiredStatus); err != nil {
			if request.Command == "restart" {
				compensationErr := service.StartContainer(ctx, request.ID, request.Owner)
				if compensationErr == nil {
					compensationErr = requireContainerStatus(ctx, service, request, "running")
				}
				if compensationErr == nil {
					return failContainerCommand(ctx, audit, event, "container_restart_rolled_back", true, err)
				}
				return failContainerCommand(ctx, audit, event, "container_restart_rollback_failed", false, errors.Join(err, compensationErr))
			}
			return failContainerCommand(ctx, audit, event, "container_command_verification_failed", false, err)
		}
		if err := task.Checkpoint(ctx, "external_applied", map[string]any{"containerId": request.ID, "command": request.Command, "status": desiredStatus}); err != nil {
			return failContainerCommand(ctx, audit, event, "container_command_checkpoint_failed", false, err)
		}
		event.Outcome = domain.AuditSucceeded
		event.Details["status"] = desiredStatus
		if err := audit.SaveAudit(ctx, event); err != nil {
			return map[string]any{"containerId": request.ID, "errorClass": "container_command_audit_finalize_failed"}, domain.JobFailed, errors.New("container command audit finalization failed")
		}
		return map[string]any{"containerId": request.ID, "owner": request.Owner, "command": request.Command, "status": desiredStatus, "auditId": event.ID}, domain.JobSucceeded, nil
	}, recoverContainerCommand(service, audit)); err != nil {
		return fmt.Errorf("register RouterOS container jobs: %w", err)
	}
	return nil
}

func recoverContainerCommand(service containerCommandService, audit api.AuditStore) task.Recoverer {
	return func(ctx context.Context, job domain.Job) (task.RecoveryDecision, error) {
		request, err := decodeContainerCommandRequest(job.Request)
		if err != nil {
			return containerRecoveryFailure(ctx, job, request, audit, "container_command_recovery_request_invalid"), nil
		}
		current, err := service.ManagedContainer(ctx, request.ID, request.Owner)
		if err != nil {
			return task.RecoveryDecision{}, err
		}
		status := normalizedContainerStatus(current.Status)
		phase, _ := job.Result["phase"].(string)
		initialStatus, _ := job.Result["initialStatus"].(string)
		initialStatus = normalizedContainerStatus(initialStatus)
		desiredStatus := "running"
		if request.Command == "stop" {
			desiredStatus = "stopped"
		}

		completed := status == desiredStatus
		if request.Command == "restart" && completed && phase != "restart_stopped" && phase != "external_applied" {
			completed = false
		}
		if completed {
			if audit != nil {
				event := containerCommandAuditEvent(job, request)
				event.Outcome = domain.AuditSucceeded
				event.Details["status"] = desiredStatus
				event.Details["recovered"] = true
				_ = audit.SaveAudit(ctx, event)
			}
			return task.RecoveryDecision{Status: domain.JobSucceeded, Result: map[string]any{"phase": "readback_succeeded", "containerId": request.ID, "command": request.Command, "status": desiredStatus, "readback": true}}, nil
		}

		canResume := false
		switch request.Command {
		case "start":
			canResume = status == "stopped" && (initialStatus == "" || initialStatus == "stopped")
		case "stop":
			canResume = status == "running" && (initialStatus == "" || initialStatus == "running")
		case "restart":
			canResume = initialStatus == "running" && (status == "running" || status == "stopped")
		}
		if canResume {
			return task.RecoveryDecision{Status: domain.JobQueued, Result: map[string]any{"phase": "resume_verified", "containerId": request.ID, "command": request.Command, "initialStatus": initialStatus, "readback": true}}, nil
		}
		decision := containerRecoveryFailure(ctx, job, request, audit, "container_command_recovery_partial_state")
		decision.Result["status"] = status
		decision.Result["readback"] = true
		return decision, nil
	}
}

func decodeContainerCommandRequest(values map[string]any) (containerCommandRequest, error) {
	request := containerCommandRequest{}
	request.ID, _ = values["containerId"].(string)
	request.Owner, _ = values["owner"].(string)
	request.Command, _ = values["command"].(string)
	request.ID = strings.TrimSpace(request.ID)
	request.Owner = strings.TrimSpace(request.Owner)
	request.Command = strings.ToLower(strings.TrimSpace(request.Command))
	if request.ID == "" || request.Owner == "" || (request.Command != "start" && request.Command != "stop" && request.Command != "restart") {
		return containerCommandRequest{}, errors.New("invalid container command request")
	}
	return request, nil
}

func normalizedContainerStatus(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func containerCommandPrecondition(command, initialStatus, currentStatus string) bool {
	if initialStatus != "running" && initialStatus != "stopped" {
		return false
	}
	switch command {
	case "start", "stop":
		return currentStatus == "running" || currentStatus == "stopped"
	case "restart":
		return initialStatus == "running" && (currentStatus == "running" || currentStatus == "stopped")
	default:
		return false
	}
}

func requireContainerStatus(ctx context.Context, service containerCommandService, request containerCommandRequest, desired string) error {
	container, err := service.ManagedContainer(ctx, request.ID, request.Owner)
	if err != nil {
		return err
	}
	if normalizedContainerStatus(container.Status) != desired {
		return routeros.ErrContainerState
	}
	return nil
}

func containerCommandAuditEvent(job domain.Job, request containerCommandRequest) domain.AuditEvent {
	return domain.AuditEvent{
		ID:       job.ID + "-audit",
		Action:   "routeros.container." + request.Command,
		TargetID: request.ID,
		Outcome:  domain.AuditStarted,
		Details: map[string]any{
			"jobId": job.ID, "owner": request.Owner, "command": request.Command,
			"actor": job.Request["actor"], "source": job.Request["source"],
		},
	}
}

func failContainerCommand(ctx context.Context, audit api.AuditStore, event domain.AuditEvent, errorClass string, rolledBack bool, cause error) (map[string]any, domain.JobStatus, error) {
	event.Outcome = domain.AuditFailed
	event.Details["errorClass"] = errorClass
	event.Details["rolledBack"] = rolledBack
	if audit != nil {
		_ = audit.SaveAudit(ctx, event)
	}
	status := domain.JobFailed
	if rolledBack {
		status = domain.JobRolledBack
	}
	return map[string]any{"containerId": event.TargetID, "auditId": event.ID, "rolledBack": rolledBack, "errorClass": errorClass}, status, cause
}

func containerRecoveryFailure(ctx context.Context, job domain.Job, request containerCommandRequest, audit api.AuditStore, errorClass string) task.RecoveryDecision {
	if audit != nil {
		event := containerCommandAuditEvent(job, request)
		event.Outcome = domain.AuditFailed
		event.Details["errorClass"] = errorClass
		event.Details["recovered"] = true
		_ = audit.SaveAudit(ctx, event)
	}
	return task.RecoveryDecision{Status: domain.JobFailed, ErrorClass: errorClass, ErrorMessage: "operation recovery requires manual reconciliation", Result: map[string]any{"phase": "recovery_partial_state", "containerId": request.ID, "command": request.Command}}
}

func registerBackupJobs(manager *task.Manager, service api.BackupService, audit api.AuditStore) error {
	if manager == nil {
		return errors.New("job manager is unavailable")
	}
	if service == nil {
		return manager.RequireNoRecoverableJobs("backup.create", "backup.restore")
	}
	if err := manager.RegisterWithRecovery("backup.create", func(ctx context.Context, job domain.Job, progress task.Progress) (map[string]any, domain.JobStatus, error) {
		progress(domain.JobRunning, 10)
		label, _ := job.Request["label"].(string)
		if err := task.Checkpoint(ctx, "creating", map[string]any{"operationId": job.ID}); err != nil {
			return map[string]any{"errorClass": "backup_checkpoint_failed"}, domain.JobFailed, err
		}
		recoveryService, ok := service.(backupRecoveryService)
		if !ok {
			return map[string]any{"errorClass": "backup_recovery_unavailable"}, domain.JobFailed, errors.New("backup recovery service is unavailable")
		}
		item, err := recoveryService.CreateOperation(ctx, label, job.ID)
		event := domain.AuditEvent{ID: job.ID + "-audit", Action: "backup.create", TargetID: job.ID, Outcome: domain.AuditSucceeded, Details: map[string]any{"jobId": job.ID, "actor": job.Request["actor"], "source": job.Request["source"]}}
		if err != nil {
			event.Outcome = domain.AuditFailed
			event.Details["errorClass"] = "backup_create_failed"
			if audit != nil {
				_ = audit.SaveAudit(ctx, event)
			}
			return map[string]any{"errorClass": "backup_create_failed"}, domain.JobFailed, err
		}
		if err := task.Checkpoint(ctx, "backup_published", map[string]any{"operationId": job.ID, "backupId": item.ID}); err != nil {
			return map[string]any{"backupId": item.ID, "errorClass": "backup_checkpoint_failed"}, domain.JobFailed, err
		}
		event.TargetID = item.ID
		event.Details["digestCount"] = item.FileCount
		if audit != nil {
			_ = audit.SaveAudit(ctx, event)
		}
		return map[string]any{"manifest": item, "backupId": item.ID, "operationId": job.ID}, domain.JobSucceeded, nil
	}, recoverBackupCreate(service)); err != nil {
		return fmt.Errorf("register backup create jobs: %w", err)
	}
	if err := manager.RegisterWithRecovery("backup.restore", func(ctx context.Context, job domain.Job, progress task.Progress) (map[string]any, domain.JobStatus, error) {
		id, _ := job.Request["backupId"].(string)
		digest, _ := job.Request["digest"].(string)
		auditID, _ := job.Request["auditId"].(string)
		if auditID == "" {
			auditID = job.ID + "-audit"
		}
		event := domain.AuditEvent{ID: auditID, Action: "backup.restore", TargetID: id, Outcome: domain.AuditStarted, Details: map[string]any{"jobId": job.ID, "digest": digest, "actor": job.Request["actor"], "source": job.Request["source"]}}
		progress(domain.JobVerifying, 20)
		if err := task.Checkpoint(ctx, "restore_starting", map[string]any{"operationId": job.ID, "backupId": id, "digest": digest}); err != nil {
			return map[string]any{"backupId": id, "errorClass": "backup_restore_checkpoint_failed"}, domain.JobFailed, err
		}
		recoveryService, ok := service.(backupRecoveryService)
		if !ok {
			return map[string]any{"backupId": id, "errorClass": "backup_recovery_unavailable"}, domain.JobFailed, errors.New("backup recovery service is unavailable")
		}
		err := recoveryService.RestoreOperation(mihomo.WithApplyOperation(ctx, job.Kind, job.ID), id, digest, job.ID)
		if err != nil {
			event.Outcome = domain.AuditFailed
			errorClass := "backup_restore_failed"
			status := domain.JobFailed
			if errors.Is(err, backup.ErrRestoreRolledBack) {
				errorClass = "backup_restore_rolled_back"
				status = domain.JobRolledBack
			} else if errors.Is(err, backup.ErrRestoreRollbackFailed) {
				errorClass = "backup_restore_rollback_failed"
			}
			event.Details["errorClass"] = errorClass
			if audit != nil {
				_ = audit.SaveAudit(ctx, event)
			}
			return map[string]any{"backupId": id, "operationId": job.ID, "errorClass": errorClass, "rolledBack": status == domain.JobRolledBack}, status, err
		}
		if err := task.Checkpoint(ctx, "restore_completed", map[string]any{"operationId": job.ID, "backupId": id, "digest": digest}); err != nil {
			return map[string]any{"backupId": id, "errorClass": "backup_restore_checkpoint_failed"}, domain.JobFailed, err
		}
		event.Outcome = domain.AuditSucceeded
		if audit != nil {
			_ = audit.SaveAudit(ctx, event)
		}
		return map[string]any{"backupId": id, "operationId": job.ID}, domain.JobSucceeded, nil
	}, recoverBackupRestore(service)); err != nil {
		return fmt.Errorf("register backup restore jobs: %w", err)
	}
	return nil
}

type backupRecoveryService interface {
	CreateOperation(context.Context, string, string) (backup.Manifest, error)
	ReconcileCreate(string) (backup.Manifest, bool, error)
	RestoreOperation(context.Context, string, string, string) error
	ReconcileRestore(context.Context, string, string, string) (backup.RestoreRecoveryState, error)
}

func recoverBackupCreate(service api.BackupService) task.Recoverer {
	return func(_ context.Context, job domain.Job) (task.RecoveryDecision, error) {
		recoveryService, ok := service.(backupRecoveryService)
		if !ok {
			return task.RecoveryDecision{}, errors.New("backup recovery service is unavailable")
		}
		manifest, found, err := recoveryService.ReconcileCreate(job.ID)
		if err != nil {
			return task.RecoveryDecision{}, err
		}
		if found {
			return task.RecoveryDecision{Status: domain.JobSucceeded, Result: map[string]any{"phase": "readback_succeeded", "operationId": job.ID, "backupId": manifest.ID, "manifest": manifest, "readback": true}}, nil
		}
		if phase, _ := job.Result["phase"].(string); phase == "creating" {
			return task.RecoveryDecision{Status: domain.JobQueued, Result: map[string]any{"phase": "publication_not_found", "operationId": job.ID, "readback": true}}, nil
		}
		return task.RecoveryDecision{Status: domain.JobFailed, ErrorClass: "backup_create_recovery_incomplete", ErrorMessage: "operation recovery requires manual reconciliation", Result: map[string]any{"phase": "recovery_incomplete", "operationId": job.ID, "readback": true}}, nil
	}
}

func recoverBackupRestore(service api.BackupService) task.Recoverer {
	return func(ctx context.Context, job domain.Job) (task.RecoveryDecision, error) {
		recoveryService, ok := service.(backupRecoveryService)
		if !ok {
			return task.RecoveryDecision{}, errors.New("backup recovery service is unavailable")
		}
		id, _ := job.Request["backupId"].(string)
		digest, _ := job.Request["digest"].(string)
		state, err := recoveryService.ReconcileRestore(ctx, id, digest, job.ID)
		if err != nil {
			return task.RecoveryDecision{}, err
		}
		result := map[string]any{"backupId": id, "operationId": job.ID, "digest": digest, "readback": true}
		switch state {
		case backup.RestoreRecoveryCompleted:
			result["phase"] = "readback_succeeded"
			return task.RecoveryDecision{Status: domain.JobSucceeded, Result: result}, nil
		case backup.RestoreRecoveryPending:
			result["phase"] = "resume_verified"
			return task.RecoveryDecision{Status: domain.JobQueued, Result: result}, nil
		case backup.RestoreRecoveryRolledBack:
			result["phase"] = "rollback_readback_succeeded"
			result["rolledBack"] = true
			return task.RecoveryDecision{Status: domain.JobRolledBack, ErrorClass: "backup_restore_rolled_back", ErrorMessage: "operation was rolled back", Result: result}, nil
		default:
			result["phase"] = "recovery_partial_state"
			return task.RecoveryDecision{Status: domain.JobFailed, ErrorClass: "backup_restore_recovery_partial_state", ErrorMessage: "operation recovery requires manual reconciliation", Result: result}, nil
		}
	}
}

func registerSubscriptionJobs(manager *task.Manager, updater subscription.Updater, audit api.AuditStore) error {
	if manager == nil {
		return errors.New("job manager is unavailable")
	}
	if updater.Sources == nil || updater.Nodes == nil || updater.Fetcher == nil {
		return manager.RequireNoRecoverableJobs("subscription.update")
	}
	if err := manager.RegisterWithRecovery("subscription.update", func(ctx context.Context, job domain.Job, progress task.Progress) (map[string]any, domain.JobStatus, error) {
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
		result, err := updater.ApplyWithCheckpoint(ctx, id, expected, func(phase string, preview subscription.UpdatePreview) error {
			return task.Checkpoint(ctx, phase, map[string]any{
				"subscriptionId":   id,
				"digest":           preview.Plan.Digest,
				"planDigest":       subscription.UpdatePlanDigest(preview.Plan),
				"nodeCount":        preview.Plan.NodeCount,
				"settingsRevision": preview.Plan.SettingsRevision,
			})
		})
		if err != nil {
			event.Outcome = domain.AuditFailed
			event.Details["errorClass"] = "subscription_update_failed"
			if errors.Is(err, subscription.ErrContentChanged) {
				event.Details["errorClass"] = "subscription_content_changed"
			}
			if errors.Is(err, domain.ErrSubscriptionSettingsStale) {
				event.Details["errorClass"] = "subscription_settings_stale"
			}
			failure := map[string]any{"subscriptionId": id, "errorClass": event.Details["errorClass"]}
			if result.Plan.SubscriptionID != "" {
				state, readbackErr := updater.Readback(ctx, id, result)
				failure["readback"] = readbackErr == nil
				if readbackErr == nil {
					failure["state"] = state
					switch state {
					case subscription.RecoveryCompleted:
						event.Outcome = domain.AuditSucceeded
						event.Details["recoveredByReadback"] = true
						failure["stateCommitted"] = true
						if audit != nil {
							_ = audit.SaveAudit(ctx, event)
						}
						return failure, domain.JobSucceeded, nil
					case subscription.RecoveryPreState:
						failure["oldNodesRetained"] = true
					case subscription.RecoveryPartial:
						failure["manualReconciliationRequired"] = true
					}
				} else {
					failure["readbackError"] = "subscription state readback failed"
				}
			}
			if audit != nil {
				_ = audit.SaveAudit(ctx, event)
			}
			return failure, domain.JobFailed, err
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
		return map[string]any{"subscriptionId": id, "digest": result.Plan.Digest, "planDigest": subscription.UpdatePlanDigest(result.Plan), "settingsRevision": result.Plan.SettingsRevision, "nodeCount": result.Plan.NodeCount, "oldNodesRetained": false}, domain.JobSucceeded, nil
	}, recoverSubscriptionUpdate(updater)); err != nil {
		return fmt.Errorf("register subscription update jobs: %w", err)
	}
	return nil
}

func recoverSubscriptionUpdate(updater subscription.Updater) task.Recoverer {
	return func(ctx context.Context, job domain.Job) (task.RecoveryDecision, error) {
		id, _ := job.Request["subscriptionId"].(string)
		id = strings.TrimSpace(id)
		expectedDigest, _ := job.Result["digest"].(string)
		expectedPlanDigest, _ := job.Result["planDigest"].(string)
		expectedSettingsRevision := positiveInt64(job.Result["settingsRevision"])
		if value, found := job.Request["plan"]; (expectedDigest == "" || expectedPlanDigest == "" || expectedSettingsRevision < 1) && found && value != nil {
			body, err := json.Marshal(value)
			if err == nil {
				var plan subscription.UpdatePlan
				if json.Unmarshal(body, &plan) == nil {
					expectedDigest = plan.Digest
					expectedPlanDigest = subscription.UpdatePlanDigest(plan)
					expectedSettingsRevision = plan.SettingsRevision
				}
			}
		}
		if id == "" || expectedDigest == "" || expectedPlanDigest == "" || expectedSettingsRevision < 1 {
			return task.RecoveryDecision{Status: domain.JobFailed, ErrorClass: "subscription_recovery_checkpoint_missing", ErrorMessage: "operation recovery failed", Result: map[string]any{"phase": "recovery_checkpoint_missing"}}, nil
		}
		state, preview, err := updater.Reconcile(ctx, id, expectedDigest, expectedPlanDigest, expectedSettingsRevision)
		if err != nil {
			return task.RecoveryDecision{}, err
		}
		result := map[string]any{"subscriptionId": id, "digest": expectedDigest, "planDigest": expectedPlanDigest, "settingsRevision": expectedSettingsRevision, "nodeCount": preview.Plan.NodeCount, "readback": true}
		switch state {
		case subscription.RecoveryCompleted:
			result["phase"] = "readback_succeeded"
			return task.RecoveryDecision{Status: domain.JobSucceeded, Result: result}, nil
		case subscription.RecoveryPreState:
			result["phase"] = "prestate_verified"
			return task.RecoveryDecision{Status: domain.JobQueued, Result: result}, nil
		default:
			result["phase"] = "recovery_partial_state"
			return task.RecoveryDecision{Status: domain.JobFailed, ErrorClass: "subscription_recovery_partial_state", ErrorMessage: "operation recovery requires manual reconciliation", Result: result}, nil
		}
	}
}

func positiveInt64(value any) int64 {
	switch typed := value.(type) {
	case int:
		if typed > 0 {
			return int64(typed)
		}
	case int64:
		if typed > 0 {
			return typed
		}
	case float64:
		converted := int64(typed)
		if converted > 0 && typed == float64(converted) {
			return converted
		}
	case json.Number:
		converted, err := typed.Int64()
		if err == nil && converted > 0 {
			return converted
		}
	}
	return 0
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
	if plan.PolicyID == "" || plan.Policy.ID != plan.PolicyID || plan.StateDigest == "" || !plan.RequiresConfirmation || len(plan.Operations) > routeros.MaxEgressOperations || plan.Policy.Validate() != nil {
		return routeros.EgressPlan{}, errors.New("invalid RouterOS egress job plan")
	}
	return plan, nil
}
