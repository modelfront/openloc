package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/honeybadger-io/honeybadger-go"
	"github.com/jackc/pgx/v5/pgxpool"
	"openloc/internal/ai"
	"openloc/internal/aiengine"
	"openloc/internal/aiprompt"
	"openloc/internal/airouter"
	"openloc/internal/analysis"
	analyticsPkg "openloc/internal/analytics"
	"openloc/internal/appearance"
	"openloc/internal/auth"
	"openloc/internal/chat"
	"openloc/internal/comments"
	"openloc/internal/customer"
	"openloc/internal/dashboard"
	"openloc/internal/database"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/extension"
	"openloc/internal/file"
	"openloc/internal/hold"
	"openloc/internal/httputil"
	"openloc/internal/invite"
	"openloc/internal/issues"
	"openloc/internal/job"
	"openloc/internal/keyattach"
	"openloc/internal/label"
	"openloc/internal/language"
	"openloc/internal/llmdebug"
	"openloc/internal/logging"
	mcppkg "openloc/internal/mcp"
	"openloc/internal/mtengine"
	"openloc/internal/mtrouter"
	notif "openloc/internal/notification"
	"openloc/internal/oauthserver"
	"openloc/internal/pipeline"
	"openloc/internal/productdocs"
	"openloc/internal/project"
	"openloc/internal/projectdelete"
	"openloc/internal/projectevents"
	"openloc/internal/publicapi"
	"openloc/internal/qa"
	"openloc/internal/secrets"
	"openloc/internal/segment"
	"openloc/internal/spellcheck"
	"openloc/internal/stepdef"
	"openloc/internal/suggestion"
	tmpl "openloc/internal/template"
	"openloc/internal/tenantdelete"
	"openloc/internal/terminology"
	"openloc/internal/timing"
	"openloc/internal/tmprofile"
	"openloc/internal/translation"
	"openloc/internal/translationmemory"
	"openloc/internal/userpair"
	"openloc/internal/workflow"
	"openloc/internal/workflowlib"
)

// TaskEnqueuer enqueues background tasks for async processing. Production
// wiring (cmd/server/main.go) provides a *worker.Enqueuer that implements
// every method. Tests can implement only what they exercise — the
// analysis handler depends on EnqueueAnalysis via its own smaller
// interface (analysis.Enqueuer), so unrelated tests don't pay the cost
// of stubbing it.
type TaskEnqueuer interface {
	EnqueuePipeline(fileID int64, sourceLang, targetLang string, jobID, tenantID int64, phases pipeline.TranslatePhases, triggeredBy *int64) error
	EnqueueAnalysis(analysisID, tenantID int64) error
}

type Deps struct {
	Queries       db.Querier
	DB            db.DBTX        // raw DB for batch operations
	Storage       file.BlobStore // nil = no file storage (tests)
	JWTSecret     string
	Enqueuer      TaskEnqueuer      // nil falls back to synchronous execution
	TaskCanceller job.TaskCanceller // nil skips asynq-level cancellation on delete

	// Auth / OAuth
	GoogleClientID        string
	GoogleClientSecret    string
	MicrosoftClientID     string
	MicrosoftClientSecret string
	BaseURL               string
	// Hosts of the public help and API docs (config.DocsHost, config.DeveloperHost),
	// each serving its site at the root; empty = the site stays at its /docs or
	// /api-docs prefix.
	DocsHost      string
	DeveloperHost string
	DevAutoLogin  bool
	// Shared OAuth redirect URI (config.OAuthRedirectBaseURL /
	// config.OAuthRelayDomain). Both empty = this deployment
	// owns its redirect URI and never relays, which is dev and test behaviour.
	OAuthRedirectBaseURL string
	OAuthRelayDomain     string
	// OpenAI's domain-verification token (config.OpenAIAppsChallenge).
	OpenAIAppsChallenge string
	// Platform operators allowed to approve/reject self-serve org signups.
	// Same list gates /api/platform/* and receives signup emails.
	PlatformAdminEmails []string
	// DemoOrg keeps the Playground (config.DemoOrg). Off, nothing offers it:
	// entering is refused and saved tours are not listed.
	DemoOrg bool
	// Reverse proxies in front of the server (config.TrustedProxyHops).
	TrustedProxyHops int

	// AI provider secrets encryption key (config.AISecretsKey). Any string;
	// SHA-256'd to an AES-256 key. Provider API keys are stored encrypted.
	AISecretsKey string

	// ExtensionHTTPClient is the client extensions reach their vendors with.
	// nil uses the extension runtime's default.
	ExtensionHTTPClient *http.Client

	// LanguageTool /v2/check endpoint (config.SpellcheckLTURL). Defaults to the
	// self-hosted compose service; see internal/config for why.
	SpellcheckLTURL string

	// Notifications
	NotificationService *notif.Service
	// EmailSender delivers transactional emails (invites). Same sender the
	// notification service uses. nil disables invite emails.
	EmailSender notif.Sender
	// Dev-only record of the emails EmailSender sent, so E2E tests can follow
	// invite and reset links. Built in main.go only when DEV_AUTO_LOGIN=true;
	// nil (production) leaves GET /api/dev/outbox unmounted ⇒ 404.
	DevOutbox *notif.Outbox
	// Optional. If set, exposes the owner/admin /api/admin/notifications/deadline-check
	// for manual cron triggering (ops + smoke testing). nil disables the endpoint.
	NotificationScheduledHandler *notif.ScheduledHandler
	// Optional. If set, exposes GET /api/notifications/stream (SSE) for
	// real-time bell delivery. nil disables — the
	// frontend falls back to polling /api/notifications/count every 60s.
	NotificationBroker notif.Broker
	// Optional. If set, exposes GET /api/jobs/{jobID}/holds/stream (SSE), so an
	// open editor sees segment holds change at once; it must be listening
	// (hold.Feed.ListenOn a running pglisten.Listener). nil disables — the
	// editor polls GET /holds every 5 s.
	HoldFeed *hold.Feed

	// Draining is closed when the process begins shutting down, so the
	// long-lived SSE handlers (notification stream, segment hold stream, chat
	// turn) can close their connections instead of holding
	// http.Server.Shutdown open until its deadline. Optional: nil means "never
	// drains", which is what tests want.
	Draining <-chan struct{}

	// Per-tenant LLM model catalog, fetched from each
	// vendor's API and refreshed on a ticker in main.go. Optional: nil builds a
	// catalog that never fetches, which is what tests want — they prime it
	// directly rather than reaching a provider.
	AICatalog *ai.Catalog

	// Dev-only LLM/extension request-response ring (internal/llmdebug).
	// Constructed in main.go only when DEV_AUTO_LOGIN=true; nil (production)
	// leaves GET /api/debug/llm/calls unmounted ⇒ 404, same gate semantics
	// as /api/auth/dev-login.
	//
	// This field gates the ROUTE ONLY. Capture is wired from
	// LLMDebugRecorder below, so persisting in production never mounts a
	// viewer — Entry carries no tenant_id, so one would leak segment text
	// across tenants.
	LLMDebugRing *llmdebug.Ring

	// Where captured provider calls go. Set to the dev ring, the prod
	// *llmdebug.Store, or nil for no capture at all. Kept separate from
	// LLMDebugRing precisely so the two decisions — record vs. expose — can't
	// be conflated by a future edit.
	LLMDebugRecorder llmdebug.Recorder

	// Production feed: reads back the NDJSON objects the Store wrote. When set
	// (and no dev ring exists) the viewer is mounted inside the platform-admin
	// group, because these entries span every tenant — Entry has no tenant_id.
	// nil leaves production with no viewer at all.
	LLMDebugReader llmdebug.Source

	// Write-side counters for the persisted feed, surfaced alongside the entries
	// so "the entry is missing" can be told apart from "the entry was dropped or
	// its PUT failed" without pod-log access.
	LLMDebugStats llmdebug.StatsSource

	// Observability. Environment and Revision are served to the SPA
	// via GET /api/config; HoneybadgerAPIKey enables the browser-notice proxy
	// (empty = disabled, the same fail-safe default as the backend reporter).
	// The key itself is never sent to a client. HoneybadgerEndpoint overrides
	// the upstream base URL — tests point it at an httptest.Server.
	Environment         string
	Revision            string
	HoneybadgerAPIKey   string
	HoneybadgerEndpoint string

	// In-app user documentation. main.go builds it from
	// the embedded content and refuses to start if a page is malformed. nil
	// leaves /api/docs unmounted — the shape tests use when they don't
	// exercise docs.
	Docs *productdocs.Index
	// API guides, served at /api/api-docs next to the OpenAPI reference.
	// nil leaves the route unmounted, like Docs.
	APIDocs *productdocs.Index
	// The customer API's OpenAPI document. main.go loads it and refuses to
	// start if it is invalid. nil leaves /api/v1 unmounted.
	APISpec *publicapi.Spec
}

// Per-key budget for /api/v1, documented in the API guides' errors page.
const (
	publicAPIRPS   = 10
	publicAPIBurst = 40
)

