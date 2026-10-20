// Package errtrack reports errors to Honeybadger.
//
// OpenLoc reports to its own Honeybadger project. Every notice follows four
// rules:
//
//   - ErrorClass is an explicit PascalCase domain name, never the Go type
//   - Fingerprint is a hyphen-joined slug from the shared Fingerprint builder
//   - context is passed per call, never via the process-global SetContext
//   - a reporting failure logs and is swallowed; it never breaks a request
//
// # Why explicit ErrorClass and Fingerprint are mandatory
//
// Honeybadger groups by error class name by default, and in Go nearly every
// error is *errors.errorString or *fmt.wrapError. Left to the default, the
// entire application would collapse into one or two useless issues. Every
// Capture call therefore sets both.
//
// # Sensitivity
//
// Notices leave our infrastructure. Unlike logs — which may legitimately carry
// truncated segment text, see the internal/logging package doc — a notice must
// never contain customer content or credentials. The SDK works against us here:
// its Handler passes the *http.Request straight to Notify, and setHttpRequest
// then copies every header (including Cookie and Authorization), the full URL
// with query string, and all query/form params into the notice. Nothing gates
// that; there is no SendDefaultPII equivalent. scrubNotice (scrub.go) is
// registered as a BeforeNotify handler and is a hard prerequisite for enabling
// the middleware, not a later refinement.
package errtrack

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/honeybadger-io/honeybadger-go"
	"openloc/internal/logging"
)

// Error classes. PascalCase and domain-specific, never a Go type name.
const (
	ClassHTTPServerError = "HTTPServerError"
	ClassRequestPanic    = "RequestPanic"
	ClassWorkerTaskFail  = "WorkerTaskFailed"
	ClassStartupFailure  = "StartupFailure"

	// ClassShutdownFailure is graceful shutdown not completing in time.
	//
	// Kept apart from ClassStartupFailure, which means "a pod cannot come up"
	// and is the most urgent thing this service reports. Shutdown timeouts are
	// routine on deploy (long-lived SSE streams hold http.Server.Shutdown open
	// until its deadline), and sharing a class would bury the startup alert
	ClassShutdownFailure = "ShutdownFailure"

	// ClassEmailSendFailed covers a notification, invite or password-reset email
	// that could not be delivered. These are swallowed on purpose — SMTP being
	// down should not fail the request that triggered it — which means the
	// caller gets HTTP 200 and the recipient gets nothing. Without a notice the
	// only evidence is a support ticket ("I never got the invite").
	//
	// Never put the recipient address in one of these notices; see sendFailure
	// callers and the package doc on what may leave our infrastructure.
	ClassEmailSendFailed = "EmailSendFailed"

	// ClassStateWriteFailed covers a discarded write that leaves a row
	// inconsistent with reality — the `_ = queries.Update…` shape. The canonical
	// case is UpdateAnalysisFailed: the analysis already failed, and if marking
	// it failed also fails, it displays as "running" forever with no error path
	// left to notice.
	ClassStateWriteFailed = "StateWriteFailed"

	// ClassUnsupportedImport covers a file a user tried to import that we can
	// identify but don't support (e.g. a DCT-style or non-TBX file uploaded to the
	// TBX importer). Not a bug — it's demand signal: the notice's context carries
	// which dialect/type was attempted, so we can see what's worth supporting next.
	ClassUnsupportedImport = "UnsupportedImport"
)

// ContextRevision is the deploy identifier. The Go SDK has no Revision config
// field (unlike the JS one), so deploy correlation is a filterable context key
// rather than a first-class release view.
const ContextRevision = "revision"

// ContextUserID gets special aggregation treatment in Honeybadger's UI, so the
// key name matters.
const ContextUserID = "user_id"

// ContextUserEmail is Honeybadger's native user-email convention — the fault
// UI renders it alongside user_id. Populated from the logging.Scope's dedicated
// email slot (never from scope.Fields(), so it cannot leak into ECS logs).
const ContextUserEmail = "user_email"

// ContextImpersonatingUserEmail is the address of the user being impersonated —
// the owner of the org a platform operator is viewing. Present only on
// impersonated sessions, where user_email is the operator driving and this is
// whose account they are inside; a fault needs both to be actionable. The one
// sanctioned exception to "no third party's address in a notice": the operator
// already sees this address in the platform console.
//
// Always the OWNER. HandleLoginAsOrg enters an org, not a chosen user — it takes
// a tenantID and grants RoleOwner — so this is never some other member's address,
// and it matches the identity /auth/me displays for the same session.
const ContextImpersonatingUserEmail = "impersonating_user_email"

// ContextImpersonating marks the notice as coming from a "log in as
// org" session. Present only when true, so it reads as a tag in the fault UI
// and filters cleanly. Separate from ContextImpersonatingUserEmail because an
// org with no owner row yields no address, and that notice still must not look
// like an ordinary session.
const ContextImpersonating = "impersonating"

