package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hibiken/asynq"
	"openloc/internal/ai"
	"openloc/internal/analysis"
	"openloc/internal/api"
	"openloc/internal/chat"
	"openloc/internal/config"
	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/demoorg"
	"openloc/internal/errtrack"
	"openloc/internal/extension"
	"openloc/internal/file"
	"openloc/internal/hold"
	"openloc/internal/issues"
	"openloc/internal/llmdebug"
	"openloc/internal/logging"
	notif "openloc/internal/notification"
	"openloc/internal/parsers/common"
	"openloc/internal/pglisten"
	"openloc/internal/pipeline"
	"openloc/internal/productdocs"
	"openloc/internal/projectdelete"
	"openloc/internal/publicapi"
	"openloc/internal/secrets"
	"openloc/internal/tenantdelete"
	"openloc/internal/translationmemory"
	"openloc/internal/worker"
	"openloc/internal/workflow"
)

// fatal reports an unrecoverable STARTUP failure and exits non-zero.
//
// Use it instead of log.Fatalf, which exits immediately: notice delivery is
// async, so without an explicit flush the report is dropped on the way out. A
// pod that crash-loops without reporting why is the worst case to debug.
//
// Shutdown failures deliberately do NOT come through here — see the end of
// main(). They are not startup failures and must not dilute this alert.
func fatal(ctx context.Context, err error, msg, action string) {
	logging.Error(ctx, err, msg, action)
	errtrack.Capture(ctx, err, errtrack.CaptureOptions{
		Class:     errtrack.ClassStartupFailure,
		Message:   msg,
		Subsystem: "startup",
		Event:     action,
	})
	errtrack.Flush()
	os.Exit(1)
}