func NewRouter(deps Deps) chi.Router {
	r := chi.NewRouter()

	// A nil catalog is a catalog that never fetches: empty model lists, and
	// model validation that can't reject what it can't see. Tests take this
	// path and prime it via Set when they need a list.
	catalog := deps.AICatalog
	if catalog == nil {
		catalog = ai.NewCatalog(nil, nil, ai.DefaultModelLimit)
	}

	// Built once here rather than per route group: an MT engine keeps its key
	// on a vendor token, so every path that reads a provider's language catalog
	// needs to decrypt one. nil leaves those reads on the static tables.
	var mtDecryptor mtengine.Decryptor
	if enc, err := secrets.NewEncryptor(deps.AISecretsKey); err != nil {
		logging.Error(context.Background(), err,
			"MT engine catalogs limited to the built-in language lists", "mt-decryptor-unavailable")
	} else {
		mtDecryptor = enc
	}

	r.Use(middleware.RequestID)
	r.Use(httputil.ClientIP(deps.TrustedProxyHops))
	r.Use(middleware.Recoverer)
	// Ordering is load-bearing, see internal/api/observability.go:
	// requestObservability must sit inside Recoverer and outside
	// honeybadger.Handler so a panic is reported exactly once.
	// requestObservability replaces chi's middleware.Logger — running both would
	// emit two lines per request, one unstructured and one ECS.
	r.Use(requestObservability)
	r.Use(honeybadger.Handler)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{deps.BaseURL},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Use(docSitesOnly(deps.BaseURL, docSites(deps.DocsHost, deps.DeveloperHost)))

	docHosts := []string{deps.DocsHost, deps.DeveloperHost}
	r.Use(blockCrawlers(docHosts))

	// Comments
	commentHandler := comments.NewHandler(deps.Queries)

	// Spell check (LanguageTool). Routes are registered inside the authenticated
	// group below: spell check sends text to LanguageTool, so it must never be
	// reachable without auth.
	spellcheckHandler := spellcheck.NewHandler(deps.Queries, spellcheck.NewClient(deps.SpellcheckLTURL))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		httputil.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// /health/scheduler — exposes the deadline cron's last-run state for
	// monitoring. Unauthenticated like /health so ops
	// dashboards can scrape it without juggling tokens. Returns enabled
	// flag + last_run_at (nullable, RFC3339) + last_run_dispatched.
	if deps.NotificationScheduledHandler != nil {
		r.Get("/health/scheduler", deps.NotificationScheduledHandler.HandleHealth)
	}

	// /health/complete — deep health check: DB connection + schema + migration version.
	// Use for monitoring dashboards, deploy verification, and manual checks.
	// NOT used for k8s probes (too heavy for every 5s check).
	r.Get("/health/complete", func(w http.ResponseWriter, r *http.Request) {
		if err := database.DeepHealthCheck(r.Context(), deps.DB.(*pgxpool.Pool)); err != nil {
			// Don't surface raw err.Error() — this endpoint is unauthenticated
			// (per the doc above) and the DeepHealthCheck error chain can include
			// connection strings, schema names, migration metadata. Log full
			// context server-side; return a stable category so dashboards can
			// alert without scraping internals.
			logging.Error(r.Context(), err, "health check unhealthy", "health-complete-unhealthy")
			httputil.JSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "unhealthy",
				"error":  "deep health check failed (see server logs)",
			})
			return
		}
		version := os.Getenv("VERSION")
		if version == "" {
			version = "dev"
		}
		httputil.JSON(w, http.StatusOK, map[string]string{
			"status":  "healthy",
			"version": version,
		})
	})

	// /version — used by k8s liveness/readiness probes
	r.Get("/version", func(w http.ResponseWriter, r *http.Request) {
		version := os.Getenv("VERSION")
		if version == "" {
			version = "dev"
		}
		httputil.JSON(w, http.StatusOK, map[string]string{"version": version})
	})

	// /api/config — SPA bootstrap config, fetched once per page load.
	// Unauthenticated: the login page needs it too. error_reporting tells the
	// frontend whether to enable its Honeybadger SDK; the key itself never
	// leaves the server (notices are proxied through /api/errtrack below).
	r.Get("/api/config", handleAppConfig(deps))

	// Browser error notices → Honeybadger, with the API key attached
	// server-side (see errtrack_proxy.go). Unauthenticated (the JS SDK's
	// transport can't carry our bearer token), so rate-limited per IP:
	// 10/min with burst 5 — the client additionally throttles itself to
	// 5/min per error type and 10 per page load.
	noticeProxy := newErrtrackProxy(deps)
	errtrackLimiter := httputil.NewRateLimiter(10.0/60.0, 5)
	r.With(errtrackLimiter.Middleware).Post(errtrackNoticePath, noticeProxy.handleNotice)

	r.Get("/robots.txt", robotsTxt(docHosts))

	// Auth routes (public — no JWT required, rate-limited to prevent brute force)
	authHandler := auth.NewHandler(deps.Queries, deps.DB.(*pgxpool.Pool), deps.JWTSecret, deps.BaseURL, deps.DevAutoLogin, deps.EmailSender, deps.PlatformAdminEmails)
	// An organization delete runs on the worker; without a queue, in the request.
	if q, ok := deps.Enqueuer.(tenantdelete.TaskEnqueuer); ok {
		authHandler.SetTenantDeleteQueue(q)
	}
	authHandler.SetDemoOrg(deps.DemoOrg)
	oauthRelay := auth.NewOAuthRelay(deps.BaseURL, deps.OAuthRedirectBaseURL, deps.OAuthRelayDomain)
	googleCfg := auth.GoogleOAuthConfig(deps.GoogleClientID, deps.GoogleClientSecret, oauthRelay.RedirectBaseURL)
	microsoftCfg := auth.MicrosoftOAuthConfig(deps.MicrosoftClientID, deps.MicrosoftClientSecret, oauthRelay.RedirectBaseURL)
	authLimiter := httputil.NewRateLimiter(5, 10) // 5 req/sec per IP, burst of 10

	r.Post("/api/auth/logout", authHandler.HandleLogout)
	r.Get("/api/auth/config", authHandler.HandleAuthConfig(googleCfg != nil, microsoftCfg != nil))
	// Rate-limit the unauthenticated sign-in entry points (5 rps/IP, burst 10).
	r.With(authLimiter.Middleware).Post("/api/auth/dev-login", authHandler.HandleDevLogin)
	r.With(authLimiter.Middleware).Get("/api/auth/google", authHandler.HandleGoogleLogin(googleCfg, oauthRelay))
	r.With(authLimiter.Middleware).Get("/api/auth/google/callback", authHandler.HandleGoogleCallback(googleCfg, oauthRelay))
	r.With(authLimiter.Middleware).Get("/api/auth/microsoft", authHandler.HandleMicrosoftLogin(microsoftCfg, oauthRelay))
	r.With(authLimiter.Middleware).Get("/api/auth/microsoft/callback", authHandler.HandleMicrosoftCallback(microsoftCfg, oauthRelay))
	// Email+password auth: login, invite-based account creation, and
	// password reset. All public + rate-limited like the OAuth entry points.
	r.With(authLimiter.Middleware).Post("/api/auth/login", authHandler.HandlePasswordLogin)
	r.With(authLimiter.Middleware).Post("/api/auth/accept-invite", authHandler.HandleAcceptInvite)
	r.With(authLimiter.Middleware).Post("/api/auth/forgot-password", authHandler.HandleForgotPassword)
	r.With(authLimiter.Middleware).Post("/api/auth/reset-password", authHandler.HandleResetPassword)

	// One invite service for REST and MCP: it holds the per-user email budget.
	inviteSvc := invite.NewService(deps.Queries, deps.BaseURL, deps.EmailSender)
	inviteHandler := invite.NewHandler(deps.Queries, deps.JWTSecret, inviteSvc)
	// Public invite-token lookup for the accept page (flat 404 on any invalid
	// token — not an oracle). Rate-limited like the other unauthenticated routes.
	r.With(authLimiter.Middleware).Get("/api/invites/lookup/{token}", inviteHandler.HandleLookup)

	// userExists closure — GetActiveUser returns ErrNoRows for both
	// deleted-from-DB and soft-deactivated users. The row
	// is fetched anyway, so the email comes for free — it feeds error-notice
	// identity (user_email in Honeybadger).
	userExists := func(ctx context.Context, id int64) (string, error) {
		u, err := deps.Queries.GetActiveUser(ctx, id)
		if err != nil {
			return "", err
		}
		return u.Email, nil
	}

	// tenantActive gates the pending/rejected orgs out of the app.
	// auth.ErrTenantNotApproved unless the tenant is 'approved'; auth.Middleware
	// turns that into a 403 org_pending.
	tenantActive := func(ctx context.Context, tenantID int64) (bool, error) {
		t, err := deps.Queries.GetTenantStatus(ctx, tenantID)
		if err != nil {
			return false, err
		}
		if t.Status != "approved" {
			return false, auth.ErrTenantNotApproved
		}
		return t.Kind == auth.TenantKindDemo, nil
	}

	// Issues users report to the platform operators. Mounted only when the
	// deployment has operators, who are the ones reading them.
	issueSvc := issues.NewService(deps.DB.(*pgxpool.Pool), deps.Storage, deps.NotificationService,
		deps.EmailSender, deps.PlatformAdminEmails, deps.BaseURL)
	// Operator emails go to the worker, so a report or a reply does not wait
	// on the mail server. Without a queue they are sent in the request.
	if q, ok := deps.Enqueuer.(issues.TaskEnqueuer); ok {
		issueSvc.WithEmailQueue(q)
	}
	// Signs the assistant's checks of the reports it writes, the same on
	// every replica.
	issueSvc.WithVerificationKey(issues.DeriveVerificationKey([]byte(deps.JWTSecret)))

	// Identity-scoped routes: authenticated but no org selected yet.
	// The org picker lives here — accepts both identity and tenant tokens.
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireIdentity(deps.JWTSecret, userExists))
		r.Use(attachIdentity(deps)) // identity into the logging.Scope
		r.Get("/api/auth/me", authHandler.HandleGetMe)
		r.Patch("/api/auth/me", authHandler.HandleUpdateMe)
		r.Get("/api/auth/orgs", authHandler.HandleListOrgs)
		r.Post("/api/auth/orgs/{tenantID}/select", authHandler.HandleSelectOrg)
		// The shared read-only demo organization: any
		// signed-in user may enter it and leave it again.
		r.With(authLimiter.Middleware).Post("/api/auth/demo/enter", authHandler.HandleEnterDemo)
		r.Post("/api/auth/demo/leave", authHandler.HandleLeaveDemo)
		// Self-serve org signup: an identity with no org creates one
		// (pending operator approval) and becomes its owner.
		r.With(authLimiter.Middleware).Post("/api/auth/orgs", authHandler.HandleCreateOrg)

		// Platform operator surface: list every org and drive its
		// lifecycle (approve/reject signups, disable/enable existing orgs).
		// Gated by PLATFORM_ADMIN_EMAILS on top of a valid identity.
		r.Group(func(r chi.Router) {
			r.Use(authHandler.RequirePlatformAdmin)
			r.Get("/api/platform/orgs", authHandler.HandleListPlatformOrgs)
			// Provision an org for a customer + seat its owner via an
			// owner-role invite.
			r.Post("/api/platform/orgs", authHandler.HandlePlatformCreateOrg)
			r.Post("/api/platform/orgs/{tenantID}/status", authHandler.HandleSetOrgStatus)
			// Operator impersonation: enter an org to inspect/support it.
			r.Post("/api/platform/orgs/{tenantID}/login", authHandler.HandleLoginAsOrg)

			// Every org's issues: read, answer, change status, assign,
			// prioritize, tag, add internal notes.
			if issueSvc.Enabled() {
				ph := issues.NewPlatformHandler(issueSvc)
				r.Get("/api/platform/issues", ph.HandleList)
				r.Get("/api/platform/issues/orgs", ph.HandleOrgs)
				r.Get("/api/platform/issues/assignees", ph.HandleAssignees)
				r.Get("/api/platform/issues/tags", ph.HandleTags)
				r.Get("/api/platform/issues/{issueID}", ph.HandleGet)
				r.Delete("/api/platform/issues/{issueID}", ph.HandleDelete)
				r.Post("/api/platform/issues/{issueID}/status", ph.HandleSetStatus)
				r.Post("/api/platform/issues/{issueID}/comments", ph.HandleComment)
				r.Post("/api/platform/issues/{issueID}/notes", ph.HandleNote)
				r.Patch("/api/platform/issues/{issueID}", ph.HandleUpdate)
				r.Get("/api/platform/issues/{issueID}/attachments/{attachmentID}", ph.HandleAttachment)
			}

			// LLM/extension viewer over persisted objects.
			// Platform-owner only and cross-tenant by construction: an Entry
			// carries no tenant_id, so this shows every org's segment text and
			// must never be reachable by a tenant admin. Registered only when
			// there's no dev ring, so the path is mounted exactly once.
			if deps.LLMDebugReader != nil && deps.LLMDebugRing == nil {
				r.Get("/api/debug/llm/calls",
					llmdebug.NewHandler(deps.LLMDebugReader).WithStats(deps.LLMDebugStats).HandleList)
			}
		})
	})

	// OAuth 2.1 authorization server: lets Claude/ChatGPT
	// connect to /mcp on a user's behalf. Discovery + authorize + token are
	// PUBLIC by design — the whole point is that clients arrive uncredentialed;
	// each handler enforces exactly what it needs.
	oauthSrv := oauthserver.NewServer(deps.Queries, deps.JWTSecret, deps.BaseURL)
	r.Get("/.well-known/oauth-authorization-server", oauthSrv.HandleAuthServerMetadata)
	r.Get("/.well-known/oauth-protected-resource", oauthSrv.HandleProtectedResourceMetadata)
	r.Get("/.well-known/oauth-protected-resource/mcp", oauthSrv.HandleProtectedResourceMetadata)
	r.Get("/.well-known/openai-apps-challenge", openAIAppsChallenge(deps.OpenAIAppsChallenge))
	r.Get("/.well-known/microsoft-identity-association.json", microsoftIdentityAssociation(deps.MicrosoftClientID))
	// Per-IP rate limit, same as every other public sign-in route: /register
	// writes a client row per call and /authorize triggers an outbound CIMD
	// fetch, so anonymous floods need a ceiling.
	r.With(authLimiter.Middleware).Get("/oauth/authorize", oauthSrv.HandleAuthorize)
	r.With(authLimiter.Middleware).Post("/oauth/authorize", oauthSrv.HandleAuthorize)
	r.With(authLimiter.Middleware).Post("/oauth/token", oauthSrv.HandleToken)
	r.With(authLimiter.Middleware).Post("/oauth/register", oauthSrv.HandleRegister)

	// Shared by the authenticated group below and the explicit /mcp chain.
	authMW := auth.Middleware(deps.JWTSecret, userExists, tenantActive, auth.NewPATResolver(deps.Queries), oauthserver.NewResolver(deps.Queries))

	// The user help and the API guides — public. A signed-in caller gets the
	// pages for their role; an anonymous one (anyone, and every visitor on the
	// docs and developer hosts) gets the pages every role may read. Global
	// product content, not tenant data: no tenant scoping. Rate-limited per IP
	// like the other unauthenticated routes, one budget for both sets.
	docsLimiter := httputil.NewRateLimiter(10, 40)
	mountDocs := func(path string, idx *productdocs.Index, extra func(chi.Router)) {
		h := productdocs.NewHandler(idx)
		r.Route(path, func(r chi.Router) {
			r.Use(docsLimiter.Middleware, auth.Optional(authMW), attachIdentity(deps))
			if extra != nil {
				extra(r)
			}
			// Static routes before the slug catch-all.
			r.Get("/search", h.HandleSearch)
			r.Get("/assets/*", h.HandleAsset)
			r.Get("/", h.HandleTree)
			r.Get("/*", h.HandlePage)
		})
	}
	if deps.Docs != nil {
		mountDocs("/api/docs", deps.Docs, nil)
	}
	if deps.APIDocs != nil {
		mountDocs("/api/api-docs", deps.APIDocs, func(r chi.Router) {
			// The endpoint reference reads the customer API's OpenAPI document
			// here, without a key. /api/v1/openapi.json, for API clients, keeps
			// the key and the per-key limit.
			if deps.APISpec != nil {
				r.Get("/openapi.json", deps.APISpec.ServeHTTP)
			}
		})
	}

	// MCP server. Constructed up here because /mcp mounts outside
	// the authenticated group (see below) while the built-in chat inside the
	// group shares the same instance.
	mcpBackend := mcppkg.WithInsightDeps(
		mcppkg.NewDirectBackendWithPool(deps.Queries, deps.DB.(*pgxpool.Pool), deps.Docs, catalog, mtDecryptor, deps.Storage),
		mcppkg.InsightDeps{AnalysisEnqueuer: deps.Enqueuer, Invites: inviteSvc})
	mcppkg.WireIssues(mcpBackend, issueSvc)
	mcpServer, mcpErr := mcppkg.NewServer(mcppkg.Deps{
		Backend:    mcpBackend,
		Audit:      deps.Queries,
		Version:    os.Getenv("VERSION"),
		ConfirmKey: mcppkg.DeriveConfirmKey([]byte(deps.JWTSecret)),
		// Lets a read-scoped Claude/ChatGPT grant re-authorize for write.
		StepUpChallenge: oauthSrv.WriteScopeChallenge(),
		IssueReports:    issueSvc.Enabled(),
	})
	if mcpErr != nil {
		logging.Error(context.Background(), mcpErr, "MCP endpoint disabled", "mcp-disabled")
	}

	// /mcp gets an EXPLICIT chain (outside the group) so the 401s that
	// auth.Middleware writes carry the RFC 9728 WWW-Authenticate pointer —
	// that header on a 401 is how Claude/ChatGPT discover the authorization
	// server. Order: wwwAuth ⟶ auth ⟶ per-token rate limit ⟶ MCP handler.
	if mcpErr == nil {
		r.With(oauthSrv.WWWAuthenticate401, authMW, mcppkg.NewTokenRateLimiter().Middleware).
			Handle("/mcp", mcpServer.Handler())
	}

	// All other API routes require a selected org (tenant-scoped token).
	//
	// Three credential types reach this group: the
	// browser's HS256 JWT (cookie or Bearer), personal access tokens, and OAuth
	// access tokens (which the middleware confines to /mcp). The paths are
	// mutually exclusive — see auth.Middleware.
	r.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Use(attachIdentity(deps)) // identity into the logging.Scope
		// Dev-only role switcher (404s in prod). Not vendor-blocked — a vendor
		// must be able to switch back out. See RoleSwitcher in the header.
		r.Post("/api/auth/dev-role", authHandler.HandleDevSetRole)

		// Single-segment spell check — backs the live underlines in the editor.
		// Any authenticated member; the text comes from the segment they're
		// already editing.
		r.Post("/api/spellcheck", spellcheckHandler.HandleCheck)

		// Invites — managers (owner/admin/PM). The invite service decides which
		// invites each may touch: nobody handles a role above their own, and PMs
		// handle only linguist (vendor) invites.
		r.Route("/api/invites", func(r chi.Router) {
			r.Use(auth.RequireManager())
			r.Post("/", inviteHandler.HandleCreate)
			r.Get("/", inviteHandler.HandleList)
			r.Delete("/{inviteID}", inviteHandler.HandleDelete)
			r.Post("/{inviteID}/resend", inviteHandler.HandleResend)
		})
		// Managers (owner/admin/PM) may LIST members — the assignee pickers
		// (Assign job, workflow-step default assignee) and the Vendors tab all
		// need it. Role changes / (de)activation stay owner/admin.
		r.With(auth.RequireManager()).Get("/api/members", authHandler.HandleListMembers)
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole("owner", "admin"))
			r.Put("/api/members/{userID}/role", handleUpdateMemberRole(deps.Queries))
			// Per-org removal: deletes the membership, not the account.
			r.Delete("/api/members/{userID}", authHandler.HandleRemoveMember)
		})

		// User language pairs. Managers read; replace is owner/admin.
		userpairHandler := userpair.NewHandler(deps.Queries, deps.DB.(*pgxpool.Pool))
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireManager())
			r.Get("/api/users/by-language-pair", userpairHandler.HandleListUsersForPair)
			r.Get("/api/users/{userID}/language-pairs", userpairHandler.HandleList)
		})
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole("owner", "admin"))
			r.Put("/api/users/{userID}/language-pairs", userpairHandler.HandleReplace)
		})

		// Customers. Managers list, create and rename customers;
		// delete is owner-only.
		customerHandler := customer.NewHandler(deps.Queries)
		r.Route("/api/customers", func(r chi.Router) {
			// Managers (owner/admin/PM) see + create + rename customers.
			r.Use(auth.RequireManager())
			r.Get("/", customerHandler.HandleList)
			r.Get("/{customerID}", customerHandler.HandleGet)
			r.Get("/{customerID}/project-count", customerHandler.HandleProjectCount)
			r.Post("/", customerHandler.HandleCreate)
			r.Patch("/{customerID}", customerHandler.HandleUpdate)
			// Delete customer is owner-only.
			r.With(auth.RequireRole("owner")).Delete("/{customerID}", customerHandler.HandleDelete)
		})

		// Project labels: tenant-defined classifiers with a fixed value set.
		// Manager surface (owner/admin/PM) — a PM fills the project form, so a
		// PM adds the label it needs. Deleting a whole label is owner-only,
		// matching customers: it disappears from every project that
		// used it. Retiring one value stays with the managers who add them.
		labelHandler := label.NewHandler(deps.Queries, deps.DB.(*pgxpool.Pool))
		r.Route("/api/labels", func(r chi.Router) {
			r.Use(auth.RequireManager())
			r.Get("/", labelHandler.HandleList)
			r.Post("/", labelHandler.HandleCreate)
			r.Post("/{labelID}/values", labelHandler.HandleCreateValue)
			r.Get("/{labelID}/values/{valueID}/project-count", labelHandler.HandleValueProjectCount)
			// Removing anything is admin-or-above. A manager shapes the
			// vocabulary by adding to it; deleting a value unsets it on every
			// project that picked it, which is past a manager's day-to-day
			// project work.
			r.With(auth.RequireRole("owner", "admin")).Delete("/{labelID}/values/{valueID}", labelHandler.HandleDeleteValue)
			r.With(auth.RequireRole("owner", "admin")).Delete("/{labelID}", labelHandler.HandleDelete)
		})

		// Step definitions + workflow libraries. Manager
		// surface end to end: reads and writes are owner/admin/project_manager.
		stepDefHandler := stepdef.NewHandler(deps.Queries, deps.DB.(*pgxpool.Pool))
		r.Route("/api/step-definitions", func(r chi.Router) {
			r.Use(auth.RequireManager())
			r.Get("/", stepDefHandler.HandleList)
			r.Post("/", stepDefHandler.HandleCreate)
			r.Get("/{stepDefinitionID}", stepDefHandler.HandleGet)
			r.Put("/{stepDefinitionID}", stepDefHandler.HandleUpdate)
			r.Delete("/{stepDefinitionID}", stepDefHandler.HandleDelete)
		})
		workflowLibHandler := workflowlib.NewHandler(deps.Queries, deps.DB.(*pgxpool.Pool))
		r.Route("/api/workflows", func(r chi.Router) {
			r.Use(auth.RequireManager())
			r.Get("/", workflowLibHandler.HandleList)
			r.Post("/", workflowLibHandler.HandleCreate)
			r.Get("/pretranslate-only", workflowLibHandler.HandleGetPretranslate)
			r.Get("/{workflowID}", workflowLibHandler.HandleGet)
			r.Put("/{workflowID}", workflowLibHandler.HandleUpdate)
			r.Delete("/{workflowID}", workflowLibHandler.HandleDelete)
		})

		// Extensions run QE+APE for engines whose key belongs to one; the
		// pipeline below reads them too. Nil when AI secrets or storage are
		// not configured.
		var extSvc *extension.Service

		// AI provider connections. Managers see connection status;
		// connecting/disconnecting (which handles API keys) is owner/admin-only.
		// Keys are verified live, then stored encrypted — never returned to JS.
		if enc, err := secrets.NewEncryptor(deps.AISecretsKey); err != nil {
			logging.Error(context.Background(), err, "AI providers disabled", "ai-providers-disabled")
		} else {
			aiHandler := ai.NewHandler(deps.Queries, enc, ai.NewHTTPTester(), catalog)
			if deps.Storage != nil {
				extSvc = extension.NewService(deps.Queries, deps.Storage, enc, extension.NewRuntime(deps.ExtensionHTTPClient, deps.LLMDebugRecorder))
				aiHandler.WithExtensions(extSvc)
				// Extensions: listing is manager-level like vendors; installing
				// and removing one is owner/admin.
				// The icon is for every member: it shows wherever the provider does.
				extHandler := extension.NewHandler(extSvc)
				r.Route("/api/ai/extensions", func(r chi.Router) {
					r.Get("/{extID}/icon", extHandler.HandleIcon)
					r.With(auth.RequireManager()).Get("/", extHandler.HandleList)
					r.With(auth.RequireRole("owner", "admin")).Post("/", extHandler.HandleUpload)
					r.With(auth.RequireRole("owner", "admin")).Delete("/{extID}", extHandler.HandleDelete)
				})
			}
			r.Route("/api/ai/vendors", func(r chi.Router) {
				r.Use(auth.RequireManager())
				r.Get("/", aiHandler.HandleList)
				r.With(auth.RequireRole("owner", "admin")).Post("/{provider}/tokens", aiHandler.HandleCreateToken)
				r.With(auth.RequireRole("owner", "admin")).Patch("/{provider}/tokens/{tokenID}", aiHandler.HandleUpdateToken)
				r.With(auth.RequireRole("owner", "admin")).Delete("/{provider}/tokens/{tokenID}", aiHandler.HandleDeleteToken)
				// Engines that lost their key, and the engines a key serves:
				// owner/admin, the people who add and delete keys.
				keyAttach := keyattach.NewHandler(deps.Queries, deps.DB.(*pgxpool.Pool), catalog)
				r.With(auth.RequireRole("owner", "admin")).Get("/{provider}/tokens/{tokenID}/keyless-engines", keyAttach.HandleKeyless)
				r.With(auth.RequireRole("owner", "admin")).Post("/{provider}/tokens/{tokenID}/attach", keyAttach.HandleAttach)
				r.With(auth.RequireRole("owner", "admin")).Get("/{provider}/tokens/{tokenID}/engines", keyAttach.HandleUsing)
				// Connections: the endpoint a token points at.
				// Same gating as tokens — reading is manager-level, writing is
				// owner/admin.
				r.Get("/connections", aiHandler.HandleListConnections)
				r.With(auth.RequireRole("owner", "admin")).Post("/{provider}/connections", aiHandler.HandleCreateConnection)
				r.With(auth.RequireRole("owner", "admin")).Patch("/{provider}/connections/{connectionID}", aiHandler.HandleUpdateConnection)
				r.With(auth.RequireRole("owner", "admin")).Delete("/{provider}/connections/{connectionID}", aiHandler.HandleDeleteConnection)
			})
		}

		// Personal access tokens — the credential MCP clients and
		// headless agents present. Any authenticated member may manage their own
		// tokens at their own role; the handlers enforce that managers (and only
		// managers) can list/revoke org-wide. Deliberately NOT manager-gated at
		// the route level: a linguist needs their own token to use the CAT-editor
		// tools from an agent.
		r.Route("/api/api-tokens", func(r chi.Router) {
			r.Get("/", authHandler.HandleListAPITokens)
			r.Post("/", authHandler.HandleCreateAPIToken)
			r.Delete("/{tokenID}", authHandler.HandleRevokeAPIToken)
		})

		// Connected apps: the caller's OAuth grants. The MCP
		// endpoint itself is mounted OUTSIDE this group (see the explicit
		// wwwAuth⟶auth⟶rate-limit chain above), so it isn't here.
		r.Get("/api/oauth/connections", oauthSrv.HandleListConnections)
		r.Delete("/api/oauth/connections/{familyID}", oauthSrv.HandleRevokeConnection)

		// Dashboards: shared and personal
		// dashboards of charts backed by the metrics semantic layer. The whole
		// group is manager-tier; writes and deletes are gated per dashboard in
		// the handler (see below).
		dashboardHandler := dashboard.NewHandler(deps.Queries, deps.DB.(*pgxpool.Pool))
		r.Route("/api/dashboards", func(r chi.Router) {
			r.Use(auth.RequireManager())
			// Each person's own Pinned view: references to charts they can see.
			dashboardHandler.MountPinned(r, "")
			r.Get("/", dashboardHandler.HandleList)
			r.Get("/{dashboardID}", dashboardHandler.HandleGet)
			r.Get("/charts/{chartID}/data", dashboardHandler.HandleChartData)
			r.Get("/{dashboardID}/filter-options", dashboardHandler.HandleFilterOptions)
			r.Get("/metrics-catalog", dashboardHandler.HandleMetricsCatalog)
			r.Post("/", dashboardHandler.HandleCreateDashboard)
			r.Post("/{dashboardID}/charts", dashboardHandler.HandleCreateChart)
			// Writes are NOT gated here: a shared dashboard needs owner/admin,
			// but a personal one belongs to its owner, who may be a project
			// manager. dashboard.mayWrite holds both rules in one place —
			// splitting them across router and handler is how they drift.
			r.Post("/{dashboardID}/duplicate", dashboardHandler.HandleDuplicateDashboard)
			r.Post("/charts/{chartID}/share", dashboardHandler.HandleShareChart)
			r.Delete("/charts/{chartID}/share", dashboardHandler.HandleRevokeChartShare)
			r.Put("/{dashboardID}/name", dashboardHandler.HandleRenameDashboard)
			r.Put("/charts/{chartID}/title", dashboardHandler.HandleRenameChart)
			r.Put("/charts/{chartID}", dashboardHandler.HandleUpdateChart)
			r.Put("/charts/{chartID}/filters", dashboardHandler.HandleUpdateChartControls)
			r.Post("/{dashboardID}/filters/remove", dashboardHandler.HandleRemoveDashboardFilter)
			r.Post("/charts/{chartID}/filters/remove", dashboardHandler.HandleRemoveChartFilter)
			r.Put("/{dashboardID}/filters", dashboardHandler.HandleUpdateFilters)
			r.Put("/{dashboardID}/visibility", dashboardHandler.HandleSetVisibility)
			r.Put("/charts/{chartID}/dashboard", dashboardHandler.HandleMoveChart)
			r.Post("/charts/{chartID}/copy", dashboardHandler.HandleCopyChart)
			r.Put("/{dashboardID}/chart-order", dashboardHandler.HandleReorderCharts)
			r.Put("/order", dashboardHandler.HandleReorderDashboards)
			r.Get("/shared-count", dashboardHandler.HandleSharedCount)
			r.Post("/reset-defaults", dashboardHandler.HandleResetDefaults)
			r.Delete("/{dashboardID}", dashboardHandler.HandleDeleteDashboard)
			r.Delete("/charts/{chartID}", dashboardHandler.HandleDeleteChart)
		})

		// Chart share links. Deliberately OUTSIDE the
		// manager-tier group above: the token IS the authorization, so a
		// linguist who was sent one may open that chart even though they cannot
		// browse dashboards. Still inside the authed group — a share link is for
		// signed-in colleagues, not the public — and the query is tenant-scoped,
		// so a leaked link shows another organisation nothing.
		r.Get("/api/shared/charts/{token}", dashboardHandler.HandleGetSharedChart)
		r.Get("/api/shared/charts/{token}/data", dashboardHandler.HandleSharedChartData)

		// Built-in chat: agent loop over the tenant's Claude key with
		// tools served by the SAME MCP server over an in-process transport —
		// one tool surface for Desktop and chat. Requires both the MCP server
		// and a working secrets key; degrades to route-off like MCP itself.
		if mcpErr != nil {
			logging.Warn(context.Background(), nil, "built-in chat disabled: MCP server unavailable", "chat-disabled-no-mcp")
		} else if chatEnc, err := secrets.NewEncryptor(deps.AISecretsKey); err != nil {
			logging.Error(context.Background(), err, "built-in chat disabled", "chat-disabled")
		} else {
			chatHandler := chat.NewHandler(deps.Queries, chatEnc, mcpServer, deps.Draining, catalog)
			chatHandler.SetStorage(deps.Storage)
			chatHandler.SetPlayground(deps.DemoOrg)
			chatHandler.SetIssueReports(issueSvc.Enabled())
			r.Route("/api/chat", func(r chi.Router) {
				r.Post("/", chatHandler.HandleSend)
				// Runs one guarded tool call the user approved on a confirm card.
				r.Post("/confirm", chatHandler.HandleConfirm)
				// The user sent the assistant's issue report from the form; the
				// conversation records it (any member, their own conversation).
				r.Post("/issue-reported", chatHandler.HandleIssueReported)
				// A file attached in the panel, for the import tools (any member).
				r.Post("/uploads", chatHandler.HandleUpload)
				// The caller's own upload, when it is an image: a screenshot the
				// report form shows and attaches (any member, their own uploads).
				r.Get("/uploads/{uploadID}", chatHandler.HandleUploadImage)
				// Playbooks for the caller's role; saved tours run without the model.
				r.Get("/playbooks", chatHandler.HandleListPlaybooks)
				// Tour cards read aloud on the org's OpenAI key (any member).
				r.Get("/speech", chatHandler.HandleSpeechStatus)
				r.Post("/speech", chatHandler.HandleSpeech)
				r.Get("/models", chatHandler.HandleListModels)
				// Prompt-card suggestions read dashboard definitions — manager data.
				r.With(auth.RequireManager()).Get("/suggest-chart", chatHandler.HandleSuggestChart)
				r.Get("/conversations", chatHandler.HandleListConversations)
				r.Get("/conversations/{id}/messages", chatHandler.HandleGetMessages)
				r.Delete("/conversations/{id}", chatHandler.HandleDeleteConversation)
			})
		}

		// Owner-only: close the organization; its data is deleted in the background.
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole("owner"))
			r.Delete("/api/tenants/current", authHandler.HandleDeleteTenant)
		})

		projectHandler := project.NewHandler(deps.Queries, deps.DB.(*pgxpool.Pool))
		// A workflow edit that appends a hybrid/ai step to a
		// job whose steps had all finished starts it — run its QE+APE then.
		if aiEnq, ok := deps.Enqueuer.(workflow.AIStepEnqueuer); ok {
			projectHandler.SetAIStepEnqueuer(aiEnq)
		}
		// A deleted project is purged on the worker; without a queue, in the request.
		if q, ok := deps.Enqueuer.(projectdelete.TaskEnqueuer); ok {
			projectHandler.SetDeleteQueue(q)
		}
		projectEventsHandler := projectevents.NewHandler(deps.Queries)
		segmentHandler := segment.NewHandler(deps.Queries)
		pipelineSvc := pipeline.NewService(deps.Queries, pipeline.NewPgxTxRunner(deps.DB.(*pgxpool.Pool)), deps.Storage)
		// AI-as-MT: let the pipeline resolve AI engines of type 'mt'
		// into live provider calls. Without a valid secrets key, AI engines
		// simply can't be used as MT (PhaseSkipNoProvider); classic MT is
		// unaffected.
		// Shared with the worker's wiring in cmd/server/main.go — see
		// Service.ConfigureAI for why this is one function and not two copies.
		// It also wires the recorder independently of the secrets key.
		// Paired with the "[worker]" line in cmd/server/main.go: together they
		// say whether capture is wired on the synchronous path, the automatic
		// one, or both.
		logging.Info(context.Background(), "pipeline LLM debug capture", "pipeline-llm-debug-capture", logging.Label("enabled", strconv.FormatBool(deps.LLMDebugRecorder != nil)))
		if _, err := pipelineSvc.ConfigureAI(deps.AISecretsKey, deps.LLMDebugRecorder); err != nil {
			logging.Error(context.Background(), err, "AI-as-MT disabled", "ai-as-mt-disabled")
		}
		if extSvc != nil {
			pipelineSvc.SetExtensions(extSvc)
		}
		// Signs and verifies the provenance of Resources-panel suggestions:
		// the TM-match and MT-preview endpoints sign, the draft save verifies.
		suggestions := suggestion.NewSigner([]byte(deps.JWTSecret))
		translationHandler := translation.NewHandler(deps.Queries, deps.DB.(*pgxpool.Pool), pipelineSvc, qa.NewService(deps.Queries))
		translationHandler.SetSuggestionSigner(suggestions)
		holdHandler := hold.NewHandler(hold.NewService(deps.Queries, deps.DB.(*pgxpool.Pool)))
		pipelineHandler := pipeline.NewHandler(deps.Queries, pipelineSvc)
		fileHandler := file.NewHandler(deps.Queries, deps.DB.(*pgxpool.Pool), deps.Storage, deps.Enqueuer)
		// The production enqueuer (*worker.Enqueuer) also enqueues
		// QE+APE predict-on-start; a nil / non-async enqueuer skips it.
		if aiEnq, ok := deps.Enqueuer.(file.AIStepEnqueuer); ok {
			fileHandler.SetAIStepEnqueuer(aiEnq)
		}
		// The production enqueuer (*worker.Enqueuer) also dispatches
		// auto-analysis-on-upload; a nil / non-async enqueuer skips it.
		if anEnq, ok := deps.Enqueuer.(analysis.Enqueuer); ok {
			fileHandler.SetAnalysisEnqueuer(anEnq)
		}
		// Publishing a draft turns its uploads into jobs.
		projectHandler.SetFileMaterializer(fileHandler)

		r.Route("/api/projects", func(r chi.Router) {
			// Linguists never reach the project-management surface — they work
			// only through /api/tasks + the job/file groups. Managers
			// (owner/admin/PM) get everything here EXCEPT delete.
			r.Use(auth.RequireManager())
			r.Get("/", projectHandler.HandleList)
			r.Post("/", projectHandler.HandleCreate)
			// Static segment — registered before /{projectID} so the intent is
			// obvious at a glance (chi's trie prefers static over param either
			// way; same shape as /api/tms/concordance).
			r.Get("/creators", projectHandler.HandleListCreators)
			r.Get("/{projectID}", projectHandler.HandleGet)
			// The New Project wizard's autosave + its "Create project" click.
			// Manager-level like POST /.
			r.Put("/{projectID}/draft", projectHandler.HandleSaveDraft)
			r.Post("/{projectID}/publish", projectHandler.HandlePublish)
			r.Get("/{projectID}/settings", projectHandler.HandleGetSettings)
			// Manager-level: rename/status/customer, project config, clear.
			r.Patch("/{projectID}", projectHandler.HandleUpdateMetadata)
			r.Patch("/{projectID}/settings", projectHandler.HandleUpdateSettings)
			r.Put("/{projectID}/tms", projectHandler.HandleSetTMAssignments)
			r.Put("/{projectID}/terminology", projectHandler.HandleSetTermAssignments)
			r.Put("/{projectID}/workflow-steps", projectHandler.HandleSetWorkflowSteps)
			// Label picks live on their own routes rather than inside the
			// project payload: the project list would otherwise pay for a
			// join per row to render a page that does not show them.
			r.Get("/{projectID}/labels", labelHandler.HandleListProjectLabels)
			r.Put("/{projectID}/labels", labelHandler.HandleSetProjectLabels)
			r.Post("/{projectID}/workflow/apply", projectHandler.HandleApplyWorkflow)
			r.Post("/{projectID}/workflow/save-as", projectHandler.HandleSaveWorkflowAs)
			r.Post("/{projectID}/workflow/detach", projectHandler.HandleDetachWorkflow)
			// Templates are create-time-only (pass template_id to POST
			// /api/projects); a template is made from the New project form
			// (POST /api/templates carries the same setup payload).
			// TM profile link: link via PATCH /settings (tm_profile_id); these are
			// the other two directions.
			r.Post("/{projectID}/tms/detach", projectHandler.HandleDetachTMProfile)
			r.Post("/{projectID}/save-tm-profile", projectHandler.HandleSaveTMProfile)
			r.Post("/{projectID}/clear-translations", projectHandler.HandleClearTranslations)
			// Project history / audit log. Manager-level
			// like the rest of the group.
			r.Get("/{projectID}/events", projectEventsHandler.HandleList)

			// Per-project spell check dictionary — the persistence behind
			// "Ignore all". Inherits this group's RequireManager, so adding a
			// word is owner/admin/PM; removing one re-opens findings for the
			// whole project, so it's owner/admin only.
			r.Route("/{projectID}/spellcheck-dictionary", func(r chi.Router) {
				r.Get("/", spellcheckHandler.HandleListDictionary)
				r.Post("/", spellcheckHandler.HandleAddDictionaryWord)
				r.With(auth.RequireRole("owner", "admin")).
					Delete("/{wordID}", spellcheckHandler.HandleDeleteDictionaryWord)
			})
			// Delete project is owner/admin only (PM cannot delete),
			// except a draft, which the manager filling the wizard in may
			// throw away with Discard. The route
			// group is manager; the rest of the gate is in the handler, the
			// same shape as deleting a file from a draft.
			r.Delete("/{projectID}", projectHandler.HandleDelete)

			// Batch download → one ZIP. Single-item
			// downloads deliberately go through the job/file routes below so
			// linguists can reach them from the CAT editor.
			r.Post("/{projectID}/download", pipelineHandler.HandleProjectDownload)

			r.Route("/{projectID}/files", func(r chi.Router) {
				r.Get("/", fileHandler.HandleList)
				r.Post("/", fileHandler.HandleUpload)
				// The column layout of an Excel workbook without language
				// codes: read it before the upload, change it after.
				r.Post("/inspect", fileHandler.HandleInspectXLSX)
				r.Get("/{fileID}/xlsx-layout", fileHandler.HandleGetXLSXLayout)
				r.Put("/{fileID}/xlsx-layout", fileHandler.HandleSetXLSXLayout)
				r.Get("/{fileID}", fileHandler.HandleGet)
				r.Patch("/{fileID}", fileHandler.HandleRename)
				// Owner/admin, plus any manager while the project is a draft —
				// the handler checks the project.
				r.Delete("/{fileID}", fileHandler.HandleDelete)
			})
		})

		tmHandler := translationmemory.NewHandler(deps.Queries, deps.DB, deps.DB.(*pgxpool.Pool))
		tmHandler.SetSuggestionSigner(suggestions)
		r.Get("/api/tms/concordance", tmHandler.HandleConcordanceSearch)
		// /api/jobs/{jobID}/tm-matches is mounted inside the RequireJobOwnership
		// route group below — see r.Route("/api/jobs/{jobID}", ...).
		r.Route("/api/tms", func(r chi.Router) {
			// Concordance (/api/tms/concordance) is registered separately above
			// and stays open to vendors as translator tooling; TM management does not.
			r.Use(auth.RequireManager())
			r.Get("/", tmHandler.HandleListCollections)
			r.Post("/", tmHandler.HandleCreateCollection)

			// All {tmID} routes verify TM belongs to current tenant
			r.Route("/{tmID}", func(r chi.Router) {
				r.Use(auth.RequireTMOwnership(deps.Queries))
				r.Get("/", tmHandler.HandleGetCollection)
				r.Patch("/", tmHandler.HandleRenameCollection)
				r.Get("/entries", tmHandler.HandleListEntries)
				r.Get("/entries/{entryID}/history", tmHandler.HandleListEntryHistory)
				r.Get("/language-pairs", tmHandler.HandleListLanguagePairs)
				r.Post("/import", tmHandler.HandleImportTMX)
				r.Get("/imports", tmHandler.HandleListImports)
				r.Get("/export", tmHandler.HandleExportTMX)
				// Manager-level TM content edits (import, find-replace and edit
				// are PM-allowed). Inherit the parent RequireManager.
				r.Delete("/entries/{entryID}", tmHandler.HandleDeleteEntry)
				r.Patch("/entries/{entryID}", tmHandler.HandleUpdateEntry)
				r.Post("/find-replace", tmHandler.HandleTMFindReplace)
				// Delete the whole TM collection is owner/admin only (PM cannot).
				r.With(auth.RequireRole("owner", "admin")).Delete("/", tmHandler.HandleDeleteCollection)
			})
		})

		// TM profiles: a reusable bundle of TMs + match scoring that projects and
		// templates link to live. Managers manage them like TMs; deleting one is
		// owner/admin only, and refused while any project or template follows it.
		tmProfileHandler := tmprofile.NewHandler(deps.Queries, deps.DB.(*pgxpool.Pool))
		r.Route("/api/tm-profiles", func(r chi.Router) {
			r.Use(auth.RequireManager())
			r.Get("/", tmProfileHandler.HandleList)
			r.Post("/", tmProfileHandler.HandleCreate)
			r.Get("/{profileID}", tmProfileHandler.HandleGet)
			r.Patch("/{profileID}", tmProfileHandler.HandleUpdate)
			r.Put("/{profileID}/tms", tmProfileHandler.HandleSetTMAssignments)
			r.With(auth.RequireRole("owner", "admin")).Delete("/{profileID}", tmProfileHandler.HandleDelete)
		})

		// AI prompts + AI engines. Managers read; owner/admin write.
		aiPromptHandler := aiprompt.NewHandler(deps.Queries, deps.DB.(*pgxpool.Pool))
		r.Route("/api/ai/prompts", func(r chi.Router) {
			r.Use(auth.RequireManager())
			r.Get("/", aiPromptHandler.HandleList)
			// Assembles a draft prompt for display. Manager-read despite the
			// POST — it writes nothing and reads no tenant data.
			r.Post("/preview", aiPromptHandler.HandlePreview)
			r.With(auth.RequireRole("owner", "admin")).Post("/", aiPromptHandler.HandleCreate)
			r.With(auth.RequireRole("owner", "admin")).Patch("/{promptID}", aiPromptHandler.HandleUpdate)
			r.With(auth.RequireRole("owner", "admin")).Delete("/{promptID}", aiPromptHandler.HandleDelete)
		})

		aiEngineHandler := aiengine.NewHandler(deps.Queries, deps.DB.(*pgxpool.Pool), catalog)
		r.Route("/api/ai/engines", func(r chi.Router) {
			r.Use(auth.RequireManager())
			r.Get("/", aiEngineHandler.HandleList)
			r.With(auth.RequireRole("owner", "admin")).Post("/", aiEngineHandler.HandleCreate)
			r.With(auth.RequireRole("owner", "admin")).Patch("/{engineID}", aiEngineHandler.HandleUpdate)
			r.With(auth.RequireRole("owner", "admin")).Delete("/{engineID}", aiEngineHandler.HandleDelete)
		})

		// AI routers: target language → QE+APE engine maps
		// that workflow steps point at. Same posture as engines: manager-read,
		// owner/admin-write.
		aiRouterHandler := airouter.NewHandler(deps.Queries, deps.DB.(*pgxpool.Pool))
		r.Route("/api/ai/routers", func(r chi.Router) {
			r.Use(auth.RequireManager())
			r.Get("/", aiRouterHandler.HandleList)
			r.Get("/{routerID}", aiRouterHandler.HandleGet)
			r.With(auth.RequireRole("owner", "admin")).Post("/", aiRouterHandler.HandleCreate)
			r.With(auth.RequireRole("owner", "admin")).Put("/{routerID}", aiRouterHandler.HandleUpdate)
			r.With(auth.RequireRole("owner", "admin")).Delete("/{routerID}", aiRouterHandler.HandleDelete)
		})

		// MT routers: target language → MT engine maps that settings profiles
		// point at. Same posture as MT engines: every op is manager-level.
		mtRouterHandler := mtrouter.NewHandler(deps.Queries, deps.DB.(*pgxpool.Pool), mtDecryptor)
		r.Route("/api/mt-routers", func(r chi.Router) {
			r.Use(auth.RequireManager())
			r.Get("/", mtRouterHandler.HandleList)
			r.Post("/", mtRouterHandler.HandleCreate)
			r.Get("/{routerID}", mtRouterHandler.HandleGet)
			r.Put("/{routerID}", mtRouterHandler.HandleUpdate)
			r.Delete("/{routerID}", mtRouterHandler.HandleDelete)
		})

		mtEngineHandler := mtengine.NewHandler(deps.Queries, mtDecryptor)
		r.Route("/api/mt-engines", func(r chi.Router) {
			// All MT-engine ops (incl. delete) are PM-allowed.
			r.Use(auth.RequireManager())
			r.Get("/{engineID}/languages", mtEngineHandler.HandleLanguages)
			// Before /{engineID} would shadow it.
			r.Get("/catalog", mtEngineHandler.HandleCatalog)
			r.Get("/", mtEngineHandler.HandleList)
			r.Post("/", mtEngineHandler.HandleCreate)
			r.Patch("/{engineID}", mtEngineHandler.HandleUpdate)
			r.Delete("/{engineID}", mtEngineHandler.HandleDelete)
		})

		templateHandler := tmpl.NewHandler(deps.Queries, deps.DB.(*pgxpool.Pool))
		r.Route("/api/templates", func(r chi.Router) {
			r.Use(auth.RequireManager())
			r.Get("/", templateHandler.HandleList)
			r.Post("/", templateHandler.HandleCreate)
			r.Get("/{templateID}", templateHandler.HandleGet)
			r.Patch("/{templateID}", templateHandler.HandleUpdate)
			// A template presets a project's labels, so it carries picks the
			// same way a project does.
			r.Get("/{templateID}/labels", labelHandler.HandleListTemplateLabels)
			r.Put("/{templateID}/labels", labelHandler.HandleSetTemplateLabels)
			r.Post("/{templateID}/duplicate", templateHandler.HandleDuplicate)
			r.Put("/{templateID}/tms", templateHandler.HandleSetTMAssignments)
			r.Post("/{templateID}/tms/detach", templateHandler.HandleDetachTMProfile)
			r.Post("/{templateID}/save-tm-profile", templateHandler.HandleSaveTMProfile)
			r.Put("/{templateID}/terminology", templateHandler.HandleSetTermAssignments)
			r.Get("/{templateID}/workflow-steps", templateHandler.HandleListSteps)
			r.Put("/{templateID}/workflow-steps", templateHandler.HandleSetSteps)
			r.Post("/{templateID}/workflow/apply", templateHandler.HandleApplyWorkflow)
			r.Post("/{templateID}/workflow/save-as", templateHandler.HandleSaveWorkflowAs)
			r.Post("/{templateID}/workflow/detach", templateHandler.HandleDetachWorkflow)
			// Template delete is PM-allowed.
			r.Delete("/{templateID}", templateHandler.HandleDelete)
		})

		termHandler := terminology.NewHandler(deps.Queries, deps.DB.(*pgxpool.Pool))
		// Term lookup is translator tooling (the CAT editor's terminology
		// sidebar) — open to vendors. Registered outside the management route
		// below so the vendor block there doesn't cut it off.
		r.Get("/api/terminology/lookup", termHandler.HandleLookup)
		r.Route("/api/terminology", func(r chi.Router) {
			r.Use(auth.RequireManager())
			r.Get("/", termHandler.HandleListBases)
			r.Post("/", termHandler.HandleCreateBase)
			r.Get("/{baseID}", termHandler.HandleGetBase)
			r.Patch("/{baseID}", termHandler.HandleUpdateBase)
			// Concept-centric CRUD (Phase 4 concept editor)
			r.Get("/{baseID}/concepts", termHandler.HandleListConcepts)
			r.Post("/{baseID}/concepts", termHandler.HandleCreateConcept)
			r.Get("/{baseID}/concepts/{conceptID}", termHandler.HandleGetConcept)
			r.Patch("/{baseID}/concepts/{conceptID}", termHandler.HandleUpdateConcept)
			r.Delete("/{baseID}/concepts/{conceptID}", termHandler.HandleDeleteConcept)
			r.Post("/{baseID}/concepts/{conceptID}/terms", termHandler.HandleAddTerm)
			r.Patch("/{baseID}/concepts/{conceptID}/terms/{termID}", termHandler.HandleUpdateTerm)
			r.Delete("/{baseID}/concepts/{conceptID}/terms/{termID}", termHandler.HandleDeleteTerm)
			r.Post("/{baseID}/import", termHandler.HandleImportCSV)
			r.Get("/{baseID}/export", termHandler.HandleExportCSV)
			r.Get("/{baseID}/languages", termHandler.HandleListBaseLanguages)
			r.Post("/{baseID}/import-tbx", termHandler.HandleImportTBX)
			r.Get("/{baseID}/export-tbx", termHandler.HandleExportTBX)
			// Admin-only: term base deletion
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole("owner", "admin"))
				r.Delete("/{baseID}", termHandler.HandleDeleteBase)
			})
		})

		// Comments per translation (translationID-scoped; not under /api/files).
		r.Route("/api/translations/{translationID}/comments", func(r chi.Router) {
			// Vendors may only see/add comments on their assigned work.
			r.Use(auth.RequireLinguistTranslationAccess(deps.Queries))
			r.Get("/", commentHandler.HandleList)
			r.Post("/", commentHandler.HandleCreate)
			r.Delete("/{commentID}", commentHandler.HandleDelete)
		})

		// All /api/files/{fileID}/* routes are gated by RequireFileOwnership at
		// the route-group level. Pairs with inline
		// GetFileTenant / GetSegmentTenant / GetTranslationTenant in handlers
		// (defense-in-depth, matches the RequireTMOwnership + inline-check
		// convention in the TM surface).
		r.Route("/api/files/{fileID}", func(r chi.Router) {
			r.Use(auth.RequireFileOwnership(deps.Queries))
			// Vendors may only touch files they're assigned to. No-op for every
			// other role. It passes on any job of the file, so the handlers of
			// translations, export, propagate and comment counts also require
			// the job of the language they touch.
			r.Use(auth.RequireLinguistFileAccess(deps.Queries))

			r.Route("/segments", func(r chi.Router) {
				r.Get("/", segmentHandler.HandleList)
				r.Get("/{segmentID}", segmentHandler.HandleGet)
			})

			r.Route("/translations", func(r chi.Router) {
				r.Get("/", translationHandler.HandleListByFile)
				// Bulk draft save. Registered before the /{translationID}
				// patterns so chi doesn't route "batch-draft" as an id.
				r.Put("/batch-draft", translationHandler.HandleBatchDraft)
				r.Get("/{translationID}", translationHandler.HandleGet)
				r.Put("/{translationID}", translationHandler.HandleUpdateHuman)
				r.Put("/{translationID}/draft", translationHandler.HandleSaveDraft)
				// Locking approves the segment and makes it read-only, so it is a
				// manager's decision. Unlocking is open to anyone who may edit the
				// file: a translator unlocks a segment to change it.
				// Tenant isolation is enforced via the file-level middleware AND inline
				// GetTranslationInFileTenant — the role gate is the *additional* defense.
				r.With(auth.RequireManager()).Post("/{translationID}/lock", translationHandler.HandleLock)
				r.Post("/{translationID}/unlock", translationHandler.HandleUnlock)
				// Clear a segment's translation is an editor action — allowed for
				// everyone with access to the file (managers + assigned linguists).
				// File-level access is already
				// enforced by RequireFileOwnership + RequireLinguistFileAccess above.
				r.Post("/{translationID}/clear", translationHandler.HandleClear)
			})

			// Comment counts per file (batch — avoids N+1)
			r.Get("/comment-counts", commentHandler.HandleCountByFile)
			r.Post("/propagate", translationHandler.HandlePropagate)

			// XLIFF import (round-trip after external editing) — every role.
			// A linguist must hold the task of the imported language; the
			// handler checks that, since the file-level gate above passes on
			// any of the file's tasks.
			r.Post("/reimport-xliff", fileHandler.HandleReimportXLIFF)

			r.Get("/export", pipelineHandler.HandleExportFile)
			// The untouched upload. Mounted here, not on
			// the manager-gated project group, so linguists/vendors can pull
			// the source document from the CAT editor's Download menu.
			r.Get("/download", fileHandler.HandleDownloadSource)
		})

		// QA
		qaHandler := qa.NewHandler(deps.Queries, deps.DB.(*pgxpool.Pool))
		r.Route("/api/qa-profiles", func(r chi.Router) {
			r.Use(auth.RequireManager())
			r.Get("/", qaHandler.HandleListProfiles)
			r.Post("/", qaHandler.HandleCreateProfile)
			r.Get("/{profileID}", qaHandler.HandleGetProfile)
			r.Patch("/{profileID}", qaHandler.HandleUpdateProfile)
			r.Delete("/{profileID}", qaHandler.HandleDeleteProfile)
			r.Get("/{profileID}/rules", qaHandler.HandleListRules)
			r.Post("/{profileID}/rules", qaHandler.HandleUpsertRule)
		})
		r.With(auth.RequireLinguistTranslationAccess(deps.Queries)).
			Get("/api/translations/{translationID}/qa", qaHandler.HandleTranslationQA)
		// POST re-runs + persists QA for the single edited
		// segment — the CAT editor calls it after autosave so badges stay fresh.
		r.With(auth.RequireLinguistTranslationAccess(deps.Queries)).
			Post("/api/translations/{translationID}/qa", qaHandler.HandleRunTranslationQA)
		// Ignore / un-ignore one finding. Editor action (linguist=Yes when
		// assigned); the flag flips on the stored row, nothing is recomputed.
		r.With(auth.RequireLinguistTranslationAccess(deps.Queries)).
			Post("/api/translations/{translationID}/qa/{checkType}/ignore", qaHandler.HandleIgnoreQAFinding)
		r.With(auth.RequireLinguistTranslationAccess(deps.Queries)).
			Delete("/api/translations/{translationID}/qa/{checkType}/ignore", qaHandler.HandleUnignoreQAFinding)

		// Segment history — every recorded change to one segment, newest first.
		// Read-only; same access as comments + QA (managers and owners/admins
		// anywhere, linguists on their assigned segments only).
		r.With(auth.RequireLinguistTranslationAccess(deps.Queries)).
			Get("/api/translations/{translationID}/history", translationHandler.HandleHistory)
		// How each finished workflow step left one segment. Same access as
		// history.
		r.With(auth.RequireLinguistTranslationAccess(deps.Queries)).
			Get("/api/translations/{translationID}/step-statuses", translationHandler.HandleStepStatuses)

		// Languages
		langHandler := language.NewHandler(deps.Queries)
		r.Get("/api/languages", langHandler.HandleList)

		// Jobs
		jobHandler := job.NewHandler(deps.Queries, deps.DB.(*pgxpool.Pool), pipelineSvc, deps.Enqueuer)
		jobHandler.SetSuggestionSigner(suggestions)
		if deps.TaskCanceller != nil {
			jobHandler.SetCanceller(deps.TaskCanceller)
		}
		if deps.NotificationService != nil {
			jobHandler.SetFailureNotifier(deps.NotificationService)
		}
		// Synchronous pipeline runs are detached from their request context;
		// draining is what still cancels them on shutdown.
		jobHandler.SetDraining(deps.Draining)
		// The MCP project and file tools run the same services as these routes.
		mcppkg.WireProjectOps(mcpBackend, mcppkg.ProjectOpsServices{
			Projects: projectHandler, Files: fileHandler, Jobs: jobHandler, Exports: pipelineSvc,
		})
		// Tasks — jobs assigned on each job's current step, plus the step names
		// for the page's Step filter. Open to every authenticated role (this is
		// the vendor's home page). Owner/admin see every assignee's tasks,
		// everyone else only their own; the handler scopes it, and the query is
		// tenant-scoped, so it never leaks other tenants' work.
		r.Get("/api/tasks", jobHandler.HandleListMyTasks)
		r.Get("/api/tasks/steps", jobHandler.HandleListTaskSteps)
		// analysisHandler is declared here (outer scope) because the job group
		// below also uses it (HandleJobLeverage). deps.Enqueuer satisfies
		// analysis.Enqueuer; when nil, HandleCreate inserts the row with no
		// worker dispatched (UI shows 'pending' — the honest state).
		analysisHandler := analysis.NewHandler(deps.Queries, deps.Enqueuer)
		timingHandler := timing.NewHandler(deps.Queries)

		// Project jobs-list + analysis + saved-analyses + pretranslate surface.
		// All blocked for vendors — they reach only their own jobs via
		// /api/tasks and the job group. Pre-translate makes paid MT calls; the
		// billing TODO (RequireRole for MT quota) still stands for members.
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireManager())
			r.Get("/api/projects/{projectID}/jobs", jobHandler.HandleListByProject)
			r.Get("/api/projects/{projectID}/analysis", jobHandler.HandleProjectAnalysis)
			r.Get("/api/projects/{projectID}/analysis/v2", jobHandler.HandleProjectAnalysisV2)
			r.Get("/api/projects/{projectID}/analysis.csv", jobHandler.HandleProjectAnalysisCSVExport)
			r.Post("/api/projects/{projectID}/analyses", analysisHandler.HandleCreate)
			r.Get("/api/projects/{projectID}/analyses", analysisHandler.HandleList)
			r.Get("/api/analyses/{analysisID}", analysisHandler.HandleGet)
			r.Get("/api/analyses/{analysisID}/export", analysisHandler.HandleExport)
			r.Delete("/api/analyses/{analysisID}", analysisHandler.HandleDelete)
			r.Post("/api/projects/{projectID}/pretranslate", jobHandler.HandleProjectPretranslate)
			// Processing-time panel. Manager+ like the
			// sibling analysis/productivity reads: it exposes per-engine and
			// per-model latency, which is operational data rather than
			// something a linguist needs while translating.
			r.Get("/api/projects/{projectID}/timings", timingHandler.HandleProjectTimings)
		})
		// Workflow. Notifications go out on a detached goroutine (see
		// workflow.AsyncNotifier) so a slow sender never stalls the request.
		var onWorkflowEvent workflow.OnEventFunc
		if deps.NotificationService != nil {
			onWorkflowEvent = workflow.AsyncNotifier(deps.NotificationService.Send)
		}
		workflowHandler := workflow.NewHandler(deps.Queries, pipeline.NewPgxTxRunner(deps.DB.(*pgxpool.Pool)), onWorkflowEvent)
		// Predict-on-start for QE+APE workflow steps. A nil enqueuer skips
		// predict.
		var wfAIEnq workflow.AIStepEnqueuer
		if e, ok := deps.Enqueuer.(workflow.AIStepEnqueuer); ok {
			wfAIEnq = e
		}
		workflowHandler.WithAIStep(wfAIEnq)
		jobHandler.SetWorkflow(workflowHandler.Service())
		// Every step assignment of every job in a project, for the project
		// page's task matrix. Manager-only, like the project jobs list.
		r.With(auth.RequireManager()).Get("/api/projects/{projectID}/tasks", workflowHandler.HandleListProjectTasks)

		// The MCP job/segment tools run on these same service instances, so a
		// tool and its REST route share wiring as well as code.
		mcppkg.WireJobOps(mcpBackend, mcppkg.JobOpsServices{
			Jobs:         jobHandler.Service,
			Workflow:     workflowHandler.Service(),
			Translations: translationHandler.Service,
			Comments:     comments.NewService(deps.Queries),
			TMMatches:    tmHandler,
		})
		// All /api/jobs/{jobID}/* routes are gated by RequireJobOwnership at the
		// route-group level — pairs with
		// inline GetJobTenant in the workflow + QA handlers (defense-in-depth,
		// matches the RequireTMOwnership + inline-check convention).
		r.Route("/api/jobs/{jobID}", func(r chi.Router) {
			r.Use(auth.RequireJobOwnership(deps.Queries))
			// Linguists may only touch jobs assigned to them. No-op for every
			// other role (their tenant scope from RequireJobOwnership applies).
			r.Use(auth.RequireLinguistJobAccess(deps.Queries))

			r.Get("/", jobHandler.HandleGet)
			r.Get("/translations", jobHandler.HandleTranslations)
			// Segment holds: who has a segment open in the editor. Any member
			// the group admits; taking one also needs an editable task.
			r.Get("/holds", holdHandler.HandleList)
			r.Put("/holds/{translationID}", holdHandler.HandleAcquire)
			r.Delete("/holds/{translationID}", holdHandler.HandleRelease)
			if deps.HoldFeed != nil {
				r.Get("/holds/stream", hold.NewStreamHandler(deps.HoldFeed, deps.Draining).HandleStream)
			}
			// Each segment's previous workflow step, for the editor.
			r.Get("/step-statuses", jobHandler.HandleStepStatuses)
			r.Get("/analysis", jobHandler.HandleAnalysis)
			// Live job-level leverage for the CAT-editor status bar. Kept inside
			// the job group so the assignment gate covers it.
			r.Get("/leverage", analysisHandler.HandleJobLeverage)
			r.Get("/tm-matches", tmHandler.HandleTMMatches)
			r.Get("/mt-preview", jobHandler.HandleMTPreview)
			// Apply TM/MT + Clear are editor actions — linguist=Yes.
			r.Post("/tm-apply", jobHandler.HandleTMApply)
			r.Post("/mt-apply", jobHandler.HandleMTApply)
			// Stop a running pre-translation / QE+APE run. Same gate as Apply
			// TM/MT (managers + assigned linguist): stopping is not destructive
			// — saved segments are kept and Apply resumes from there.
			r.Post("/stop", jobHandler.HandleStopJob)
			// Re-run the automation an errored task failed in. The handler
			// gates the AI step to managers; pre-translation has Apply TM/MT's
			// gate (managers + assigned linguist).
			r.Post("/rerun", jobHandler.HandleRerun)
			// Skip a failed AI run on a hybrid step. Manager-only, gated in
			// the handler like the AI step's re-run.
			r.Post("/skip-ai-step", jobHandler.HandleSkipAIStep)
			r.Post("/clear-translations", jobHandler.HandleClearTranslations)
			// Bulk actions on the editor's segment selection, with the
			// single-segment routes' gates: lock is managers only, unlock any
			// member with access to the job (clear: first step only, checked in
			// the service).
			r.With(auth.RequireManager()).Post("/translations/lock", translationHandler.HandleLockSegments)
			r.Post("/translations/unlock", translationHandler.HandleUnlockSegments)
			r.Post("/translations/clear", jobHandler.HandleClearSegments)
			// Confirm has the single confirm's gate: any member with job access.
			r.Post("/translations/confirm", translationHandler.HandleConfirmSegments)
			r.Post("/translations/confirm-all", translationHandler.HandleConfirmAll)
			r.Get("/export", jobHandler.HandleExport)
			r.Post("/complete", jobHandler.HandleComplete)
			r.Post("/find-replace", translationHandler.HandleFindReplace)
			r.Post("/run-qa", qaHandler.HandleRunQA)
			r.Get("/qa-report", qaHandler.HandleQAReport)
			// Document-wide spell check. Editor action (linguist=Yes) — the
			// client sends the translation ids its chosen scope resolved to.
			r.Post("/spellcheck", spellcheckHandler.HandleCheckJob)
			// Manager-only (owner/admin/PM): delete job, assign, re-run AI step.
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireManager())
				r.Delete("/", jobHandler.HandleDeleteJob)
				r.Post("/assign", jobHandler.HandleAssignJob)
				// Re-run the active QE+APE step.
				r.Post("/ai-step", jobHandler.HandleAIStep)
			})

			// Workflow step operations. Advance (Complete) + reject are gated
			// inside the handlers (assigned user OR sufficient role); step
			// (re)assignment is manager-only.
			r.Route("/workflow", func(r chi.Router) {
				r.Get("/steps", workflowHandler.HandleListJobSteps)
				r.Post("/advance", workflowHandler.HandleAdvanceStep)
				r.Post("/reject", workflowHandler.HandleRejectStep)
				r.With(auth.RequireManager()).Post("/steps/{stepID}/assign", workflowHandler.HandleAssignStep)
				// Deadline lives on the assignment. Manager-only.
				r.With(auth.RequireManager()).Patch("/steps/{stepID}/deadline", workflowHandler.HandleSetStepDeadline)
			})
		})

		// Customer API. Same credential and the same gates as the app routes
		// it mirrors (see internal/publicapi), plus its own per-key budget
		// separate from /mcp's. Browser sessions pass the limiter untouched.
		if deps.APISpec != nil {
			v1Limiter := auth.NewTokenRateLimiter("api-v1", publicAPIRPS, publicAPIBurst)
			r.Route("/api/v1", func(r chi.Router) {
				r.Use(v1Limiter.Middleware)
				publicapi.Register(r, publicapi.Handlers{
					Queries:  deps.Queries,
					Pool:     deps.DB.(*pgxpool.Pool),
					Spec:     deps.APISpec,
					Projects: projectHandler,
					Files:    fileHandler,
					Jobs:     jobHandler,

					Translations: translationHandler,
					Comments:     comments.NewService(deps.Queries),
					TMs:          tmHandler,
					Glossaries:   termHandler,
					Templates:    templateHandler,
					AIPrompts:    aiPromptHandler,
					Invites:      inviteSvc,
					AICatalog:    catalog,
				})
			})
		}

		// Notifications
		notifHandler := notif.NewHandler(deps.Queries)
		r.Route("/api/notifications", func(r chi.Router) {
			r.Get("/", notifHandler.HandleList)
			r.Get("/count", notifHandler.HandleUnreadCount)
			r.Post("/read-all", notifHandler.HandleMarkAllRead)
			r.Patch("/{notificationID}/read", notifHandler.HandleMarkRead)
			// Real-time SSE delivery. Optional — if
			// the broker isn't wired (test harness without notifications),
			// we leave the endpoint off entirely and the frontend falls
			// back to its existing 30s polling cadence.
			if deps.NotificationBroker != nil {
				sseHandler := notif.NewSSEHandler(deps.NotificationBroker, deps.Queries, deps.Draining)
				r.Get("/stream", sseHandler.HandleStream)
			}
		})
		r.Get("/api/notification-preferences", notifHandler.HandleGetPreferences)

		// Any member files issues; managers see the whole org's, anyone else
		// their own.
		if issueSvc.Enabled() {
			ih := issues.NewHandler(issueSvc)
			r.Get("/api/issues", ih.HandleList)
			r.Post("/api/issues", ih.HandleCreate)
			r.Get("/api/issues/{issueID}", ih.HandleGet)
			// No RequireRole: every role deletes its own; the handler checks
			// the reporter's rank for anyone else's.
			r.Delete("/api/issues/{issueID}", ih.HandleDelete)
			r.Post("/api/issues/{issueID}/comments", ih.HandleComment)
			r.Post("/api/issues/{issueID}/confirm", ih.HandleConfirm)
			// No RequireRole: like a comment, whoever sees the issue (the
			// reporter, or a manager) may change its kind.
			r.Patch("/api/issues/{issueID}/kind", ih.HandleKind)
			r.Get("/api/issues/{issueID}/attachments/{attachmentID}", ih.HandleAttachment)
		}
		r.Put("/api/notification-preferences", notifHandler.HandleUpdatePreferences)

		// Dev LLM/extension viewer (/debug/llm page), served from the
		// in-memory ring. Mounted only when the ring exists (DEV_AUTO_LOGIN=true
		// in main.go). Production has no ring and instead mounts the SAME path
		// from object storage inside the platform-admin group above — one path,
		// one registration, so chi can't see a duplicate pattern.
		if deps.LLMDebugRing != nil {
			r.Get("/api/debug/llm/calls", llmdebug.NewHandler(deps.LLMDebugRing).HandleList)
		}
		// Dev email outbox, for E2E tests that follow an emailed link. It holds
		// live tokens, so it needs both the outbox and the dev-login flag.
		if deps.DevOutbox != nil && deps.DevAutoLogin {
			r.Get("/api/dev/outbox", deps.DevOutbox.HandleList)
		}

		// Per-user appearance (palettes) — personal setting, any authenticated
		// role, keyed by user so it follows the user across orgs.
		appearanceHandler := appearance.NewHandler(deps.Queries)
		r.Get("/api/appearance", appearanceHandler.HandleGet)
		r.Put("/api/appearance", appearanceHandler.HandleUpdate)

		// Org-level notification rules — never a vendor. They switch a
		// notification off for every member, so managers read and only
		// owner/admin change them.
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireManager())
			r.Get("/api/org/notification-rules", notifHandler.HandleGetOrgRules)
			r.With(auth.RequireRole("owner", "admin")).Put("/api/org/notification-rules", notifHandler.HandleUpdateOrgRules)
		})

		// Manual deadline-check trigger for the caller's org (owner/admin; dev/ops use). Off in
		// tests where the scheduled handler isn't wired.
		if deps.NotificationScheduledHandler != nil {
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole("owner", "admin"))
				r.Post("/api/admin/notifications/deadline-check", deps.NotificationScheduledHandler.HandleManualDeadlineCheck)
			})
		}

		// Org registration settings
		r.Get("/api/org/settings", handleGetOrgSettings(deps.Queries))
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole("owner", "admin"))
			r.Put("/api/org/settings", handleUpdateOrgSettings(deps.Queries))
		})

		// Analytics — member-level reads are blocked for vendors (they must not
		// enumerate org/project productivity).
		analyticsHandler := analyticsPkg.NewHandler(deps.Queries)
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireManager())
			r.Get("/api/analytics/weights", analyticsHandler.HandleGetWeights)
			r.Get("/api/analytics/productivity", analyticsHandler.HandleOrgProductivity)
			r.Get("/api/projects/{projectID}/productivity", analyticsHandler.HandleProjectProductivity)
			r.Get("/api/projects/{projectID}/productivity/by-step", analyticsHandler.HandleProjectStepProductivity)
		})
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole("owner", "admin"))
			r.Put("/api/analytics/weights", analyticsHandler.HandleUpdateWeights)
		})
	})

	return r
}

