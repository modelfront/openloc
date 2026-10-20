package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
)

// ServiceName is the ECS service.name for this service. Fleet's ndjson parser
// runs with overwrite_keys:true, so this wins over any agent-supplied value.
const ServiceName = "openloc"

// ECSModeValue is the ECS_LOGGING value that switches on ECS JSON output.
const ECSModeValue = "fleet"

// Options configures Init.
type Options struct {
	// ECS turns on ECS-shaped JSON. False gives human-readable text for local
	// development. Derive it with ECSEnabled.
	ECS bool
	// Version populates service.version — the VERSION git hash in production.
	Version string
	// Environment populates service.environment ("production" / "staging").
	Environment string
	// Level defaults to slog.LevelInfo.
	Level slog.Level
	// Writer defaults to os.Stderr. Tests inject a buffer.
	Writer io.Writer
}

// ECSEnabled reports whether an ECS_LOGGING value selects ECS output.
func ECSEnabled(ecsLogging string) bool {
	return strings.EqualFold(strings.TrimSpace(ecsLogging), ECSModeValue)
}

// current holds the active logger. Atomic because tests re-Init while other
// goroutines may be logging; in production Init runs once before anything else.
var current atomic.Pointer[slog.Logger]

func init() {
	// Sane default so packages that log before Init — and unit tests, and
	// cmd/seed — don't nil-panic. Text to stderr, matching pre-logging behaviour.
	l := New(Options{})
	current.Store(l)
}

// Init installs the process logger and returns it. Call once from main.
func Init(o Options) *slog.Logger {
	l := New(o)
	current.Store(l)
	return l
}

// L returns the active logger. Never nil.
func L() *slog.Logger { return current.Load() }

// SetLogger installs l as the process logger. Mainly for tests, which capture
// output into a buffer and restore the previous logger in a t.Cleanup. Ignores
// nil so a botched restore can't leave the package without a logger.
func SetLogger(l *slog.Logger) {
	if l != nil {
		current.Store(l)
	}
}

// New builds a logger without installing it. Useful in tests.
func New(o Options) *slog.Logger {
	w := o.Writer
	if w == nil {
		w = os.Stderr
	}

	var h slog.Handler
	if o.ECS {
		h = slog.NewJSONHandler(w, &slog.HandlerOptions{
			Level:       o.Level,
			ReplaceAttr: ecsRename,
		})
	} else {
		h = slog.NewTextHandler(w, &slog.HandlerOptions{Level: o.Level})
	}

	base := []slog.Attr{slog.String(KeyServiceName, ServiceName)}
	if o.Version != "" {
		base = append(base, slog.String(KeyServiceVersion, o.Version))
	}
	if o.Environment != "" {
		base = append(base, slog.String(KeyServiceEnv, o.Environment))
	}

	return slog.New(&scopeHandler{Handler: h.WithAttrs(base)})
}

// ecsRename maps slog's built-in attribute names onto ECS. Only top-level
// built-ins are touched — anything inside a group, and every application
// attribute, passes through untouched.
func ecsRename(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a
	}
	switch a.Key {
	case slog.TimeKey:
		a.Key = KeyTimestamp
	case slog.MessageKey:
		a.Key = KeyMessage
	case slog.LevelKey:
		// ECS log.level is conventionally lowercase.
		a.Key = KeyLogLevel
		a.Value = slog.StringValue(strings.ToLower(a.Value.String()))
	}
	return a
}

// scopeHandler merges the request-scoped Scope into every record, so identity
// and entity IDs are attached once per request rather than at each call site.
type scopeHandler struct{ slog.Handler }

func (h *scopeHandler) Handle(ctx context.Context, r slog.Record) error {
	attrs := ScopeFromContext(ctx).Attrs()
	if len(attrs) == 0 {
		return h.Handler.Handle(ctx, r)
	}
	// A per-call attr must win over the scope's value for the same key. Simply
	// appending both would emit the key twice — JSONHandler does not dedupe,
	// and while last-wins happens to be how encoding/json and Elasticsearch
	// read it, duplicate keys are ambiguous JSON. So skip any scope attr the
	// caller already set explicitly.
	seen := make(map[string]bool, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool { seen[a.Key] = true; return true })

	r2 := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	for _, a := range attrs {
		if !seen[a.Key] {
			r2.AddAttrs(a)
		}
	}
	r.Attrs(func(a slog.Attr) bool { r2.AddAttrs(a); return true })
	return h.Handler.Handle(ctx, r2)
}

func (h *scopeHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return &scopeHandler{Handler: h.Handler.WithAttrs(as)}
}

func (h *scopeHandler) WithGroup(name string) slog.Handler {
	return &scopeHandler{Handler: h.Handler.WithGroup(name)}
}

// --- Call-site API ----------------------------------------------------------
//
// Mirrors the sibling services' (message, event, meta) shape: `msg` is the
// human sentence, `action` is the stable machine slug landing in event.action.
// Keep `action` stable across message rewordings — it's what dashboards and
// alerts filter on.
//
//	logging.Info(ctx, "MT phase complete", "pipeline-mt-done", logging.FileID(id), logging.Count(n))

func Debug(ctx context.Context, msg, action string, attrs ...slog.Attr) {
	logAt(ctx, slog.LevelDebug, nil, msg, action, attrs)
}

func Info(ctx context.Context, msg, action string, attrs ...slog.Attr) {
	logAt(ctx, slog.LevelInfo, nil, msg, action, attrs)
}

// Warn takes an optional err — many warnings in this codebase are "X failed,
// degrading gracefully" and the cause is worth keeping. Pass nil when there
// isn't one.
func Warn(ctx context.Context, err error, msg, action string, attrs ...slog.Attr) {
	logAt(ctx, slog.LevelWarn, err, msg, action, attrs)
}

// Error records a failure. err may be nil for conditions that are errors
// without an error value (a failed invariant check, say).
func Error(ctx context.Context, err error, msg, action string, attrs ...slog.Attr) {
	logAt(ctx, slog.LevelError, err, msg, action, attrs)
}

// LogSafe is implemented by errors that wrap a vendor response body. The body
// can echo the request, which carries segment text, so logs get LogMessage —
// the same error without the body — in place of Error().
type LogSafe interface {
	error
	LogMessage() string
}

// SafeErrorMessage is err.Error() with any LogSafe error in its chain rendered
// through LogMessage, so wrappers keep their context but never the body. Logs
// and errtrack notices both render a cause through it.
func SafeErrorMessage(err error) string {
	msg := err.Error()
	var safe LogSafe
	if errors.As(err, &safe) {
		msg = strings.Replace(msg, safe.Error(), safe.LogMessage(), 1)
	}
	return msg
}

func logAt(ctx context.Context, level slog.Level, err error, msg, action string, attrs []slog.Attr) {
	l := L()
	if !l.Enabled(ctx, level) {
		return
	}
	out := make([]slog.Attr, 0, len(attrs)+3)
	if action != "" {
		out = append(out, slog.String(KeyEventAction, action))
	}
	if err != nil {
		out = append(out,
			slog.String(KeyErrorMessage, SafeErrorMessage(err)),
			slog.String(KeyErrorType, fmt.Sprintf("%T", err)),
		)
	}
	out = append(out, attrs...)
	l.LogAttrs(ctx, level, msg, out...)
}