func main() {
	// `server <command>` (migrate, create-org) runs one task and exits.
	if handled, code := runCommand(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); handled {
		os.Exit(code)
	}

	cfg := config.Load()

	// Observability first, so every later failure — including the ones that
	// abort startup below — is structured and reported.
	logging.Init(logging.Options{
		ECS:         logging.ECSEnabled(cfg.ECSLogging),
		Version:     cfg.Revision,
		Environment: cfg.Environment,
	})
	// The reports are flushed by fatal on a failed start, and by the shutdown
	// sequence, within its budget, on the way out.
	flushNotices := errtrack.Init(errtrack.Options{
		APIKey:      cfg.HoneybadgerAPIKey,
		Environment: cfg.Environment,
		Revision:    cfg.Revision,
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := cfg.Validate(); err != nil {
		fatal(ctx, err, "invalid configuration", "startup-config-invalid")
	}

	// One honest line on which piece is missing when reporting is off.
	switch {
	case cfg.HoneybadgerAPIKey != "":
		logging.Info(ctx, "Honeybadger error reporting enabled", "startup-errtrack-on")
	case !cfg.HoneybadgerEnabled:
		logging.Info(ctx, "Honeybadger error reporting disabled (HONEYBADGER_ENABLED is not true)", "startup-errtrack-off")
	default:
		logging.Info(ctx, "Honeybadger error reporting disabled (HONEYBADGER_API_KEY is not set)", "startup-errtrack-off")
	}

	pool, err := database.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fatal(ctx, err, "failed to connect to database", "startup-db-connect")
	}
	logging.Info(ctx, "connected to database", "startup-db-connected")

	// Verify DB schema before starting — refuse to serve traffic with missing tables.
	// This prevents the silent failure where migrations are recorded but didn't apply.
	if err := database.VerifySchema(ctx, pool); err != nil {
		fatal(ctx, err, "schema verification failed", "startup-schema-invalid")
	}
	// A binary deployed before its migrations ran would pass the table check
	// and fail on the first query that names a new column.
	if err := database.VerifyMigrated(ctx, pool); err != nil {
		fatal(ctx, err, "database is behind this binary", "startup-migrations-pending")
	}
	logging.Info(ctx, "schema verified — all required tables present, no pending migrations", "startup-schema-ok")

	queries := db.New(pool)

	storage := file.NewStorage(cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket)
	if err := storage.EnsureBucket(ctx); err != nil {
		log.Printf("Warning: could not ensure S3 bucket: %v", err)
	}

	common.SidecarURL = cfg.ParserSidecarURL
	log.Printf("Parser sidecar URL: %s", cfg.ParserSidecarURL)

	// --- Async worker (asynq) ---
	redisOpt := asynq.RedisClientOpt{Addr: cfg.RedisAddr}

	asynqClient := asynq.NewClient(redisOpt)
	defer asynqClient.Close()

	enqueuer := worker.NewEnqueuer(asynqClient, queries, cfg.AsynqQueue)

	// Dev-only LLM/extension request-response recorder (DEV_AUTO_LOGIN gate —
	// same as dev-login). Kept as a concrete *Ring; only assigned into
	// interface fields when non-nil. Feeds GET /api/debug/llm/calls.
	var llmDebugRing *llmdebug.Ring
	if cfg.DevAutoLogin {
		llmDebugRing = llmdebug.NewRing(200)
	}

	// Persisted LLM debug log — prod-only, gated on its own
	// env rather than DevAutoLogin. Deliberately NOT wired into the HTTP route:
	// Entry has no tenant_id, so /api/debug/llm/calls stays keyed on the dev
	// ring and stays 404 here. Both replicas write to the one shared bucket;
	// the pod name in each object key keeps them from overwriting each other.
	var llmDebugStore *llmdebug.Store
	var llmDebugPruner *llmdebug.Pruner
	var llmDebugReader llmdebug.Source
	if cfg.LLMDebugPersist {
		llmDebugStore = llmdebug.NewStore(storage, llmdebug.StoreOpts{
			FlushInterval: time.Duration(cfg.LLMDebugFlushSeconds) * time.Second,
		})
		// Reads those objects back for /debug/llm. Platform-owner gated in
		// api.go; nothing tenant-scoped can reach it.
		llmDebugReader = llmdebug.NewReader(storage, llmdebug.DefaultKeyPrefix)
		llmDebugPruner = llmdebug.NewPruner(storage, llmdebug.PrunerOpts{
			RetainDays: cfg.LLMDebugRetainDays,
		})
		log.Printf("LLM debug log persistence ON (prefix %s/, flush every %ds, retain %d day(s) before today)",
			llmdebug.DefaultKeyPrefix, cfg.LLMDebugFlushSeconds, cfg.LLMDebugRetainDays)
	}

	// One Recorder for the capture sites. Multi handles the nil cases (a typed
	// nil in an interface is non-nil and would defeat the recorder==nil fast
	// path) and, when both are configured, records to both rather than silently
	// leaving the dev viewer empty.
	llmDebugRecorder := llmdebug.Multi(llmDebugRing, llmDebugStore)

	// Pipeline service used by both the worker and synchronous handlers
	pipelineSvc := pipeline.NewService(queries, pipeline.NewPgxTxRunner(pool), storage)
	// AI-as-MT: the worker runs Apply MT jobs, so its pipeline needs
	// the LLM translator + secret decryptor to resolve AI engines of type 'mt'.
	//
	// ConfigureAI is shared with the HTTP router (internal/api), so both
	// pipelines record to the same llmdebug recorder. The
	// log line says whether capture is wired on the worker, which runs QE+APE.
	log.Printf("[worker] pipeline llm debug capture: %v", llmDebugRecorder != nil)
	if enc, err := pipelineSvc.ConfigureAI(cfg.AISecretsKey, llmDebugRecorder); err != nil {
		log.Printf("AI-as-MT disabled: %v", err)
	} else {
		// The worker runs QE+APE, which calls extensions.
		pipelineSvc.SetExtensions(extension.NewService(queries, storage, enc, extension.NewRuntime(nil, llmDebugRecorder)))
	}

	// Consume ONLY this deployment's queue. Producer and consumer both go
	// through worker.QueueOrDefault so they cannot disagree — publishing to a
	// queue nobody consumes would be a silent backlog.
	taskQueue := worker.QueueOrDefault(cfg.AsynqQueue)
	// Two pools: pipeline and QE+APE tasks on their own queue and workers,
	// everything else on the deployment's queue (+ "default" at low priority,
	// so tasks from un-namespaced producers are not orphaned at "queued").
	var asynqServers []*asynq.Server
	for _, spec := range worker.ServerSpecs(taskQueue, cfg.WorkerConcurrency, cfg.WorkerLongConcurrency) {
		logging.Info(ctx, "asynq pool configured", "startup-asynq-pool",
			logging.Label("pool", spec.Name), logging.Label("queues", fmt.Sprint(spec.Queues)), logging.Count(spec.Concurrency))
		asynqServers = append(asynqServers, asynq.NewServer(redisOpt, asynq.Config{
			Queues:          spec.Queues,
			Concurrency:     spec.Concurrency,
			LogLevel:        asynq.WarnLevel,
			ShutdownTimeout: asynqShutdownTimeout,
			RetryDelayFunc: func(n int, e error, t *asynq.Task) time.Duration {
				return time.Duration(1<<uint(n)) * time.Minute // 1m, 2m, 4m
			},
			// Report terminal task failures. Transient retries are logged
			// only — see internal/worker/observability.go for why.
			ErrorHandler: worker.ErrorHandler(queries),
		}))
	}
	// Notification service — created BEFORE mux setup because the
	// notification.ScheduledHandler also registers on the same mux.
	//
	// Email via SMTP or Gmail when configured; otherwise a console sender logs
	// emails (dev). An init failure logs and falls back to console rather than
	// stopping the server.
	emailSender, emailDesc, err := notif.NewSender(ctx, notif.EmailConfig{
		From:            cfg.NotificationEmail,
		SMTPHost:        cfg.SMTPHost,
		SMTPPort:        cfg.SMTPPort,
		SMTPUsername:    cfg.SMTPUsername,
		SMTPPassword:    cfg.SMTPPassword,
		SMTPTLS:         cfg.SMTPTLS,
		GmailClientID:   cfg.NotificationServiceClientID,
		GmailPrivateKey: cfg.NotificationServicePrivateKey,
	})
	if err != nil {
		logging.Error(ctx, err, "📧 Email sender init failed — using console sender, no email is sent", "startup-email-sender-failed")
		emailSender = &notif.ConsoleSender{}
	} else {
		logging.Info(ctx, "📧 "+emailDesc, "startup-email-sender")
	}
	// Dev only: record sent emails so E2E tests can follow invite and reset
	// links (GET /api/dev/outbox). The emails hold live tokens, so the outbox
	// shares the DEV_AUTO_LOGIN gate.
	var devOutbox *notif.Outbox
	if cfg.DevAutoLogin {
		devOutbox = notif.NewOutbox(200)
		emailSender = &notif.RecordingSender{Next: emailSender, Outbox: devOutbox}
	}
	// The broker lets Service.Send publish to live SSE subscribers on every
	// replica, in addition to writing the in-app row: notices travel by
	// Postgres NOTIFY, and the process's one listen connection (pgListener,
	// started below) receives them. The listener stops with the signal; the
	// shutdown closes the broker after the workers and handlers have
	// drained, so what they published still goes out.
	notifBroker := notif.NewPGBroker(pool)
	pgListener := pglisten.New()
	notifBroker.ListenOn(pgListener)
	notifService := notif.NewServiceWithBroker(queries, emailSender, notifBroker, cfg.BaseURL)

	// Saved analyses worker. Attached to
	// the asynq worker handler so TaskAnalysis tasks (enqueued by the POST
	// handler) get computed in-process. Stateless service; one instance.
	analysisSvc := analysis.NewService(queries, pipeline.NewPgxTxRunner(pool))
	// WithAIStepEnqueuer: let a finished pre-translation pipeline chain
	// the QE+APE /predict run, instead of racing MT with a parallel upload-time
	// enqueue.
	// WithWorkflow: an `ai` step completes itself after its
	// QE+APE run and hands the job to the next step.
	workerHandler := worker.NewHandler(pipelineSvc, queries).
		WithAnalysisService(analysisSvc).
		WithAIStepEnqueuer(enqueuer).
		WithAnalysisEnqueuer(enqueuer).
		WithFailureNotifier(notifService).
		WithWorkflow(workflow.NewService(queries, pipeline.NewPgxTxRunner(pool), workflow.AsyncNotifier(notifService.Send)).WithAIStep(enqueuer))
	scheduledHandler := notif.NewScheduledHandler(queries, notifService)
	mux := asynq.NewServeMux()
	workerHandler.RegisterHandlers(mux)
	// Notification-scheduled tasks: hourly deadline scan.
	// Producer is the asynq.Scheduler started below (env-gated).
	scheduledHandler.RegisterHandlers(mux)
	// Daily retention sweep for the persisted LLM debug log. Only registered
	// when persistence is on; the producer below is scheduler-gated.
	if llmDebugPruner != nil {
		llmDebugPruner.RegisterHandlers(mux)
	}
	// The shared demo organization: built or refreshed now,
	// in the background so a slow build never holds up serving, and daily from
	// the scheduler below. A failure is logged and waits for the next run.
	var demoRefresher *demoorg.Refresher
	if cfg.DemoOrg {
		demoRefresher = demoorg.NewRefresher(pool)
		demoRefresher.RegisterHandlers(mux)
		go func() {
			defer errtrack.Monitor(ctx, "startup", "demo-org-refresh")
			if err := demoRefresher.Refresh(ctx); err != nil {
				return // Refresh logged it
			}
		}()
	}

	// Hourly purge of expired assistant uploads and of the blobs of deleted
	// upload rows.
	uploadPurger := chat.NewUploadPurger(queries, storage)
	uploadPurger.RegisterHandlers(mux)

	// Hourly purge of the files of deleted issue attachments.
	issues.NewAttachmentPurger(queries, storage).RegisterHandlers(mux)
	// The operator emails about new issues and replies, queued by the API.
	issues.NewOperatorMailer(queries, emailSender, cfg.BaseURL).RegisterHandlers(mux)

	// Sweep of expired segment holds.
	hold.NewSweeper(queries).RegisterHandlers(mux)
	// Sweep of TMX imports whose process died.
	translationmemory.NewImportSweeper(queries).RegisterHandlers(mux)
	// Background deletes of closed organizations, and their sweep.
	tenantdelete.New(pool, enqueuer).RegisterHandlers(mux)
	// Background purges of deleted projects, and their sweep.
	projectdelete.New(pool, enqueuer).RegisterHandlers(mux)

	for _, srv := range asynqServers {
		go func() {
			// asynq recovers panics inside task handlers, but not one raised by
			// Start itself. Same gap as every other bare goroutine.
			defer errtrack.Monitor(ctx, "startup", "asynq-server")
			if err := srv.Start(mux); err != nil {
				fatal(ctx, err, "asynq server error", "startup-asynq-failed")
			}
		}()
	}
	logging.Info(ctx, "asynq workers started", "startup-asynq-started", logging.Count(len(asynqServers)))

	// Periodic scheduler — env-gated. In multi-replica prod, set
	// RUN_SCHEDULER=false on all but ONE replica (asynq's Scheduler doesn't
	// dedup periodic enqueues across processes; running it everywhere fans
	// out the hourly task N times, which the SQL dedup window catches but
	// is wasteful + confusing in logs).
	//
	// The scheduler also runs when only the debug-log retention sweep needs it:
	// staging sets RUN_SCHEDULER=false because the DEADLINE SCAN sends real
	// emails, while the 1-day retention window is the data-protection control
	// and must still run there.
	// Retention is not an email concern. The daily demo-organization refresh
	// and the hourly assistant-upload purge need the scheduler for the same
	// reason, so the scheduler always runs; RUN_SCHEDULER gates only the
	// deadline scan.
	asynqScheduler := asynq.NewScheduler(redisOpt, &asynq.SchedulerOpts{
		LogLevel: asynq.WarnLevel,
	})
	if cfg.RunScheduler {
		// @hourly = at the top of every hour. The SQL query (24h window
		// minus 20h dedup) tolerates the cadence comfortably.
		//
		// asynq.Unique(time.Hour) is the belt-and-suspenders for the
		// multi-replica concern in the comment above: even if RUN_SCHEDULER
		// is mistakenly true on >1 replica (rolling deploy, ops misconfig),
		// asynq itself rejects the second Enqueue with the same uniqueness
		// key (task type + payload + queue). Within the 1-hour TTL only one
		// task actually lands in Redis, so workers don't double-process and
		// users don't get duplicate emails. Without this, the SQL dedup
		// only saves the SECOND firing — the first concurrent pair still
		// races (both queries see "no recent notification" before either
		// inserts).
		if _, err := asynqScheduler.Register(
			"@hourly",
			notif.NewDeadlineCheckTask(),
			asynq.Queue(taskQueue),
			asynq.Unique(time.Hour),
		); err != nil {
			fatal(ctx, err, "asynq scheduler register failed", "startup-scheduler-register-failed")
		}
	}

	// Daily LLM-debug retention sweep — registered whenever
	// persistence is on, INDEPENDENT of RUN_SCHEDULER. Unique for the day so
	// multiple replicas (or a restart) can't fire two sweeps; harmless if they
	// did, since deleting an already-deleted key is a no-op, but it keeps the
	// log honest about how often retention actually ran.
	if llmDebugPruner != nil {
		if _, err := asynqScheduler.Register(
			"@daily",
			llmdebug.NewPruneTask(),
			asynq.Queue(taskQueue),
			asynq.Unique(24*time.Hour),
		); err != nil {
			fatal(ctx, err, "asynq scheduler register failed", "startup-scheduler-register-failed")
		}
		log.Println("LLM debug retention sweep scheduled (@daily)")
	}

	// Daily demo organization refresh — independent of RUN_SCHEDULER, like the
	// retention sweep: it sends nothing. Unique for the day, and Ensure holds an
	// advisory lock, so several replicas still build once.
	if demoRefresher != nil {
		if _, err := asynqScheduler.Register(
			"@daily",
			demoorg.NewRefreshTask(),
			asynq.Queue(taskQueue),
			asynq.Unique(24*time.Hour),
		); err != nil {
			fatal(ctx, err, "asynq scheduler register failed", "startup-scheduler-register-failed")
		}
	}

	// Assistant uploads expire after 24 hours; an hourly sweep keeps an expired
	// blob at most an hour past its expiry. Unique for the hour, like the
	// deadline scan, so several replicas enqueue one sweep.
	if _, err := asynqScheduler.Register(
		"@hourly",
		chat.NewUploadPurgeTask(),
		asynq.Queue(taskQueue),
		asynq.Unique(time.Hour),
	); err != nil {
		fatal(ctx, err, "asynq scheduler register failed", "startup-scheduler-register-failed")
	}

	// Expired segment holds already block nothing; the sweep keeps the table
	// small. Unique for the window, so several replicas enqueue one sweep.
	if _, err := asynqScheduler.Register(
		"@every 15m",
		hold.NewSweepTask(),
		asynq.Queue(taskQueue),
		asynq.Unique(15*time.Minute),
	); err != nil {
		fatal(ctx, err, "asynq scheduler register failed", "startup-scheduler-register-failed")
	}

	// A TMX import runs in the process that received the file; one a restart or
	// a crash ended is marked failed. Unique for the window, like the hold sweep.
	if _, err := asynqScheduler.Register(
		"@every 15m",
		translationmemory.NewImportSweepTask(),
		asynq.Queue(taskQueue),
		asynq.Unique(15*time.Minute),
	); err != nil {
		fatal(ctx, err, "asynq scheduler register failed", "startup-scheduler-register-failed")
	}

	// An organization delete whose task was lost or failed for good is queued
	// again. Unique for the window, so several replicas enqueue one sweep.
	if _, err := asynqScheduler.Register(
		"@every 15m",
		tenantdelete.NewSweepTask(),
		asynq.Queue(taskQueue),
		asynq.Unique(15*time.Minute),
	); err != nil {
		fatal(ctx, err, "asynq scheduler register failed", "startup-scheduler-register-failed")
	}

	// A deleted project whose purge task was lost or failed for good is queued
	// again. Unique for the window, so several replicas enqueue one sweep.
	if _, err := asynqScheduler.Register(
		"@every 15m",
		projectdelete.NewSweepTask(),
		asynq.Queue(taskQueue),
		asynq.Unique(15*time.Minute),
	); err != nil {
		fatal(ctx, err, "asynq scheduler register failed", "startup-scheduler-register-failed")
	}

	// A deleted org's issue attachments leave their files queued; the hourly
	// sweep deletes them.
	if _, err := asynqScheduler.Register(
		"@hourly",
		issues.NewAttachmentPurgeTask(),
		asynq.Queue(taskQueue),
		asynq.Unique(time.Hour),
	); err != nil {
		fatal(ctx, err, "asynq scheduler register failed", "startup-scheduler-register-failed")
	}

	// Use Start() (non-blocking) — NOT Run(), which installs its own
	// SIGTERM/SIGINT handler and races with main's signal.NotifyContext
	// for shutdown ownership. Start() lets us call Shutdown() explicitly
	// in the teardown block below, the same pattern asynqServer uses.
	logging.Info(ctx, "starting asynq scheduler", "startup-scheduler-starting",
		logging.Label("deadline_scan", strconv.FormatBool(cfg.RunScheduler)),
		logging.Label("llm_debug_retention", strconv.FormatBool(llmDebugPruner != nil)),
		logging.Label("demo_organization", strconv.FormatBool(demoRefresher != nil)))
	if err := asynqScheduler.Start(); err != nil {
		fatal(ctx, err, "asynq scheduler start failed", "startup-scheduler-failed")
	}
	// Tell the handler whether it's the wired-up replica so /health/scheduler
	// can distinguish "scheduler off by config" from "scheduler on but
	// silent." Must happen after the gate above so the value is correct.
	scheduledHandler.SetEnabled(cfg.RunScheduler)

	if cfg.GoogleClientID != "" {
		log.Println("Google OAuth SSO enabled")
	}
	if cfg.MicrosoftClientID != "" {
		log.Println("Microsoft (Entra ID) SSO enabled")
	}
	if len(cfg.PlatformAdminEmails) > 0 {
		log.Printf("Platform operators configured: %d", len(cfg.PlatformAdminEmails))
	}
	if cfg.DevAutoLogin {
		log.Println("Dev auto-login enabled (DEV_AUTO_LOGIN=true)")
	}

	// Inspector for cancelling active asynq tasks on job delete
	asynqInspector := asynq.NewInspector(redisOpt)

	// draining is closed at the top of the shutdown sequence so long-lived SSE
	// handlers can end their streams. See the shutdown block at the end of main.
	draining := make(chan struct{})

	// Segment holds reach every replica's open editors at once: a trigger
	// notifies on commit and pgListener receives it.
	holdFeed := hold.NewFeed()
	holdFeed.ListenOn(pgListener)
	go func() {
		defer errtrack.Monitor(ctx, "startup", "pg-listen")
		pgListener.Run(ctx, pool.Config().ConnConfig)
	}()

	// Per-tenant LLM model catalog. Fills on demand and is
	// kept fresh by a ticker that only re-reads what is actually being used. An
	// unreadable AI_SECRETS_KEY means no stored key can be decrypted at all, so
	// the catalog stays empty rather than the process failing to start.
	aiCatalog := ai.NewCatalog(nil, nil, cfg.AIModelListLimit)
	if aiEnc, err := secrets.NewEncryptor(cfg.AISecretsKey); err != nil {
		logging.Warn(ctx, err, "AI model catalog disabled", "startup-ai-catalog-disabled")
	} else {
		aiCatalog = ai.NewCatalog(ai.NewHTTPModelLister(), ai.NewDBKeyLoader(queries, aiEnc), cfg.AIModelListLimit)
		refresher := ai.NewRefresher(aiCatalog,
			time.Duration(cfg.AIModelRefreshMinutes)*time.Minute, ai.DefaultIdleTTL)
		go func() {
			defer errtrack.Monitor(ctx, "startup", "ai-model-refresh")
			refresher.Run(ctx)
		}()
		logging.Info(ctx, fmt.Sprintf("AI model catalog: refreshing warm entries every %d min, %d models per vendor",
			cfg.AIModelRefreshMinutes, cfg.AIModelListLimit), "startup-ai-catalog-on")
	}

	// User documentation is embedded and parsed once. A malformed page is a
	// build-time mistake, so it fails startup rather than a user's request.
	docsIndex, err := productdocs.Default()
	if err != nil {
		fatal(ctx, err, "user documentation failed to parse", "startup-docs-invalid")
	}
	// Not fatal: the server has to run for `make docs-screenshots` to take
	// them. The productdocs unit test is strict, so this never ships.
	if missing := docsIndex.MissingScreenshots(); len(missing) > 0 {
		logging.Warn(ctx, nil, "user documentation references screenshots that are not in content/assets/ — run make docs-screenshots",
			"startup-docs-missing-screenshots", slog.Int("count", len(missing)), slog.Any("missing", missing))
	}

	apiDocsIndex, err := productdocs.API()
	if err != nil {
		fatal(ctx, err, "API guides failed to parse", "startup-api-docs-invalid")
	}

	apiSpec, err := publicapi.LoadSpec()
	if err != nil {
		fatal(ctx, err, "API specification failed to load", "startup-api-spec-invalid")
	}

	router := api.NewRouter(api.Deps{
		Docs:                         docsIndex,
		APIDocs:                      apiDocsIndex,
		APISpec:                      apiSpec,
		Draining:                     draining,
		Queries:                      queries,
		DB:                           pool,
		Storage:                      storage,
		JWTSecret:                    cfg.JWTSecret,
		Enqueuer:                     enqueuer,
		TaskCanceller:                asynqInspector,
		GoogleClientID:               cfg.GoogleClientID,
		GoogleClientSecret:           cfg.GoogleClientSecret,
		MicrosoftClientID:            cfg.MicrosoftClientID,
		MicrosoftClientSecret:        cfg.MicrosoftClientSecret,
		BaseURL:                      cfg.BaseURL,
		DocsHost:                     cfg.DocsHost,
		DeveloperHost:                cfg.DeveloperHost,
		DevAutoLogin:                 cfg.DevAutoLogin,
		OAuthRedirectBaseURL:         cfg.OAuthRedirectBaseURL,
		OAuthRelayDomain:             cfg.OAuthRelayDomain,
		OpenAIAppsChallenge:          cfg.OpenAIAppsChallenge,
		PlatformAdminEmails:          cfg.PlatformAdminEmails,
		DemoOrg:                      cfg.DemoOrg,
		TrustedProxyHops:             cfg.TrustedProxyHops,
		NotificationService:          notifService,
		NotificationScheduledHandler: scheduledHandler,
		NotificationBroker:           notifBroker,
		HoldFeed:                     holdFeed,
		EmailSender:                  emailSender,
		DevOutbox:                    devOutbox,
		AISecretsKey:                 cfg.AISecretsKey,
		AICatalog:                    aiCatalog,
		SpellcheckLTURL:              cfg.SpellcheckLTURL,
		MaxUploadBytes:               int64(cfg.MaxUploadMB) << 20,
		LLMDebugRing:                 llmDebugRing,
		LLMDebugRecorder:             llmDebugRecorder,
		LLMDebugReader:               llmDebugReader,
		LLMDebugStats:                statsSource(llmDebugStore),
		Environment:                  cfg.Environment,
		Revision:                     cfg.Revision,
		HoneybadgerAPIKey:            cfg.HoneybadgerAPIKey,
	})

	// Serve React SPA from STATIC_DIR in production (replaces Vite dev proxy).
	// API routes (/api/*, /health, /auth/*) take priority — this is a fallback.
	if cfg.StaticDir != "" {
		log.Printf("Serving static files from %s", cfg.StaticDir)
		addSPAHandler(router, cfg.StaticDir)
	}

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
	}

	go func() {
		// net/http recovers per-connection panics; a panic from ListenAndServe
		// itself would kill the process silently.
		defer errtrack.Monitor(ctx, "startup", "http-server")
		log.Printf("Starting server on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fatal(ctx, err, "http server error", "startup-http-failed")
		}
	}()

	<-ctx.Done()
	log.Println("Shutting down...")

	// Tell the long-lived SSE handlers to close FIRST.
	//
	// http.Server.Shutdown closes the listeners and then waits for every
	// connection to become idle; it never cancels request contexts, and Go's
	// docs put the onus on the caller to notify long-lived connections. The
	// notification stream is open for every logged-in user at all times and only
	// ends when the client disconnects, so without this signal Shutdown waits
	// out the whole grace period whenever one browser is open.
	close(draining)

	steps := shutdownSteps{
		stopWorkers: func() {
			asynqScheduler.Shutdown()
			var wg sync.WaitGroup
			for _, srv := range asynqServers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					srv.Shutdown()
				}()
			}
			wg.Wait()
		},
		drainHTTP: func(ctx context.Context) error {
			err := srv.Shutdown(ctx)
			if err != nil {
				// NOT fatal(), and NOT StartupFailure.
				//
				// A StartupFailure means "a pod cannot come up" — the most urgent
				// thing this service reports — so shutdown timeouts must not dilute
				// it. This is still worth reporting: the streams close on
				// `draining`, so a timeout here means a handler is genuinely wedged
				// past its grace.
				logging.Error(context.Background(), err, "server shutdown did not complete", "shutdown-incomplete")
				errtrack.Capture(context.Background(), err, errtrack.CaptureOptions{
					Class:     errtrack.ClassShutdownFailure,
					Message:   "server shutdown did not complete in time",
					Subsystem: "shutdown",
					Event:     "shutdown-incomplete",
				})
			}
			return err
		},
		closeBroker:  notifBroker.Close,
		flushReports: flushNotices,
		closePool:    pool.Close,
	}
	if llmDebugStore != nil {
		// Flush buffered debug entries AFTER the worker has drained — the
		// pipeline records right up to the end, so closing earlier would
		// discard the last batch of exactly the run an operator is most likely
		// to be investigating.
		steps.flushLLMDebug = func(ctx context.Context) {
			if err := llmDebugStore.Close(ctx); err != nil {
				logging.Warn(context.Background(), err, "llm debug store: final flush did not complete", "shutdown-llm-debug-flush-incomplete")
			}
		}
	}
	// The exit stays 0 when the HTTP drain times out. Graceful shutdown ran
	// and in-flight work got its grace period; a non-zero exit makes
	// Kubernetes report the container's last state as Error on every deploy.
	if err := shutdown(serverShutdownLimits, steps); err != nil {
		return
	}
	log.Println("Server stopped")
}

