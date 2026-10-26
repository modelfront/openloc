package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"openloc/internal/logging"
)

type Config struct {
	Port        string
	DatabaseURL string
	RedisAddr   string
	S3Endpoint  string // Non-empty = S3-compatible store with static creds (SeaweedFS in dev); empty = real AWS S3 (IAM roles)
	S3AccessKey string // Only used with a static-credential endpoint
	S3SecretKey string // Only used with a static-credential endpoint
	S3Bucket    string
	JWTSecret   string

	// Google OAuth SSO
	GoogleClientID     string
	GoogleClientSecret string
	// Microsoft (Entra ID) SSO — work/school accounts only. Empty
	// client ID disables the Microsoft sign-in routes (404), same as Google.
	MicrosoftClientID     string
	MicrosoftClientSecret string
	BaseURL               string // e.g. http://localhost:9000 — this deployment's own origin, used for frontend redirects
	DocsHost              string // DOCS_HOST, e.g. docs.openloc.com — the public help's one address, at its root; empty = help stays at /docs
	DeveloperHost         string // DEVELOPER_HOST, e.g. developer.openloc.com — the public API docs' one address, at its root; empty = they stay at /api-docs
	DevAutoLogin          bool   // DEV_AUTO_LOGIN=true enables /api/auth/dev-login

	// Shared OAuth redirect URI. Providers accept only
	// exactly-registered redirect URIs and no wildcards, so every preview
	// deployment (one per branch, say) would need its own console entry.
	// Instead they all share the main deployment's registered URI, and the main
	// deployment forwards the authorization code to the one named in the OAuth
	// state.
	//
	//   OAuthRedirectBaseURL — OAUTH_REDIRECT_BASE_URL. Origin that owns the
	//                 registered redirect URI. Empty means this deployment owns
	//                 it (auth.NewOAuthRelay falls back to BaseURL), which is
	//                 the case for a single deployment and local dev.
	//   OAuthRelayDomain — OAUTH_RELAY_DOMAIN. Parent domain this deployment may
	//                 forward callbacks to; only `<label>.<domain>` is accepted.
	//                 Empty refuses every forward, so dev and tests never relay.
	OAuthRedirectBaseURL string
	OAuthRelayDomain     string
	// OpenAIAppsChallenge — OPENAI_APPS_CHALLENGE. The domain-verification
	// token OpenAI issues for a ChatGPT plugin submission, served at
	// /.well-known/openai-apps-challenge. Empty serves 404.
	OpenAIAppsChallenge string

	// Platform operators allowed to approve/reject self-serve
	// org signups. Comma-separated emails in PLATFORM_ADMIN_EMAILS.
	// This single list is both the approval-endpoint gate and the notification
	// recipient set — no super-admin role/table.
	PlatformAdminEmails []string

	// Parser sidecar (Python service for DOCX/PDF)
	ParserSidecarURL string // PARSER_SIDECAR_URL — default http://localhost:5200

	// LanguageTool (spell + grammar check). Self-hosted by default: the public
	// api.languagetool.org endpoint allows only 20 requests/IP/minute and caps
	// suggestions at the first 30 misspellings per check, neither of which
	// survives a document-wide check — and it would ship every customer segment
	// to a third party. Point SPELLCHECK_LT_URL at the public endpoint or a
	// LanguageTool Enterprise host if a deployment prefers that tradeoff.
	SpellcheckLTURL string // SPELLCHECK_LT_URL — default http://localhost:9008/v2/check

	// Static file serving (production: Go serves React SPA)
	StaticDir string // STATIC_DIR — path to web/dist/; empty = disabled (dev uses Vite)

	// Upload limit in MB for project files, assistant uploads and re-imports.
	MaxUploadMB int // MAX_UPLOAD_MB — default 200

	// Email. One sender per deployment, picked by notification.NewSender:
	// SMTP when SMTP_HOST is set, Gmail when the Google service account is,
	// otherwise a console sender that logs emails (dev). Setting both is an
	// error.
	//   NotificationEmail             — the From address; for Gmail also the
	//                                   Workspace mailbox to impersonate
	//   NotificationServiceClientID   — Gmail: service-account JWT issuer
	//   NotificationServicePrivateKey — Gmail: service-account PEM private key
	//   SMTPHost, SMTPPort            — any SMTP server; port defaults to 587
	//   SMTPUsername, SMTPPassword    — both or neither (a relay without login)
	//   SMTPTLS                       — starttls | tls | none; empty = tls on
	//                                   port 465, starttls otherwise
	NotificationEmail             string // NOTIFICATION_EMAIL
	NotificationServiceClientID   string // NOTIFICATION_SERVICE_CLIENT_ID
	NotificationServicePrivateKey string // NOTIFICATION_SERVICE_PRIVATE_KEY
	SMTPHost                      string // SMTP_HOST
	SMTPPort                      int    // SMTP_PORT
	SMTPUsername                  string // SMTP_USERNAME
	SMTPPassword                  string // SMTP_PASSWORD
	SMTPTLS                       string // SMTP_TLS

	// AI provider secrets encryption. Any string; SHA-256'd to a 32-byte
	// AES-256-GCM key. Provider API keys are stored encrypted at rest.
	// Development falls back to DevAISecretsKey; every other ENV must set
	// AI_SECRETS_KEY or Validate refuses to start. Rotating it invalidates all
	// stored provider keys (they'd need re-connecting).
	AISecretsKey string

	// Per-tenant LLM model catalog. The list of selectable
	// models is fetched from each vendor's API with the tenant's own key, so it
	// reflects what that key can actually reach.
	//
	//   AIModelListLimit      — AI_MODEL_LIST_LIMIT. How many models a vendor
	//                           offers in a picker, newest first. The full
	//                           fetched list still backs validation, so lowering
	//                           this can't invalidate an already-pinned model.
	//   AIModelRefreshMinutes — AI_MODEL_REFRESH_MINUTES. How often the cached
	//                           catalogs are refetched. Only entries someone is
	//                           still reading are refreshed — not every tenant
	//                           holding a key — so provider call volume tracks
	//                           active organizations, not the customer count.
	AIModelListLimit      int
	AIModelRefreshMinutes int

	// Periodic scheduler (asynq.Scheduler). Default true. In multi-replica
	// production, set RUN_SCHEDULER=false on all but ONE replica so the
	// hourly deadline scan doesn't fan out (asynq doesn't dedup periodic
	// task enqueues across multiple schedulers).
	RunScheduler bool

	// DemoOrg — DEMO_ORG. Keeps the shared, read-only demo organization:
	// built at server start and by `make seed`, refreshed
	// daily by the scheduler. Default on outside production, off in production
	// until it is switched on there deliberately.
	DemoOrg bool

	// TrustedProxyHops — TRUSTED_PROXY_HOPS. How many reverse proxies sit
	// between the internet and this server; decides which address the per-IP
	// rate limits key on (httputil.ClientIP). 0, the default, trusts no
	// forwarding header. Production behind the nginx ingress sets 1.
	TrustedProxyHops int

	// AsynqQueue namespaces this deployment's background tasks. Deployments
	// that share one Redis (preview deployments, say) need one queue each:
	// asynq has no key prefix, so deployments on a shared queue consume each
	// other's jobs and run them with the wrong code. Set per deployment;
	// empty = asynq's "default".
	AsynqQueue string

	// WorkerConcurrency is how many short background tasks (analyses,
	// notifications, sweeps, exports) one worker process runs at once;
	// WorkerLongConcurrency is how many pipeline and QE+APE tasks it runs at
	// once, on their own queue, so long tasks never take the short ones' slots.
	WorkerConcurrency     int
	WorkerLongConcurrency int

	// Persisted LLM debug log. Independent of DevAutoLogin:
	// dev keeps the in-memory ring + /debug/llm viewer and writes nothing, prod
	// writes NDJSON batches to the bucket and mounts no route.
	//
	//   LLMDebugPersist    — LLM_DEBUG_PERSIST. Off by default; capture costs
	//                        object writes and retains customer segment text.
	//   LLMDebugRetainDays — LLM_DEBUG_RETAIN_DAYS. Days BEFORE today to keep,
	//                        so the default 1 keeps today + yesterday. The
	//                        literal mirrors llmdebug.DefaultRetainDays;
	//                        config stays a leaf package rather than importing
	//                        a domain one for a constant.
	//   LLMDebugFlushSeconds — LLM_DEBUG_FLUSH_SECONDS. How long a partial batch
	//                          waits before being written. Also the /debug/llm
	//                          feed's worst-case lag, which is why the default
	//                          is 10 rather than 60: the viewer's whole point is
	//                          watching calls as they happen.
	LLMDebugPersist      bool
	LLMDebugRetainDays   int
	LLMDebugFlushSeconds int

	// Observability.
	//
	//   ECSLogging  — ECS_LOGGING. "fleet" switches structured logging to
	//                 ECS-shaped JSON for the logs-openloc-* data stream;
	//                 anything else gives human-readable text for local dev.
	//   Environment — ENV. Populates ECS service.environment and the
	//                 Honeybadger environment, which keeps notices from a test
	//                 deployment out of production alerting.
	//   Revision    — VERSION (the git hash the image was built from). ECS service.version, and the Honeybadger
	//                 `revision` context key: the Go SDK has no Revision
	//                 config field, so deploy correlation is a filterable
	//                 context value rather than a first-class release.
	//   HoneybadgerEnabled — HONEYBADGER_ENABLED. The on-switch for error
	//                 reporting, default off. Load blanks HoneybadgerAPIKey
	//                 unless it is true.
	//   HoneybadgerAPIKey — HONEYBADGER_API_KEY. Error reporting runs only when
	//                 this is set AND HoneybadgerEnabled is true.
	ECSLogging         string
	Environment        string
	Revision           string
	HoneybadgerEnabled bool
	HoneybadgerAPIKey  string
}