// Options configures Init.
type Options struct {
	// APIKey is the OpenLoc project key. Empty disables reporting entirely.
	APIKey string
	// Environment separates staging from production notices in Honeybadger,
	// which is what keeps branch noise out of production alerting.
	Environment string
	// Revision is the VERSION git hash, attached to every notice.
	Revision string
	// Sync forces synchronous delivery. Tests set it so assertions don't race
	// the buffered worker; production leaves it false.
	Sync bool
	// Backend overrides the transport. Tests inject a recorder.
	Backend honeybadger.Backend
}

// revision is captured at Init and attached to every notice.
var revision string

// enabled gates every send. Default false, so a process that never calls Init
// cannot report — see the comment in Init for why that matters.
var enabled atomic.Bool

// Init configures the process-wide Honeybadger client and returns a flush
// function to call before exit.
//
// Two SDK behaviours make an explicit gate necessary rather than paranoid:
//
//  1. The SDK reads HONEYBADGER_API_KEY from the environment at package init,
//     with the real HTTP backend. So a binary that merely links this package —
//     any test binary, cmd/seed — would send live notices to the shared org
//     account if something called Capture, without anyone configuring anything.
//  2. Configuration.update only overwrites non-zero fields, so passing an empty
//     APIKey here does NOT clear an environment-derived one. "Empty key means
//     off" cannot be expressed through the SDK's own config at all.
//
// Hence `enabled`: Capture is a no-op until Init explicitly turns it on, and an
// empty APIKey additionally installs the null backend as a second line of
// defence.
func Init(o Options) (flush func()) {
	revision = o.Revision

	cfg := honeybadger.Configuration{
		APIKey: o.APIKey,
		Env:    o.Environment,
		Sync:   o.Sync,
	}
	switch {
	case o.Backend != nil:
		cfg.Backend = o.Backend
	case o.APIKey == "":
		cfg.Backend = honeybadger.NewNullBackend()
	}
	honeybadger.Configure(cfg)

	// Register the handlers exactly once per process.
	//
	// honeybadger.BeforeNotify APPENDS to a slice that is never cleared, so a
	// second Init would stack a second copy of each handler — and since the rate
	// limiter consumes a token per invocation, N copies would consume N tokens
	// per notice and throttle almost everything. Production calls Init once, but
	// making it idempotent is what lets tests re-Init at all, and it removes a
	// footgun for any future caller.
	//
	// Order matters: scrub first, so the limiter sees a clean notice and an
	// early return from the limiter can never skip scrubbing.
	registerOnce.Do(func() {
		honeybadger.BeforeNotify(scrubNotice)
		honeybadger.BeforeNotify(dropMisleadingBacktrace)
		honeybadger.BeforeNotify(defaultLimiter.allow)
	})

	// Reconfiguring resets rate-limit state: the previous configuration's
	// accounting has no bearing on the new one, and without this the limiter
	// would carry counts across every test in a package.
	defaultLimiter.reset()

	enabled.Store(o.APIKey != "" || o.Backend != nil)
	return honeybadger.Flush
}

// registerOnce guards BeforeNotify registration — see Init.
var registerOnce sync.Once

// Disable turns reporting off. Tests use it to restore state; production
// doesn't call it.
func Disable() { enabled.Store(false) }

// Flush blocks until queued notices have been delivered. Delivery is async by
// default, so any path that ends in os.Exit must call this first or the notice
// is silently dropped — which is exactly the case (a crash-looping pod) where
// you most need it.
func Flush() { honeybadger.Flush() }

// Enabled reports whether reporting is on. Deliberately tracks our own state
// rather than reading honeybadger.Config.APIKey, which can hold a stale
// environment-derived key even when the null backend is installed.
func Enabled() bool { return enabled.Load() }

// CaptureOptions describes one notice. Class and Event are required; without
// them grouping degrades to Honeybadger's default, which is useless in Go.
type CaptureOptions struct {
	// Class is the ErrorClass — use one of the Class* constants.
	Class string
	// Message is the human sentence, e.g. "translate task failed after retries".
	Message string
	// Subsystem scopes the fingerprint: "http", "worker", "startup".
	Subsystem string
	// Event is the stable slug for what happened, e.g. a route pattern or task
	// type. Keep it free of IDs — those fragment grouping.
	Event string
	// Entity optionally discriminates grouping further (a job ID, a file ID).
	// Use sparingly: a unique value per occurrence defeats grouping entirely.
	Entity string
	// Extra adds context keys beyond whatever the logging.Scope already carries.
	Extra map[string]any
}

