package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"openloc/internal/logging"
)

// logECS runs fn against a fresh ECS-mode logger and returns the single decoded
// record. Assertions go against the decoded map, never a golden string — JSON
// key order isn't guaranteed and a golden string would break on any new field.
func logECS(t *testing.T, fn func(l *slog.Logger)) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	fn(logging.New(logging.Options{ECS: true, Version: "abc123", Environment: "staging", Writer: &buf, Level: slog.LevelDebug}))

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 log line, got %d: %q", len(lines), buf.String())
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, lines[0])
	}
	return got
}

// withInstalled swaps the process logger for the duration of the test so the
// package-level Info/Error helpers can be exercised, then restores it.
func withInstalled(t *testing.T, o logging.Options) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	o.Writer = &buf
	if o.Level == 0 {
		o.Level = slog.LevelDebug
	}
	prev := logging.L()
	logging.Init(o)
	t.Cleanup(func() { logging.SetLogger(prev) })
	return &buf
}

func decodeOne(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("no log output")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, line)
	}
	return got
}

// TestECSRenamesBuiltins pins the ECS field mapping. slog's native keys
// (time/level/msg) must not survive: Elasticsearch's ecs@mappings knows
// @timestamp/log.level/message and nothing about slog's names.
func TestECSRenamesBuiltins(t *testing.T) {
	got := logECS(t, func(l *slog.Logger) { l.Info("hello") })

	for _, k := range []string{logging.KeyTimestamp, logging.KeyLogLevel, logging.KeyMessage} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing ECS key %q; got keys %v", k, keysOf(got))
		}
	}
	for _, k := range []string{"time", "level", "msg"} {
		if _, ok := got[k]; ok {
			t.Errorf("slog built-in key %q leaked into ECS output", k)
		}
	}
	if got[logging.KeyMessage] != "hello" {
		t.Errorf("message = %v, want %q", got[logging.KeyMessage], "hello")
	}
}

// TestECSLevelIsLowercase — ECS convention is lowercase log.level; slog emits
// "INFO". Kibana filters and saved searches are case-sensitive.
func TestECSLevelIsLowercase(t *testing.T) {
	got := logECS(t, func(l *slog.Logger) { l.Warn("careful") })
	if got[logging.KeyLogLevel] != "warn" {
		t.Errorf("log.level = %v, want %q", got[logging.KeyLogLevel], "warn")
	}
}

func TestServiceFieldsAlwaysPresent(t *testing.T) {
	got := logECS(t, func(l *slog.Logger) { l.Info("x") })
	if got[logging.KeyServiceName] != logging.ServiceName {
		t.Errorf("service.name = %v, want %q", got[logging.KeyServiceName], logging.ServiceName)
	}
	if got[logging.KeyServiceVersion] != "abc123" {
		t.Errorf("service.version = %v, want %q", got[logging.KeyServiceVersion], "abc123")
	}
	if got[logging.KeyServiceEnv] != "staging" {
		t.Errorf("service.environment = %v, want %q", got[logging.KeyServiceEnv], "staging")
	}
}

// TestIDsEmitAsStrings is the highest-value test in this file.
//
// The OpenLoc index template is dynamic:false and maps every ID as `keyword`.
// A JSON number against a keyword mapping is a type mismatch, and Elasticsearch
// drops mismatched fields to _source un-indexed — the field silently stops
// being filterable in Kibana, with no error anywhere. If someone "simplifies"
// logging.TenantID to slog.Int64, this test must fail.
func TestIDsEmitAsStrings(t *testing.T) {
	buf := withInstalled(t, logging.Options{ECS: true})
	logging.Info(context.Background(), "m", "act",
		logging.TenantID(7), logging.JobID(42), logging.FileID(99), logging.SegmentID(3),
		logging.ProjectID(11), logging.TMID(5), logging.UserID(8),
	)
	got := decodeOne(t, buf)

	for _, k := range []string{
		logging.KeyTenantID, logging.KeyJobID, logging.KeyFileID, logging.KeySegmentID,
		logging.KeyProjectID, logging.KeyTMID, logging.KeyUserID,
	} {
		v, ok := got[k]
		if !ok {
			t.Errorf("missing %q", k)
			continue
		}
		if _, isStr := v.(string); !isStr {
			t.Errorf("%s = %#v (%T), want a JSON string — numbers are dropped "+
				"to _source un-indexed against a keyword mapping", k, v, v)
		}
	}
	if got[logging.KeyTenantID] != "7" {
		t.Errorf("tenant.id = %v, want %q", got[logging.KeyTenantID], "7")
	}
}