func Load() Config {
	cfg := Config{
		Port:        envOr("PORT", "8080"),
		DatabaseURL: envOr("DATABASE_URL", "postgres://openloc:openloc@localhost:9002/openloc?sslmode=disable"),
		RedisAddr:   envOr("REDIS_ADDR", "localhost:9003"),
		S3Endpoint:  envOr("S3_ENDPOINT", "http://localhost:9004"),
		S3AccessKey: envOr("S3_ACCESS_KEY", "changeme-s3-access-key"),
		S3SecretKey: envOr("S3_SECRET_KEY", "changeme-s3-secret-key"),
		S3Bucket:    envOr("S3_BUCKET", "openloc"),
		JWTSecret:   envOr("JWT_SECRET", DevJWTSecret),

		GoogleClientID:        envOr("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret:    envOr("GOOGLE_CLIENT_SECRET", ""),
		MicrosoftClientID:     envOr("MICROSOFT_CLIENT_ID", ""),
		MicrosoftClientSecret: envOr("MICROSOFT_CLIENT_SECRET", ""),
		BaseURL:               envOr("BASE_URL", "http://localhost:9000"),
		DocsHost:              strings.ToLower(envOr("DOCS_HOST", "")),
		DeveloperHost:         strings.ToLower(envOr("DEVELOPER_HOST", "")),
		DevAutoLogin:          envOr("DEV_AUTO_LOGIN", "") == "true",
		OAuthRedirectBaseURL:  envOr("OAUTH_REDIRECT_BASE_URL", ""),
		OAuthRelayDomain:      envOr("OAUTH_RELAY_DOMAIN", ""),
		OpenAIAppsChallenge:   envOr("OPENAI_APPS_CHALLENGE", ""),

		PlatformAdminEmails: parseCSVLower("PLATFORM_ADMIN_EMAILS"),

		ParserSidecarURL: envOr("PARSER_SIDECAR_URL", "http://localhost:9007"),
		SpellcheckLTURL:  envOr("SPELLCHECK_LT_URL", "http://localhost:9008/v2/check"),
		StaticDir:        envOr("STATIC_DIR", ""),
		MaxUploadMB:      parseIntOr("MAX_UPLOAD_MB", 200),

		NotificationEmail:             envOr("NOTIFICATION_EMAIL", ""),
		NotificationServiceClientID:   envOr("NOTIFICATION_SERVICE_CLIENT_ID", ""),
		NotificationServicePrivateKey: envOr("NOTIFICATION_SERVICE_PRIVATE_KEY", ""),
		SMTPHost:                      envOr("SMTP_HOST", ""),
		SMTPPort:                      parseIntOr("SMTP_PORT", 587),
		SMTPUsername:                  envOr("SMTP_USERNAME", ""),
		SMTPPassword:                  envOr("SMTP_PASSWORD", ""),
		SMTPTLS:                       envOr("SMTP_TLS", ""),

		AISecretsKey: os.Getenv("AI_SECRETS_KEY"),

		AIModelListLimit:      parseIntOr("AI_MODEL_LIST_LIMIT", 8),
		AIModelRefreshMinutes: parseIntOr("AI_MODEL_REFRESH_MINUTES", 30),

		// Default ON: dev + single-replica prod just work. Multi-replica
		// prod sets RUN_SCHEDULER=false on N-1 replicas (only one runs cron).
		// Lenient parsing — accepts every strconv.ParseBool form (0/f/F/false/
		// False/FALSE and the inverses).
		RunScheduler: parseBoolOr("RUN_SCHEDULER", true),
		AsynqQueue:   envOr("ASYNQ_QUEUE", ""),

		WorkerConcurrency:     parseIntOr("WORKER_CONCURRENCY", 4),
		WorkerLongConcurrency: parseIntOr("WORKER_LONG_CONCURRENCY", 4),

		LLMDebugPersist:      parseBoolOr("LLM_DEBUG_PERSIST", false),
		LLMDebugRetainDays:   parseIntOr("LLM_DEBUG_RETAIN_DAYS", 1),
		LLMDebugFlushSeconds: parseIntOr("LLM_DEBUG_FLUSH_SECONDS", 10),

		ECSLogging:         envOr("ECS_LOGGING", ""),
		Environment:        envOr("ENV", "development"),
		Revision:           envOr("VERSION", "dev"),
		HoneybadgerEnabled: parseBoolOr("HONEYBADGER_ENABLED", false),
		HoneybadgerAPIKey:  envOr("HONEYBADGER_API_KEY", ""),
	}
	if cfg.AISecretsKey == "" && cfg.Environment == "development" {
		cfg.AISecretsKey = DevAISecretsKey
	}
	cfg.DemoOrg = parseBoolOr("DEMO_ORG", cfg.Environment != "production")
	cfg.TrustedProxyHops = parseIntOr("TRUSTED_PROXY_HOPS", 0)
	// Error reporting needs both the switch and the key. Blanking the key here
	// switches off backend notices (errtrack.Init installs the null backend),
	// the browser-notice proxy, and the /api/config error_reporting flag that
	// keeps the FE SDK off — one policy point for all three.
	if !cfg.HoneybadgerEnabled {
		cfg.HoneybadgerAPIKey = ""
	}
	return cfg
}

// DevAISecretsKey is the development-only AI_SECRETS_KEY. It is public, so it
// protects nothing; Load uses it only when ENV is "development".
const DevAISecretsKey = "dev-ai-secrets-key-change-me"

// Validate reports configuration the server must not start with.
func (c Config) Validate() error {
	if c.AISecretsKey == "" {
		return errors.New("AI_SECRETS_KEY must be set when ENV is not development")
	}
	if c.TrustedProxyHops < 0 {
		return errors.New("TRUSTED_PROXY_HOPS must be 0 or more")
	}
	if c.MaxUploadMB < 1 {
		return errors.New("MAX_UPLOAD_MB must be 1 or more")
	}
	if c.Environment != "development" {
		if err := validateJWTSecret(c.JWTSecret); err != nil {
			return err
		}
	}
	return nil
}

// DevJWTSecret is the development-only JWT_SECRET. It is public, so Validate
// refuses it outside development.
const DevJWTSecret = "dev-secret-change-me"

// minJWTSecretLen is the shortest JWT_SECRET accepted outside development.
const minJWTSecretLen = 32

func validateJWTSecret(secret string) error {
	switch {
	case secret == DevJWTSecret:
		return errors.New("JWT_SECRET must be set when ENV is not development")
	case strings.HasPrefix(strings.ToUpper(secret), "CHANGE_ME"):
		return errors.New("JWT_SECRET is still the CHANGE_ME placeholder; generate one with `openssl rand -hex 32`")
	case len(secret) < minJWTSecretLen:
		return fmt.Errorf("JWT_SECRET must be at least %d characters; generate one with `openssl rand -hex 32`", minJWTSecretLen)
	}
	return nil
}

// parseCSVLower reads a comma-separated env var into a slice of trimmed,
// lowercased, non-empty values. Returns nil if unset/empty. Used for
// PLATFORM_ADMIN_EMAILS so "A@x.com, b@x.com" → ["a@x.com","b@x.com"].
func parseCSVLower(key string) []string {
	raw := os.Getenv(key)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if v := strings.ToLower(strings.TrimSpace(part)); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// parseBoolOr reads an env var and parses it via strconv.ParseBool, which
// accepts {1,t,T,TRUE,true,True} and {0,f,F,FALSE,false,False}. Returns
// `fallback` if unset OR unparseable; logs a warning on unparseable input
// so ops sees the typo instead of silently inheriting the default.
func parseBoolOr(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		logging.Warn(context.Background(), err, "invalid boolean in environment; using the default", "config-invalid-bool", logging.Label("env_key", key), logging.Label("fallback", strconv.FormatBool(fallback)))
		return fallback
	}
	return parsed
}

// parseIntOr reads an env var as an int, falling back (with a warning) on
// unparseable input — same contract as parseBoolOr, so a typo is visible
// instead of silently inheriting the default.
func parseIntOr(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(v)
	if err != nil {
		logging.Warn(context.Background(), err, "invalid integer in environment; using the default", "config-invalid-int", logging.Label("env_key", key), logging.Label("fallback", strconv.Itoa(fallback)))
		return fallback
	}
	return parsed
}