// addSPAHandler mounts a catch-all handler that serves static files from dir.
// If the requested path matches a file on disk, it's served directly (JS, CSS, images).
// Otherwise, index.html is served to support client-side SPA routing.
func addSPAHandler(r chi.Router, dir string) {
	absDir, _ := filepath.Abs(dir)
	fileServer := http.FileServer(http.Dir(absDir))

	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		reqPath := strings.TrimPrefix(r.URL.Path, "/")
		if reqPath == "" {
			reqPath = "index.html"
		}

		fullPath := filepath.Join(absDir, filepath.FromSlash(reqPath))
		if info, err := os.Stat(fullPath); err == nil && !info.IsDir() {
			setStaticCacheControl(w, reqPath)
			fileServer.ServeHTTP(w, r)
			return
		}

		// A request for a missing file WITH an extension is an asset request
		// (e.g. a content-hashed /assets/CATEditor-<hash>.js from a browser still
		// holding the pre-deploy index.html). Serving index.html for it returns
		// HTML under a .js URL → the browser MIME-rejects the ES module and the
		// CAT editor's lazy import fails ("Failed to fetch dynamically imported
		// module"). Return a clean 404 so the client fails fast and can reload
		// for the new bundle (see lazyWithReload / INC entry). Only extensionless
		// paths (client-side routes) get the index.html fallback below.
		if filepath.Ext(reqPath) != "" {
			http.NotFound(w, r)
			return
		}

		// SPA fallback: serve index.html for client-side routing
		indexPath := filepath.Join(absDir, "index.html")
		if _, err := fs.Stat(os.DirFS(absDir), "index.html"); err != nil {
			http.NotFound(w, r)
			return
		}
		setStaticCacheControl(w, "index.html")
		http.ServeFile(w, r, indexPath)
	})
}

// setStaticCacheControl makes a deploy visible on the next navigation.
// Content-hashed bundles under /assets/ get a one-year immutable cache: a deploy
// changes the hash and thus the URL, so a stale entry is never requested again.
// Everything else — index.html above all, which names those hashes — must be
// revalidated every time. A browser serving a stale index.html would request
// bundle hashes that no longer exist on the freshly-deployed pod, which 404 (see
// the missing-asset branch above). no-cache still allows a cheap 304 via
// ETag/Last-Modified when nothing changed.
func setStaticCacheControl(w http.ResponseWriter, reqPath string) {
	if strings.HasPrefix(reqPath, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
}

// statsSource converts a possibly-nil *Store into a truly-nil interface — a
// typed nil would be non-nil and panic on the first Stats() call.
func statsSource(s *llmdebug.Store) llmdebug.StatsSource {
	if s == nil {
		return nil
	}
	return s
}