func TestEventActionRecorded(t *testing.T) {
	buf := withInstalled(t, logging.Options{ECS: true})
	logging.Info(context.Background(), "MT phase complete", "pipeline-mt-done")
	got := decodeOne(t, buf)

	if got[logging.KeyEventAction] != "pipeline-mt-done" {
		t.Errorf("event.action = %v, want %q", got[logging.KeyEventAction], "pipeline-mt-done")
	}
	// The human message and the machine slug are separate fields on purpose:
	// dashboards filter on the slug, humans read the message.
	if got[logging.KeyMessage] != "MT phase complete" {
		t.Errorf("message = %v, want the human sentence", got[logging.KeyMessage])
	}
}

func TestErrorExpandsIntoECSFields(t *testing.T) {
	buf := withInstalled(t, logging.Options{ECS: true})
	logging.Error(context.Background(), errors.New("boom"), "translate failed", "pipeline-mt-failed")
	got := decodeOne(t, buf)

	if got[logging.KeyErrorMessage] != "boom" {
		t.Errorf("error.message = %v, want %q", got[logging.KeyErrorMessage], "boom")
	}
	if got[logging.KeyErrorType] == nil || got[logging.KeyErrorType] == "" {
		t.Errorf("error.type missing; got %v", got[logging.KeyErrorType])
	}
	if got[logging.KeyLogLevel] != "error" {
		t.Errorf("log.level = %v, want %q", got[logging.KeyLogLevel], "error")
	}
}

// TestNilErrorOmitsErrorFields — a nil err must not emit error.message:"" ,
// which would make "has an error" unfilterable (every record would match
// `error.message: *`).
func TestNilErrorOmitsErrorFields(t *testing.T) {
	buf := withInstalled(t, logging.Options{ECS: true})
	logging.Error(context.Background(), nil, "invariant violated", "pipeline-bad-state")
	got := decodeOne(t, buf)

	if _, ok := got[logging.KeyErrorMessage]; ok {
		t.Errorf("error.message present for a nil error: %v", got[logging.KeyErrorMessage])
	}
	if _, ok := got[logging.KeyErrorType]; ok {
		t.Errorf("error.type present for a nil error: %v", got[logging.KeyErrorType])
	}
}

func TestScopeFieldsMergeIntoEveryRecord(t *testing.T) {
	buf := withInstalled(t, logging.Options{ECS: true})
	ctx, scope := logging.WithScope(context.Background())
	scope.Set(logging.TenantID(4), logging.UserID(9), logging.Route("/api/projects/{projectID}"))

	// Note: no scope fields passed at the call site — the whole point.
	logging.Info(ctx, "listed projects", "project-list")
	got := decodeOne(t, buf)

	if got[logging.KeyTenantID] != "4" {
		t.Errorf("tenant.id = %v, want %q (scope did not merge)", got[logging.KeyTenantID], "4")
	}
	if got[logging.KeyUserID] != "9" {
		t.Errorf("user.id = %v, want %q", got[logging.KeyUserID], "9")
	}
	if got[logging.KeyRoute] != "/api/projects/{projectID}" {
		t.Errorf("route = %v, want the chi pattern", got[logging.KeyRoute])
	}
}