// Capture reports err. A nil err is allowed — some call sites detect a failed
// invariant with no error value — and yields a notice built from Message alone.
//
// Never returns an error: a failure to report is logged and swallowed.
// Observability must not be able to break the request it is observing.
func Capture(ctx context.Context, err error, o CaptureOptions) {
	if !enabled.Load() {
		return
	}
	defer func() {
		// The SDK marshals notices with panic-on-error (Notice.toJSON), so a
		// pathological context value could otherwise take down a request.
		if rec := recover(); rec != nil {
			logging.Error(ctx, nil, fmt.Sprintf("panic while reporting error: %v", rec), "errtrack-notify-panic")
		}
	}()

	notify(ctx, err, o, 3)
}

// notify is split out so the stack depth stays fixed regardless of which
// exported wrapper was used. skip counts frames from runtime.Callers.
func notify(ctx context.Context, err error, o CaptureOptions, skip int) {
	scope := logging.ScopeFromContext(ctx)

	hbCtx := honeybadger.Context{}
	for k, v := range scope.Fields() {
		hbCtx[k] = v
	}
	for k, v := range o.Extra {
		hbCtx[k] = v
	}
	if revision != "" {
		hbCtx[ContextRevision] = revision
	}
	// Honeybadger aggregates on user_id specifically, so mirror logging's user.id
	// into the key its UI understands.
	if uid, ok := hbCtx[logging.KeyUserID]; ok {
		hbCtx[ContextUserID] = uid
	}
	// The email rides the Scope's dedicated slot, not Fields() — notices carry
	// it (Honeybadger's user_email convention), ECS logs never do.
	if email := scope.UserEmail(); email != "" {
		hbCtx[ContextUserEmail] = email
	}
	if email := scope.ImpersonatingUserEmail(); email != "" {
		hbCtx[ContextImpersonatingUserEmail] = email
	}
	if scope.Impersonating() {
		hbCtx[ContextImpersonating] = true
	}

	tenant, _ := hbCtx[logging.KeyTenantID].(string)
	fp := Fingerprint(o.Subsystem, o.Class, tenant, o.Entity, o.Event)

	// The reported message is a human sentence followed by the underlying
	// cause. Identity is deliberately NOT in the message — it lives in the
	// context (tenant.id, user.id, user_email; Honeybadger renders user_id and
	// user_email natively), where the scrubber and the context-level rules can
	// govern it. A vendor body in the cause is replaced by its LogMessage, the
	// same text the logs carry.
	reported := safeCause(err)
	switch {
	case err == nil:
		reported = errors.New(orFallback(o.Message, "unspecified error"))
	case o.Message != "":
		reported = fmt.Errorf("%s: %w", o.Message, reported)
	}

	if _, notifyErr := honeybadger.Notify(
		withStack(reported, skip+1),
		honeybadger.ErrorClass{Name: orFallback(o.Class, "UnknownError")},
		honeybadger.Fingerprint{Content: fp},
		hbCtx,
	); notifyErr != nil {
		// A dropped notice is expected when the rate limiter kicks in, so this
		// is deliberately not an error-level log.
		logging.Debug(ctx, "notice not sent: "+notifyErr.Error(), "errtrack-notify-skipped")
	}
}

// Fingerprint builds the grouping key, hyphen-joined in the shape
// subsystem-class-tenant-entity-event. Empty
// parts become "unknown" rather than collapsing into adjacent hyphens, so a
// missing tenant can't silently merge two different groups.
func Fingerprint(subsystem, class, tenant, entity, event string) string {
	parts := []string{
		orFallback(subsystem, "unknown"),
		orFallback(class, "unknown"),
		orFallback(tenant, "unknown"),
		orFallback(entity, "none"),
		orFallback(event, "unknown"),
	}
	return strings.ToLower(strings.Join(parts, "-"))
}

// EntityID renders an int64 for use as CaptureOptions.Entity.
func EntityID(v int64) string { return strconv.FormatInt(v, 10) }

func orFallback(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// stackedError carries its own call stack. honeybadger's autostack checks for
// an error implementing `Callers() []uintptr` and prefers that over walking the
// runtime stack — without this, every backtrace would begin inside this
// package instead of at the real call site.
type stackedError struct {
	error
	callers []uintptr
}

func (e *stackedError) Callers() []uintptr { return e.callers }
func (e *stackedError) Unwrap() error      { return e.error }

// redactedError is a cause whose text went through logging.SafeErrorMessage.
// It still unwraps to the original, so errors.Is/As keep working.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

// safeCause returns err unchanged unless its chain holds a logging.LogSafe
// error, in which case the text is the logged one.
func safeCause(err error) error {
	if err == nil {
		return nil
	}
	msg := logging.SafeErrorMessage(err)
	if msg == err.Error() {
		return err
	}
	return &redactedError{msg: msg, err: err}
}

const maxFrames = 32

func withStack(err error, skip int) error {
	pcs := make([]uintptr, maxFrames)
	n := runtime.Callers(skip, pcs)
	return &stackedError{error: err, callers: pcs[:n]}
}