// --- Org settings handlers ---

type orgSettingsResponse struct {
	OpenRegistration bool    `json:"open_registration"`
	Domain           *string `json:"domain"`
}

func handleGetOrgSettings(q db.Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID := auth.TenantIDFromContext(r.Context())
		tenant, err := q.GetTenant(r.Context(), tenantID)
		if err != nil {
			httputil.ServerError(w, "load", "tenant")
			return
		}
		httputil.JSON(w, http.StatusOK, orgSettingsResponse{
			OpenRegistration: tenant.OpenRegistration,
			Domain:           tenant.Domain,
		})
	}
}

func handleUpdateOrgSettings(q db.Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OpenRegistration *bool `json:"open_registration"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.BadRequest(w, "invalid request body")
			return
		}
		tenantID := auth.TenantIDFromContext(r.Context())
		if req.OpenRegistration != nil {
			if err := q.UpdateTenantOpenRegistration(r.Context(), db.UpdateTenantOpenRegistrationParams{
				ID:               tenantID,
				OpenRegistration: *req.OpenRegistration,
			}); err != nil {
				httputil.ServerError(w, "update", "settings")
				return
			}
		}
		// Return updated state
		tenant, err := q.GetTenant(r.Context(), tenantID)
		if err != nil {
			httputil.ServerError(w, "load", "tenant")
			return
		}
		httputil.JSON(w, http.StatusOK, orgSettingsResponse{
			OpenRegistration: tenant.OpenRegistration,
			Domain:           tenant.Domain,
		})
	}
}

func handleUpdateMemberRole(q db.Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := httputil.ParseID(r, "userID")
		if err != nil {
			httputil.BadRequest(w, "invalid user ID")
			return
		}
		var req struct {
			Role string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.BadRequest(w, "invalid request body")
			return
		}
		if _, err := auth.ChangeMemberRole(r.Context(), q, auth.ActorFromContext(r.Context()), userID, req.Role); err != nil {
			auth.WriteMemberError(w, err)
			return
		}
		httputil.JSON(w, http.StatusOK, map[string]string{"status": "ok", "role": req.Role})
	}
}