// TestCallSiteAttrOverridesScopeWithoutDuplicateKey covers both halves of the
// dedupe logic: the per-call value wins, AND the key is emitted once. Duplicate
// JSON keys are ambiguous, so counting occurrences in the raw line matters.
func TestCallSiteAttrOverridesScopeWithoutDuplicateKey(t *testing.T) {
	buf := withInstalled(t, logging.Options{ECS: true})
	ctx, scope := logging.WithScope(context.Background())
	scope.Set(logging.TenantID(1))

	logging.Info(ctx, "m", "act", logging.TenantID(2))

	raw := buf.String()
	if n := strings.Count(raw, `"`+logging.KeyTenantID+`"`); n != 1 {
		t.Errorf("tenant.id appears %d times in output, want 1 (duplicate JSON keys)\n%s", n, raw)
	}
	got := decodeOne(t, buf)
	if got[logging.KeyTenantID] != "2" {
		t.Errorf("tenant.id = %v, want %q (call-site attr should win)", got[logging.KeyTenantID], "2")
	}
}

// TestScopesDoNotLeakAcrossContexts is the tenant-isolation-shaped check. The
// whole reason for a per-context scope rather than process-global state is that
// concurrent requests must not inherit each other's tenant.
func TestScopesDoNotLeakAcrossContexts(t *testing.T) {
	buf := withInstalled(t, logging.Options{ECS: true})

	ctxA, scopeA := logging.WithScope(context.Background())
	scopeA.Set(logging.TenantID(111))
	ctxB, scopeB := logging.WithScope(context.Background())
	scopeB.Set(logging.TenantID(222))

	logging.Info(ctxA, "a", "act")
	first := decodeOne(t, buf)
	if first[logging.KeyTenantID] != "111" {
		t.Fatalf("ctxA tenant.id = %v, want %q", first[logging.KeyTenantID], "111")
	}

	buf.Reset()
	logging.Info(ctxB, "b", "act")
	second := decodeOne(t, buf)
	if second[logging.KeyTenantID] != "222" {
		t.Errorf("ctxB tenant.id = %v, want %q — scope bled across contexts", second[logging.KeyTenantID], "222")
	}
}

// TestNoScopeIsSafe — worker tasks and cmd/seed log from context.Background().
func TestNoScopeIsSafe(t *testing.T) {
	buf := withInstalled(t, logging.Options{ECS: true})
	logging.Info(context.Background(), "no scope here", "act")
	if got := decodeOne(t, buf); got[logging.KeyMessage] != "no scope here" {
		t.Errorf("message = %v", got[logging.KeyMessage])
	}
}

// TestWithScopeIsIdempotent — a background task derived from a request context
// must keep the request's fields rather than start a fresh empty scope.
func TestWithScopeIsIdempotent(t *testing.T) {
	ctx, first := logging.WithScope(context.Background())
	first.Set(logging.TenantID(5))

	ctx2, second := logging.WithScope(ctx)
	if second != first {
		t.Fatal("WithScope created a new scope over an existing one")
	}
	if logging.ScopeFromContext(ctx2).Fields()[logging.KeyTenantID] != "5" {
		t.Error("existing scope fields lost")
	}
}

func TestScopeNilReceiverIsSafe(t *testing.T) {
	var s *logging.Scope
	s.Set(logging.TenantID(1)) // must not panic
	if got := s.Attrs(); got != nil {
		t.Errorf("Attrs() on nil scope = %v, want nil", got)
	}
	if got := s.Fields(); got != nil {
		t.Errorf("Fields() on nil scope = %v, want nil", got)
	}
	if got := logging.ScopeFromContext(context.Background()); got != nil {
		t.Errorf("ScopeFromContext with no scope = %v, want nil", got)
	}
}

// TestScopeConcurrentAccess exists for `go test -race`. Request handlers here
// fan out into goroutines (translationmemory/handler_concordance.go), so a
// middleware still writing identity while those goroutines log would race.
func TestScopeConcurrentAccess(t *testing.T) {
	buf := withInstalled(t, logging.Options{ECS: true})
	ctx, scope := logging.WithScope(context.Background())

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(2)
		go func() { defer wg.Done(); scope.Set(logging.JobID(int64(i))) }()
		go func() { defer wg.Done(); logging.Info(ctx, "concurrent", "act") }()
	}
	wg.Wait()
	if buf.Len() == 0 {
		t.Error("expected log output")
	}
}

// TestDevModeIsReadableNotJSON — local dev must not have to read raw JSON.
func TestDevModeIsReadableNotJSON(t *testing.T) {
	var buf bytes.Buffer
	l := logging.New(logging.Options{ECS: false, Writer: &buf})
	l.Info("readable please", slog.String("k", "v"))

	out := buf.String()
	if json.Valid([]byte(strings.TrimSpace(out))) {
		t.Errorf("dev mode emitted JSON, want text:\n%s", out)
	}
	if !strings.Contains(out, "readable please") {
		t.Errorf("dev output missing the message:\n%s", out)
	}
}

// TestDebugFieldCarriesArbitraryShapes — openloc.debug.* is the escape hatch
// for variable-shape blobs, so it must survive a nested value.
func TestDebugFieldCarriesArbitraryShapes(t *testing.T) {
	buf := withInstalled(t, logging.Options{ECS: true})
	logging.Info(context.Background(), "m", "act",
		logging.Debugf("payload", map[string]any{"nested": []int{1, 2}}),
		logging.Label("retry_attempt", "3"),
	)
	got := decodeOne(t, buf)

	blob, ok := got[logging.DebugPrefix+"payload"].(map[string]any)
	if !ok {
		t.Fatalf("debug.payload = %#v, want a nested object", got[logging.DebugPrefix+"payload"])
	}
	if _, ok := blob["nested"]; !ok {
		t.Errorf("nested key lost: %#v", blob)
	}
	if got[logging.LabelPrefix+"retry_attempt"] != "3" {
		t.Errorf("labels.retry_attempt = %v, want %q", got[logging.LabelPrefix+"retry_attempt"], "3")
	}
}

func TestLangsPair(t *testing.T) {
	buf := withInstalled(t, logging.Options{ECS: true})
	logging.Info(context.Background(), "m", "act", logging.Langs("en", "es-MX")...)
	got := decodeOne(t, buf)

	if got[logging.KeySourceLang] != "en" || got[logging.KeyTargetLang] != "es-MX" {
		t.Errorf("langs = %v/%v, want en/es-MX", got[logging.KeySourceLang], got[logging.KeyTargetLang])
	}
}

func TestECSEnabled(t *testing.T) {
	cases := map[string]bool{
		"fleet":   true,
		"FLEET":   true,
		" fleet ": true,
		"":        false,
		"true":    false,
		"json":    false,
	}
	for in, want := range cases {
		if got := logging.ECSEnabled(in); got != want {
			t.Errorf("ECSEnabled(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	l := logging.New(logging.Options{ECS: true, Writer: &buf, Level: slog.LevelWarn})
	l.Info("suppressed")
	if buf.Len() != 0 {
		t.Errorf("Info emitted at LevelWarn: %s", buf.String())
	}
	l.Warn("kept")
	if buf.Len() == 0 {
		t.Error("Warn suppressed at LevelWarn")
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

type bodyErr struct{}

func (bodyErr) Error() string      { return "vendor returned 400: SECRET-SEGMENT-TEXT" }
func (bodyErr) LogMessage() string { return "vendor returned 400" }

// TestLogSafeErrorsKeepTheirBodyOutOfLogs pins that an error wrapping a vendor
// response body logs its LogMessage, also when other errors wrap it.
func TestLogSafeErrorsKeepTheirBodyOutOfLogs(t *testing.T) {
	buf := withInstalled(t, logging.Options{ECS: true})
	logging.Error(context.Background(), fmt.Errorf("batch 3: %w", bodyErr{}), "failed", "x-failed")
	got := decodeOne(t, buf)

	if msg := got[logging.KeyErrorMessage]; msg != "batch 3: vendor returned 400" {
		t.Errorf("error.message = %v, want the wrapper kept and the body dropped", msg)
	}
	if strings.Contains(buf.String(), "SECRET-SEGMENT-TEXT") {
		t.Errorf("response body reached the log: %s", buf.String())
	}
}
