// Cross-tenant isolation and aggregation semantics against a real database.
// The registry rule: a measure without a test here does not ship — the
// structural tenant pin is only as real as the test that would catch losing it.
package metrics_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/metrics"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

type metricsFixture struct {
	pool *pgxpool.Pool
	q    db.Querier

	tenantA, tenantB   db.Tenant
	userA, userB       db.User
	projectA, projectB db.Project
	fileA, fileB       db.File
	engineA, engineB   db.MtEngine
}

// newMetricsFixture seeds two mirrored tenants:
//
// Tenant A: four de-translations on segments of 10, 7, 5 and 3 source words.
//   - t1: TWO human events (60s + 30s editing, edit distance 5 and 3) — the
//     dedup case — plus one MT event via engine A.
//   - t2: one human event (12s, distance 2), one MT event, and an `unconfirmed`
//     event from editing it again after it was approved.
//   - t3: an MT event plus an `approved` event — a confirm that changed no
//     text, so it carries editing time but no text and no distance.
//   - t4: an MT event plus an `auto_approved` event — the quality check
//     accepting machine output untouched, with no user behind it.
//   - QA: one error + one warning on t1.
//
// t3 and t4 are what hold the two `kind` filters in place. `words_processed`
// must count t3 (a confirm is work even when the text was already right) and
// `segments_by_origin` must exclude all three status kinds (approval is not an
// origin). Without those rows both WHERE clauses can be reverted with the suite
// still green.
//
// Tenant B: one 99-word segment with a human event, an MT event via engine B,
// and a QA error. 99 is chosen to be unmistakable in any leaked aggregate.
func newMetricsFixture(t *testing.T) *metricsFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	f := &metricsFixture{pool: pool, q: q}
	f.tenantA = testutil.SeedTenant(t, q, "MetricsTenantA")
	f.tenantB = testutil.SeedTenant(t, q, "MetricsTenantB")
	f.userA = testutil.SeedUserWithRole(t, q, f.tenantA.ID, "metrics-a@test.com", "pass", "owner")
	f.userB = testutil.SeedUserWithRole(t, q, f.tenantB.ID, "metrics-b@test.com", "pass", "owner")
	f.engineA = testutil.SeedMTEngine(t, q, f.tenantA.ID, "Engine Alpha", "deepl")
	f.engineB = testutil.SeedMTEngine(t, q, f.tenantB.ID, "Engine Beta", "deepl")

	f.projectA = testutil.SeedProject(t, q, f.tenantA.ID, "Metrics Alpha", "en", []string{"de"})
	f.fileA = testutil.SeedFile(t, q, f.projectA.ID, "a.txt", "txt")
	s1 := testutil.SeedSegment(t, q, f.fileA.ID, "segment one source", 1)
	s2 := testutil.SeedSegment(t, q, f.fileA.ID, "segment two source", 2)
	s3 := testutil.SeedSegment(t, q, f.fileA.ID, "segment three source", 3)
	s4 := testutil.SeedSegment(t, q, f.fileA.ID, "segment four source", 4)
	setWords(t, pool, s1.ID, 10)
	setWords(t, pool, s2.ID, 7)
	setWords(t, pool, s3.ID, 5)
	setWords(t, pool, s4.ID, 3)
	t1 := testutil.SeedTranslation(t, q, s1.ID, "de", "needs_review")
	t2 := testutil.SeedTranslation(t, q, s2.ID, "de", "needs_review")
	t3 := testutil.SeedTranslation(t, q, s3.ID, "de", "approved")
	t4 := testutil.SeedTranslation(t, q, s4.ID, "de", "auto_approved")

	f.projectB = testutil.SeedProject(t, q, f.tenantB.ID, "Metrics Beta", "en", []string{"de"})
	f.fileB = testutil.SeedFile(t, q, f.projectB.ID, "b.txt", "txt")
	s9 := testutil.SeedSegment(t, q, f.fileB.ID, "tenant b segment", 1)
	setWords(t, pool, s9.ID, 99)
	t9 := testutil.SeedTranslation(t, q, s9.ID, "de", "needs_review")

	// Tenant A events.
	history(t, q, t1.ID, "human", &f.userA.ID, nil, ms(60000), dist(5))
	history(t, q, t1.ID, "human", &f.userA.ID, nil, ms(30000), dist(3))
	history(t, q, t1.ID, "mt", nil, &f.engineA.ID, nil, nil)
	history(t, q, t2.ID, "human", &f.userA.ID, nil, ms(12000), dist(2))
	history(t, q, t2.ID, "mt", nil, &f.engineA.ID, nil, nil)
	statusHistory(t, q, t2.ID, "unconfirmed", &f.userA.ID, nil)
	history(t, q, t3.ID, "mt", nil, &f.engineA.ID, nil, nil)
	statusHistory(t, q, t3.ID, "approved", &f.userA.ID, ms(18000))
	history(t, q, t4.ID, "mt", nil, &f.engineA.ID, nil, nil)
	statusHistory(t, q, t4.ID, "auto_approved", nil, nil)
	qa(t, q, t1.ID, "terminology", "error")
	qa(t, q, t1.ID, "numbers", "warning")

	// Tenant B events — must never surface in tenant A results.
	history(t, q, t9.ID, "human", &f.userB.ID, nil, ms(999999), dist(50))
	history(t, q, t9.ID, "mt", nil, &f.engineB.ID, nil, nil)
	qa(t, q, t9.ID, "terminology", "error")

	_ = ctx
	return f
}

func setWords(t *testing.T, pool *pgxpool.Pool, segmentID int64, words int) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		"UPDATE segments SET word_count = $1 WHERE id = $2", words, segmentID); err != nil {
		t.Fatalf("set word_count: %v", err)
	}
}

// statusHistory writes one of the status events. They carry no text and no
// edit distance in production — that is what keeps them out of the
// edit-distance baseline — so the fixture must not give them either.
func statusHistory(t *testing.T, q db.Querier, translationID int64, kind string, userID *int64, editMs *int64) {
	t.Helper()
	if _, err := q.InsertTranslationHistory(context.Background(), db.InsertTranslationHistoryParams{
		TranslationID: translationID,
		Kind:          db.TranslationHistoryKind(kind),
		CreatedBy:     userID,
		EditingTimeMs: editMs,
		Details:       []byte("{}"),
	}); err != nil {
		t.Fatalf("insert %s history: %v", kind, err)
	}
}

func history(t *testing.T, q db.Querier, translationID int64, kind string, userID, engineID *int64, editMs *int64, editDist *int32) {
	t.Helper()
	text := "text"
	if _, err := q.InsertTranslationHistory(context.Background(), db.InsertTranslationHistoryParams{
		TranslationID: translationID,
		Kind:          db.TranslationHistoryKind(kind),
		Text:          &text,
		CreatedBy:     userID,
		MtEngineID:    engineID,
		EditingTimeMs: editMs,
		EditDistance:  editDist,
		Details:       []byte("{}"),
	}); err != nil {
		t.Fatalf("insert history: %v", err)
	}
}

func qa(t *testing.T, q db.Querier, translationID int64, checkType, severity string) {
	t.Helper()
	if err := q.UpsertQAResult(context.Background(), db.UpsertQAResultParams{
		TranslationID: translationID, CheckType: checkType, Severity: severity, Message: "m",
	}); err != nil {
		t.Fatalf("upsert qa: %v", err)
	}
}

var (
	ms   = ptr.To[int64]
	dist = ptr.To[int32]
)

func (f *metricsFixture) run(t *testing.T, tenantID int64, req metrics.Request) *metrics.Result {
	t.Helper()
	if req.From.IsZero() {
		req.From = time.Now().AddDate(0, 0, -1)
		req.To = time.Now().AddDate(0, 0, 1)
	}
	res, err := metrics.Run(context.Background(), f.pool, tenantID, req)
	if err != nil {
		t.Fatalf("run %s: %v", req.Measure, err)
	}
	return res
}

// asInt64 tolerates the numeric types pgx hands back per aggregate.
func asInt64(t *testing.T, v any) int64 {
	t.Helper()
	switch n := v.(type) {
	case int64:
		return n
	case int32:
		return int64(n)
	case float64:
		return int64(n)
	default:
		t.Fatalf("unexpected numeric type %T (%v)", v, v)
		return 0
	}
}

func TestIntegrationMetricsWordsProcessedDedupesAndIsolates(t *testing.T) {
	f := newMetricsFixture(t)

	// Tenant A: t1 has TWO human events but must count once → 10+7+5=22 words
	// over 3 segments. The dedup assertion is what kills a naive single-layer
	// SUM (which would report 32 words / 4 segments).
	res := f.run(t, f.tenantA.ID, metrics.Request{Measure: "words_processed"})
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	if got := asInt64(t, res.Rows[0]["words"]); got != 22 {
		t.Errorf("words = %d, want 22 (10+7 edited, +5 confirmed unchanged)", got)
	}
	// 3, not 2: t3 was confirmed without a text change, which the measure
	// counts as work. Narrowing the filter back to kind='human' drops it.
	if got := asInt64(t, res.Rows[0]["segments"]); got != 3 {
		t.Errorf("segments = %d, want 3", got)
	}

	// Tenant B sees exactly its own 99, and A's 22 never includes it.
	resB := f.run(t, f.tenantB.ID, metrics.Request{Measure: "words_processed"})
	if got := asInt64(t, resB.Rows[0]["words"]); got != 99 {
		t.Errorf("tenant B words = %d, want 99", got)
	}
}

func TestIntegrationMetricsMTEngineUsageByEngine(t *testing.T) {
	f := newMetricsFixture(t)

	res := f.run(t, f.tenantA.ID, metrics.Request{
		Measure:    "mt_engine_usage",
		Dimensions: []string{"mt_engine"},
		Filters:    map[string]string{"target_language": "DE"}, // exercises normalization
	})
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	row := res.Rows[0]
	if row["mt_engine"] != "Engine Alpha" {
		t.Errorf("engine = %v (tenant B's engine must never appear)", row["mt_engine"])
	}
	// All four tenant-A segments were machine translated: 10+7+5+3.
	if got := asInt64(t, row["segments"]); got != 4 {
		t.Errorf("segments = %d, want 4", got)
	}
	if got := asInt64(t, row["words"]); got != 25 {
		t.Errorf("words = %d, want 25", got)
	}
}

// stepEdit writes a human confirm made inside a workflow step, on a fresh
// segment of the given word count.
func stepEdit(t *testing.T, f *metricsFixture, fileID int64, userID, assignmentID int64, words int, pos int32) {
	t.Helper()
	seg := testutil.SeedSegment(t, f.q, fileID, fmt.Sprintf("step seg %d", pos), pos)
	setWords(t, f.pool, seg.ID, words)
	tr := testutil.SeedTranslation(t, f.q, seg.ID, "de", "approved")
	text := "t"
	if _, err := f.q.InsertTranslationHistory(context.Background(), db.InsertTranslationHistoryParams{
		TranslationID: tr.ID, Kind: db.TranslationHistoryKindHuman, Text: &text,
		CreatedBy: &userID, JobStepAssignmentID: &assignmentID, Details: []byte("{}"),
	}); err != nil {
		t.Fatalf("insert step history: %v", err)
	}
}

// Work splits by the workflow step it was done in. Steps are named per
// settings profile, so the dimension reads the step's name; work outside any
// step comes back empty, and another tenant's step never shows.
func TestIntegrationMetricsWorkflowStepDimension(t *testing.T) {
	f := newMetricsFixture(t)

	jobA := testutil.SeedJob(t, f.q, f.fileA.ID, "de")
	stepsA := testutil.SeedWorkflow(t, f.q, f.projectA.SettingsProfileID, jobA.ID,
		testutil.WorkflowStepSpec{Name: "Translation"}, testutil.WorkflowStepSpec{Name: "Review"})
	stepEdit(t, f, f.fileA.ID, f.userA.ID, stepsA[0], 10, 600)
	stepEdit(t, f, f.fileA.ID, f.userA.ID, stepsA[1], 5, 601)

	jobB := testutil.SeedJob(t, f.q, f.fileB.ID, "de")
	stepsB := testutil.SeedWorkflow(t, f.q, f.projectB.SettingsProfileID, jobB.ID,
		testutil.WorkflowStepSpec{Name: "Tenant B secret step"})
	stepEdit(t, f, f.fileB.ID, f.userB.ID, stepsB[0], 7, 602)

	res := f.run(t, f.tenantA.ID, metrics.Request{Measure: "words_processed", Dimensions: []string{"workflow_step"}})
	got := map[string]int64{}
	for _, row := range res.Rows {
		step, _ := row["workflow_step"].(string)
		got[step] = asInt64(t, row["words"])
	}
	if got["Translation"] != 10 || got["Review"] != 5 {
		t.Errorf("words by workflow_step = %v, want Translation 10 and Review 5", got)
	}
	if _, ok := got[""]; !ok {
		t.Errorf("work outside a step should come back as an empty step, got %v", got)
	}
	if _, leaked := got["Tenant B secret step"]; leaked {
		t.Errorf("tenant A sees tenant B's step: %v", got)
	}
}

// An ignored QA finding is "no issue" everywhere else in the product, so the
// measure does not count it. The fixture gives tenant A two findings.
func TestIntegrationMetricsQAIssuesExcludeIgnored(t *testing.T) {
	f := newMetricsFixture(t)
	if _, err := f.pool.Exec(context.Background(), `
		UPDATE qa_results qr SET ignored = true
		FROM translations t JOIN segments s ON s.id = t.segment_id
		WHERE qr.translation_id = t.id AND s.file_id = $1 AND qr.check_type = 'numbers'`, f.fileA.ID); err != nil {
		t.Fatalf("ignore finding: %v", err)
	}

	res := f.run(t, f.tenantA.ID, metrics.Request{Measure: "qa_issues", Dimensions: []string{"check_type"}})
	if len(res.Rows) != 1 || res.Rows[0]["check_type"] != "terminology" || asInt64(t, res.Rows[0]["issues"]) != 1 {
		t.Errorf("qa_issues by check_type = %+v, want only terminology: 1", res.Rows)
	}
}

// `user` is a reserved word in Postgres: unquoted, it reads as CURRENT_USER and
// every row is credited to the database role. Pins the alias quoting on the
// deduplicated (two-layer) measures and on a folded tail.
func TestIntegrationMetricsUserDimensionNamesThePerson(t *testing.T) {
	f := newMetricsFixture(t)

	res := f.run(t, f.tenantA.ID, metrics.Request{Measure: "words_processed", Dimensions: []string{"user"}})
	if len(res.Rows) != 1 || res.Rows[0]["user"] != f.userA.Name {
		t.Fatalf("words_processed by user = %+v, want one row for %q", res.Rows, f.userA.Name)
	}

	// Three users and a budget of two rows: the top user keeps a row and the
	// other two fold into "Other", which rewrites the dimension column.
	// userA already has 2 minutes of editing; these two have less.
	for i, email := range []string{"metrics-a2@test.com", "metrics-a3@test.com"} {
		u := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, email, "pass", "linguist")
		seg := testutil.SeedSegment(t, f.q, f.fileA.ID, fmt.Sprintf("fold seg %d", i), int32(700+i))
		tr := testutil.SeedTranslation(t, f.q, seg.ID, "de", "approved")
		history(t, f.q, tr.ID, "human", &u.ID, nil, ms(6000), dist(1))
	}
	folded := f.run(t, f.tenantA.ID, metrics.Request{Measure: "editing_time", Dimensions: []string{"user"}, RowBudget: 2})
	if !folded.Folded || len(folded.Rows) != 2 {
		t.Fatalf("editing_time by user with a 2-row budget = %+v (folded %v), want the top user plus Other", folded.Rows, folded.Folded)
	}
	if got := folded.Rows[0]["user"]; got != f.userA.Name {
		t.Errorf("folded top row names %q, want %q", got, f.userA.Name)
	}
	if got := folded.Rows[1]["user"]; got != metrics.OtherBucket {
		t.Errorf("folded tail row names %q, want %q", got, metrics.OtherBucket)
	}
}

func TestIntegrationMetricsEditingTimeAndEffort(t *testing.T) {
	f := newMetricsFixture(t)

	res := f.run(t, f.tenantA.ID, metrics.Request{Measure: "editing_time", Dimensions: []string{"user"}})
	if len(res.Rows) != 1 || res.Rows[0]["user"] != f.userA.Name {
		t.Fatalf("rows = %+v", res.Rows)
	}
	// (60000+30000+12000+18000) / 60000 = 2 min. Events are additive — no
	// dedup — and a confirm that changed nothing still took someone's time.
	if got := res.Rows[0]["minutes"].(float64); got != 2 {
		t.Errorf("minutes = %v, want 2", got)
	}
	if got := asInt64(t, res.Rows[0]["events"]); got != 4 {
		t.Errorf("events = %d, want 4", got)
	}

	effort := f.run(t, f.tenantA.ID, metrics.Request{Measure: "post_edit_effort"})
	// avg(5, 3, 2) = 3.3 — tenant B's distance-50 edit must not pull this up,
	// and neither may t3's `approved` row: it rewrote nothing, so averaging a
	// 0 in would understate how much rewriting the humans actually did.
	if got := effort.Rows[0]["avg_edit_distance"].(float64); got != 3.3 {
		t.Errorf("avg_edit_distance = %v, want 3.3", got)
	}
	if got := asInt64(t, effort.Rows[0]["events"]); got != 3 {
		t.Errorf("events = %d, want 3 (status rows carry no distance)", got)
	}
	// A chart must know which columns it may not stack or sum.
	if !slices.Equal(effort.Averages, []string{"avg_edit_distance"}) {
		t.Errorf("Averages = %v, want [avg_edit_distance]", effort.Averages)
	}
	if res.Averages != nil {
		t.Errorf("editing_time Averages = %v, want none", res.Averages)
	}
	// Every value column says what it counts, so a chart can print "55 words".
	if got := effort.Units["avg_edit_distance"]; got != "edit distance" {
		t.Errorf("post_edit_effort unit = %q, want %q", got, "edit distance")
	}
	if got := res.Units["minutes"]; got != "minutes" {
		t.Errorf("editing_time unit = %q, want %q", got, "minutes")
	}
}

func TestIntegrationMetricsSegmentsByOriginAndQAIssues(t *testing.T) {
	f := newMetricsFixture(t)

	origin := f.run(t, f.tenantA.ID, metrics.Request{Measure: "segments_by_origin", Dimensions: []string{"kind"}})
	byKind := map[string]int64{}
	for _, r := range origin.Rows {
		byKind[r["kind"].(string)] = asInt64(t, r["segments"])
	}
	if byKind["human"] != 2 || byKind["mt"] != 4 {
		t.Errorf("byKind = %v, want human:2 mt:4", byKind)
	}
	// Exactly two: a tenant leak would add a kind, and so would dropping the
	// status kinds from the measure's exclusion list — approved,
	// auto_approved and unconfirmed all sit in this fixture and none of them
	// is an origin.
	if len(byKind) != 2 {
		t.Errorf("unexpected kinds (tenant leak, or a status kind counted as an origin?): %v", byKind)
	}

	issues := f.run(t, f.tenantA.ID, metrics.Request{Measure: "qa_issues", Dimensions: []string{"severity"}})
	bySev := map[string]int64{}
	for _, r := range issues.Rows {
		bySev[r["severity"].(string)] = asInt64(t, r["issues"])
	}
	// Tenant B's error would make this 2.
	if bySev["error"] != 1 || bySev["warning"] != 1 {
		t.Errorf("bySev = %v, want error:1 warning:1", bySev)
	}
}

// A time grain yields chronological rows with ISO dates — the shape charts eat.
func TestIntegrationMetricsTimeGrainShape(t *testing.T) {
	f := newMetricsFixture(t)

	// The current month, exactly: the default now±1 day spans two months on
	// the first and last day of each one.
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	res := f.run(t, f.tenantA.ID, metrics.Request{
		Measure: "words_processed", Dimensions: []string{"month"},
		From: monthStart, To: monthStart.AddDate(0, 1, 0),
	})
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	month, ok := res.Rows[0]["month"].(string)
	if !ok || len(month) != 10 || month[7] != '-' {
		t.Errorf("month = %v, want YYYY-MM-DD string", res.Rows[0]["month"])
	}
	if res.Definition == "" || res.Columns[0] != "month" {
		t.Errorf("result metadata incomplete: %+v", res)
	}
}

// The range end is exclusive: an end that falls exactly on a bucket boundary
// (dashboards end at tomorrow's midnight) must not add an empty bucket for the
// period that starts there.
func TestIntegrationMetricsExclusiveEndAddsNoBucket(t *testing.T) {
	f := newMetricsFixture(t)
	now := time.Now().UTC()
	day := now.Truncate(24 * time.Hour)
	monday := day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		grain    string
		from, to time.Time
	}{
		{"day", day, day.AddDate(0, 0, 1)},
		{"week", monday, monday.AddDate(0, 0, 7)},
		{"month", month, month.AddDate(0, 1, 0)},
	} {
		res := f.run(t, f.tenantA.ID, metrics.Request{
			Measure: "words_processed", Dimensions: []string{c.grain}, From: c.from, To: c.to,
		})
		if len(res.Rows) != 1 {
			t.Errorf("%s grain over [%s, %s): want 1 bucket, got %d: %+v",
				c.grain, c.from.Format(time.DateOnly), c.to.Format(time.DateOnly), len(res.Rows), res.Rows)
		}
	}
}

// --- AI engines: separate from vendor MT, by design -----------------------------

// aiHistory attributes a history row to an AI ENGINE rather than an mt_engines
// row — the split that keeps "DeepL" and "GPT-4o behind prompt X" out of one
// dimension.
func aiHistory(t *testing.T, q db.Querier, translationID int64, kind string, aiEngineID int64) {
	t.Helper()
	text := "text"
	if _, err := q.InsertTranslationHistory(context.Background(), db.InsertTranslationHistoryParams{
		TranslationID: translationID,
		Kind:          db.TranslationHistoryKind(kind),
		Text:          &text,
		AiEngineID:    &aiEngineID,
		Details:       []byte("{}"),
	}); err != nil {
		t.Fatalf("insert ai history: %v", err)
	}
}

func aiEngine(t *testing.T, q db.Querier, tenantID int64, name, purpose string) db.AiEngine {
	t.Helper()
	sys, err := q.CreateAIEngine(context.Background(), db.CreateAIEngineParams{
		TenantID: tenantID, Name: name, Purpose: purpose,
	})
	if err != nil {
		t.Fatalf("seed ai engine: %v", err)
	}
	return sys
}

// The whole point of the split: AI volume charts on its own, and vendor MT is
// unaffected by it.
func TestIntegrationMetricsAIEngineUsageIsSeparateFromMTEngines(t *testing.T) {
	f := newMetricsFixture(t)

	mtSys := aiEngine(t, f.q, f.tenantA.ID, "GPT MT", "mt")
	apeSys := aiEngine(t, f.q, f.tenantA.ID, "Claude APE", "qe_ape")
	otherTenant := aiEngine(t, f.q, f.tenantB.ID, "Their System", "mt")

	seg := testutil.SeedSegment(t, f.q, f.fileA.ID, "hello there world", 9)
	setWords(t, f.pool, seg.ID, 3)
	tr := testutil.SeedTranslation(t, f.q, seg.ID, "de", "needs_review")
	aiHistory(t, f.q, tr.ID, "mt", mtSys.ID)
	aiHistory(t, f.q, tr.ID, "ape", apeSys.ID)

	// A row in tenant B that must never surface.
	segB := testutil.SeedSegment(t, f.q, f.fileB.ID, "leak", 9)
	trB := testutil.SeedTranslation(t, f.q, segB.ID, "de", "needs_review")
	aiHistory(t, f.q, trB.ID, "mt", otherTenant.ID)

	res := f.run(t, f.tenantA.ID, metrics.Request{
		Measure:    "ai_engine_usage",
		Dimensions: []string{"ai_engine"},
	})
	names := map[string]bool{}
	for _, row := range res.Rows {
		names[row["ai_engine"].(string)] = true
	}
	if !names["GPT MT"] || !names["Claude APE"] {
		t.Errorf("ai engines = %v, want both of tenant A's", names)
	}
	if names["Their System"] {
		t.Error("tenant B's AI engine leaked into tenant A's chart")
	}

	// Splitting MT from post-editing is the reason ai_purpose exists.
	byPurpose := f.run(t, f.tenantA.ID, metrics.Request{
		Measure:    "ai_engine_usage",
		Dimensions: []string{"ai_purpose"},
	})
	purposes := map[string]int64{}
	for _, row := range byPurpose.Rows {
		purposes[row["ai_purpose"].(string)] = asInt64(t, row["segments"])
	}
	if purposes["mt"] != 1 || purposes["qe_ape"] != 1 {
		t.Errorf("by purpose = %v, want one each", purposes)
	}

	// And the vendor-engine measure is untouched by any of it: mixing the two
	// would silently change what an existing chart answers.
	vendor := f.run(t, f.tenantA.ID, metrics.Request{
		Measure:    "mt_engine_usage",
		Dimensions: []string{"mt_engine"},
	})
	for _, row := range vendor.Rows {
		if name, _ := row["mt_engine"].(string); name == "GPT MT" || name == "" {
			t.Errorf("AI engine appeared in mt_engine_usage: %v", row)
		}
	}
}

func TestIntegrationMetricsAITokenUsageIsTenantScoped(t *testing.T) {
	f := newMetricsFixture(t)
	ctx := context.Background()

	sys := aiEngine(t, f.q, f.tenantA.ID, "GPT MT", "mt")
	usage := func(tenantID, projectID int64, name string, in, out int64) {
		t.Helper()
		if err := f.q.InsertAIUsageEvent(ctx, db.InsertAIUsageEventParams{
			TenantID: tenantID, AiEngineID: &sys.ID, AiEngineName: name, Purpose: "mt",
			Provider: "openai", Model: "gpt-4o", ProjectID: &projectID,
			InputTokens: in, OutputTokens: out,
		}); err != nil {
			t.Fatalf("insert usage: %v", err)
		}
	}
	usage(f.tenantA.ID, f.projectA.ID, "GPT MT", 100, 20)
	usage(f.tenantA.ID, f.projectA.ID, "GPT MT", 50, 10)
	usage(f.tenantB.ID, f.projectB.ID, "Their System", 999, 999)

	res := f.run(t, f.tenantA.ID, metrics.Request{
		Measure:    "ai_token_usage",
		Dimensions: []string{"ai_engine"},
	})
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %+v (tenant B's spend must not appear)", res.Rows)
	}
	row := res.Rows[0]
	if row["ai_engine"] != "GPT MT" {
		t.Errorf("ai_engine = %v", row["ai_engine"])
	}
	// Tokens are summed per CALL, and calls are counted — a thousand words may
	// be one call or thirty, which is why this measure exists beside volume.
	if got := asInt64(t, row["input_tokens"]); got != 150 {
		t.Errorf("input_tokens = %d, want 150", got)
	}
	if got := asInt64(t, row["total_tokens"]); got != 180 {
		t.Errorf("total_tokens = %d, want 180", got)
	}
	if got := asInt64(t, row["calls"]); got != 2 {
		t.Errorf("calls = %d, want 2", got)
	}
	// total_tokens is input plus output: a chart adding it to them counts twice.
	if !slices.Equal(res.Derived, []string{"total_tokens"}) {
		t.Errorf("Derived = %v, want [total_tokens]", res.Derived)
	}
}

// --- Auto-grain ----------------------------------------------------------------

// backdate moves a history row so a range spans several buckets. The fixture
// writes everything at now(), which is one bucket at every grain.
func backdate(t *testing.T, pool *pgxpool.Pool, historyID int64, daysAgo int) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		"UPDATE translation_history SET created_at = now() - make_interval(days => $1) WHERE id = $2",
		daysAgo, historyID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
}

// A range that cannot carry the requested resolution widens the bucket instead
// of dropping rows — the alternative, folding rows afterwards, cannot be right
// for averages and would need per-column additivity rules.
func TestIntegrationMetricsAutoGrainWidensRatherThanTruncating(t *testing.T) {
	f := newMetricsFixture(t)
	ctx := context.Background()

	// Four days of activity inside ONE week: 4 daily buckets, 1 weekly bucket.
	for i, daysAgo := range []int{0, 1, 2, 3} {
		seg := testutil.SeedSegment(t, f.q, f.fileA.ID, fmt.Sprintf("auto grain %d", i), int32(100+i))
		setWords(t, f.pool, seg.ID, 5)
		tr := testutil.SeedTranslation(t, f.q, seg.ID, "de", "needs_review")
		text := "t"
		h, err := f.q.InsertTranslationHistory(ctx, db.InsertTranslationHistoryParams{
			TranslationID: tr.ID, Kind: db.TranslationHistoryKindHuman, Text: &text,
			CreatedBy: &f.userA.ID, Details: []byte("{}"),
		})
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		backdate(t, f.pool, h.ID, daysAgo)
	}

	// A budget of 3 cannot hold 4 daily buckets, so the layer steps to weekly.
	res := f.run(t, f.tenantA.ID, metrics.Request{
		Measure: "words_processed", Dimensions: []string{"day"}, RowBudget: 3,
		// Wide enough to include the backdated rows; the fixture's default
		// range is a single day either side of now.
		From: time.Now().AddDate(0, 0, -10), To: time.Now().AddDate(0, 0, 1),
	})
	if res.Grain != "week" && res.Grain != "month" {
		t.Fatalf("grain = %q, want a WIDER bucket than the requested day", res.Grain)
	}
	if res.Truncated {
		t.Error("widening should make truncation unnecessary")
	}
	if len(res.Rows) > 3 {
		t.Errorf("rows = %d, over the budget it was meant to fit", len(res.Rows))
	}
	// The whole point: widening re-aggregates in SQL, so the answer is the same
	// question at a coarser resolution — not a sample of it. Compared against
	// the un-widened run rather than a fixed number, so the invariant holds
	// whatever the fixture seeds.
	wide := f.run(t, f.tenantA.ID, metrics.Request{
		Measure: "words_processed", Dimensions: []string{"day"}, RowBudget: 500,
		From: time.Now().AddDate(0, 0, -10), To: time.Now().AddDate(0, 0, 1),
	})
	sum := func(rows []map[string]any, col string) int64 {
		var n int64
		for _, row := range rows {
			n += asInt64(t, row[col])
		}
		return n
	}
	if got, want := sum(res.Rows, "words"), sum(wide.Rows, "words"); got != want {
		t.Errorf("words after widening = %d, want %d — widening must not lose volume", got, want)
	}
	if len(wide.Rows) <= len(res.Rows) {
		t.Errorf("the un-widened run returned %d rows and the widened one %d; the test is not exercising a widen",
			len(wide.Rows), len(res.Rows))
	}
	// The axis moved, so the column did too; a caller that kept plotting "day"
	// would draw nothing.
	if _, ok := res.Rows[0]["day"]; ok {
		t.Error("result still carries a 'day' column after widening")
	}
}

// A budget that fits leaves the request alone — widening is a fallback, not a
// policy.
func TestIntegrationMetricsAutoGrainLeavesAFittingRequestAlone(t *testing.T) {
	f := newMetricsFixture(t)
	res := f.run(t, f.tenantA.ID, metrics.Request{
		Measure: "words_processed", Dimensions: []string{"day"}, RowBudget: 500,
	})
	if res.Grain != "day" {
		t.Errorf("grain = %q, want the requested day", res.Grain)
	}
}

// Nothing to widen and nothing to fold: two categorical dimensions have no
// wider bucket and no single tail to collapse into "Other", so the only honest
// answer over budget is a truncated one that SAYS it is truncated.
func TestIntegrationMetricsCategoricalOverflowTruncatesHonestly(t *testing.T) {
	f := newMetricsFixture(t)
	req := metrics.Request{Measure: "segments_by_origin", Dimensions: []string{"kind", "target_language"}}

	// The precondition that makes the rest mean anything: unbudgeted, the
	// answer really is bigger than the budget below.
	full := f.run(t, f.tenantA.ID, req)
	if len(full.Rows) < 2 {
		t.Fatalf("fixture yields %d rows; the test needs more than the budget of 1", len(full.Rows))
	}
	if full.Truncated {
		t.Fatal("the unbudgeted run is already marked truncated")
	}

	req.RowBudget = 1
	res := f.run(t, f.tenantA.ID, req)
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want the budget of 1", len(res.Rows))
	}
	if !res.Truncated {
		t.Error("dropped rows without saying so: Truncated is false")
	}
	if res.Folded {
		t.Error("two dimensions cannot fold, yet the result claims it did")
	}
	if res.Grain != "" {
		t.Errorf("grain = %q, want empty for a request with no time dimension", res.Grain)
	}
}

// --- Gap filling ---------------------------------------------------------------

// A day with no activity must still appear, at zero. Without this a line or
// area chart joins the points on either side of the gap, drawing a slope
// through days that never happened.
func TestIntegrationMetricsFillsEmptyBuckets(t *testing.T) {
	f := newMetricsFixture(t)
	ctx := context.Background()

	// One event 5 days ago. The fixture's own events are at now(), so days
	// 1-4 ago are genuinely empty.
	seg := testutil.SeedSegment(t, f.q, f.fileA.ID, "gap filling", 200)
	setWords(t, f.pool, seg.ID, 4)
	tr := testutil.SeedTranslation(t, f.q, seg.ID, "de", "needs_review")
	text := "t"
	h, err := f.q.InsertTranslationHistory(ctx, db.InsertTranslationHistoryParams{
		TranslationID: tr.ID, Kind: db.TranslationHistoryKindHuman, Text: &text,
		CreatedBy: &f.userA.ID, Details: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	backdate(t, f.pool, h.ID, 5)

	res := f.run(t, f.tenantA.ID, metrics.Request{
		Measure: "words_processed", Dimensions: []string{"day"},
		From: time.Now().AddDate(0, 0, -6), To: time.Now(),
	})
	// Seven calendar days in the range, two of which have data.
	if len(res.Rows) < 6 {
		t.Fatalf("rows = %d, want one per day in the range — empty days are missing", len(res.Rows))
	}
	var zeros, nonZero int
	for _, row := range res.Rows {
		if asInt64(t, row["words"]) == 0 {
			zeros++
		} else {
			nonZero++
		}
	}
	if zeros == 0 {
		t.Error("no zero-filled days; the gap is still a hole")
	}
	if nonZero == 0 {
		t.Error("filling replaced the real data")
	}
	// Buckets arrive in time order, which is what a time axis needs; the
	// categorical ORDER BY value DESC would scramble a line chart.
	prev := ""
	for _, row := range res.Rows {
		d, _ := row["day"].(string)
		if d < prev {
			t.Fatalf("days out of order: %q after %q", d, prev)
		}
		prev = d
	}
}

// The range is [from, to): a `to` that sits exactly on a bucket boundary — the
// "end of today" every dashboard resolves to — must not add the next bucket.
func TestIntegrationMetricsFilledRangeExcludesItsEnd(t *testing.T) {
	f := newMetricsFixture(t)
	aug1 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		grain string
		to    time.Time
		want  []string
	}{
		{"month", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), []string{"2026-08-01"}},
		{"day", time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC), []string{"2026-08-01", "2026-08-02", "2026-08-03"}},
	}
	for _, c := range cases {
		res := f.run(t, f.tenantA.ID, metrics.Request{
			Measure: "words_processed", Dimensions: []string{c.grain}, From: aug1, To: c.to,
		})
		var got []string
		for _, row := range res.Rows {
			got = append(got, row[c.grain].(string))
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s buckets = %v, want %v", c.grain, got, c.want)
		}
	}
}

// An empty bucket has no mean. Filling one with 0 would read as "edits took no
// effort" where the truth is "there were no edits".
func TestIntegrationMetricsDoesNotInventZeroAverages(t *testing.T) {
	f := newMetricsFixture(t)
	ctx := context.Background()

	// One edit 5 days ago; the fixture's own edits are at now(), so days 1-4
	// ago are empty by construction rather than by luck.
	seg := testutil.SeedSegment(t, f.q, f.fileA.ID, "average gap", 201)
	tr := testutil.SeedTranslation(t, f.q, seg.ID, "de", "needs_review")
	text := "t"
	h, err := f.q.InsertTranslationHistory(ctx, db.InsertTranslationHistoryParams{
		TranslationID: tr.ID, Kind: db.TranslationHistoryKindHuman, Text: &text,
		CreatedBy: &f.userA.ID, EditDistance: dist(4), Details: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	backdate(t, f.pool, h.ID, 5)

	res := f.run(t, f.tenantA.ID, metrics.Request{
		Measure: "post_edit_effort", Dimensions: []string{"day"},
		From: time.Now().AddDate(0, 0, -6), To: time.Now(),
	})
	// Seven calendar days in the range: gap filling must produce each one.
	if len(res.Rows) < 6 {
		t.Fatalf("rows = %d, want one per day in the range — empty days are missing", len(res.Rows))
	}
	var empty, withData int
	for _, row := range res.Rows {
		if asInt64(t, row["events"]) == 0 {
			empty++
			// The additive column is zero-filled; the mean must stay absent.
			if row["avg_edit_distance"] != nil {
				t.Errorf("empty day %v has average %#v, want none", row["day"], row["avg_edit_distance"])
			}
		} else {
			withData++
			if row["avg_edit_distance"] == nil {
				t.Errorf("day %v has events but no average", row["day"])
			}
		}
	}
	if empty == 0 {
		t.Fatal("no empty day in a range built to have four")
	}
	if withData < 2 {
		t.Fatalf("days with edits = %d, want the backdated one and today", withData)
	}
}

// Two dimensions are left sparse on purpose: filling would mean the cross
// product of buckets × values, and the client already fills within each series.
func TestIntegrationMetricsDoesNotFillMultiDimensionQueries(t *testing.T) {
	f := newMetricsFixture(t)
	res := f.run(t, f.tenantA.ID, metrics.Request{
		Measure: "words_processed", Dimensions: []string{"day", "user"},
		From: time.Now().AddDate(0, 0, -6), To: time.Now(),
	})
	for _, row := range res.Rows {
		if row["user"] == nil || row["user"] == "" {
			t.Errorf("row with no user: %v — filling produced a dimensionless bucket", row)
		}
	}
}

// --- Top-N fold ------------------------------------------------------------------

// Overflow on a categorical dimension keeps the biggest values and aggregates
// the rest into one row. Truncation would drop the tail and say nothing about
// it; folding keeps the total honest.
func TestIntegrationMetricsFoldsCategoricalTailIntoOther(t *testing.T) {
	f := newMetricsFixture(t)
	ctx := context.Background()

	// Four projects with distinct volumes, so "biggest" is unambiguous.
	// Projects rather than users: the user seed helper names every row "test",
	// which would group them into one and quietly stop this exercising an
	// overflow at all.
	for i, words := range []int{40, 30, 20, 10} {
		proj := testutil.SeedProject(t, f.q, f.tenantA.ID,
			fmt.Sprintf("Fold Project %d", i), "en", []string{"de"})
		file := testutil.SeedFile(t, f.q, proj.ID, fmt.Sprintf("f%d.txt", i), "txt")
		seg := testutil.SeedSegment(t, f.q, file.ID, fmt.Sprintf("fold %d", i), 1)
		setWords(t, f.pool, seg.ID, words)
		tr := testutil.SeedTranslation(t, f.q, seg.ID, "de", "needs_review")
		text := "t"
		if _, err := f.q.InsertTranslationHistory(ctx, db.InsertTranslationHistoryParams{
			TranslationID: tr.ID, Kind: db.TranslationHistoryKindHuman, Text: &text,
			CreatedBy: &f.userA.ID, Details: []byte("{}"),
		}); err != nil {
			t.Fatalf("history: %v", err)
		}
	}

	full := f.run(t, f.tenantA.ID, metrics.Request{
		Measure: "words_processed", Dimensions: []string{"project"}, RowBudget: 500,
	})
	if len(full.Rows) < 4 {
		t.Fatalf("only %d projects have data; the test is not creating an overflow", len(full.Rows))
	}
	folded := f.run(t, f.tenantA.ID, metrics.Request{
		Measure: "words_processed", Dimensions: []string{"project"}, RowBudget: 3,
	})

	if !folded.Folded {
		t.Fatalf("result did not report folding: rows=%d truncated=%v full=%d",
			len(folded.Rows), folded.Truncated, len(full.Rows))
	}
	if folded.Truncated {
		t.Error("folding should make truncation unnecessary")
	}
	if len(folded.Rows) > 3 {
		t.Errorf("rows = %d, over the budget", len(folded.Rows))
	}

	var names []string
	var foldedTotal, fullTotal int64
	for _, row := range folded.Rows {
		names = append(names, row["project"].(string))
		foldedTotal += asInt64(t, row["words"])
	}
	for _, row := range full.Rows {
		fullTotal += asInt64(t, row["words"])
	}
	// Nothing is lost — that is the whole difference from truncation.
	if foldedTotal != fullTotal {
		t.Errorf("folded total = %d, full total = %d; the tail was dropped, not aggregated", foldedTotal, fullTotal)
	}
	if !slices.Contains(names, metrics.OtherBucket) {
		t.Errorf("rows = %v, want one %q", names, metrics.OtherBucket)
	}
}

// --- Auto-approval ---------------------------------------------------------------

// qpEvent writes one history row in the shape the pipeline writes it, and
// returns its id. outcome lands in details.predict_outcome, as RunAIStep
// records it on 'auto_approved' and 'ape' rows; "" writes empty details.
func qpEvent(t *testing.T, q db.Querier, translationID int64, kind, outcome string, userID *int64) int64 {
	t.Helper()
	details := []byte("{}")
	if outcome != "" {
		details = []byte(`{"predict_outcome":"` + outcome + `"}`)
	}
	var text *string
	if kind != "auto_approved" {
		v := "t"
		text = &v
	}
	row, err := q.InsertTranslationHistory(context.Background(), db.InsertTranslationHistoryParams{
		TranslationID: translationID, Kind: db.TranslationHistoryKind(kind), Text: text,
		CreatedBy: userID, Details: details,
	})
	if err != nil {
		t.Fatalf("insert %s history: %v", kind, err)
	}
	return row.ID
}

// The measure that says what quality prediction actually saved: words that went
// out without a human looking at them. It counts the approval EVENTS, dated when
// quality prediction approved, and splits them by where the text came from.
func TestIntegrationMetricsAutoApprovedCountsOnlyUntouchedOutcomes(t *testing.T) {
	f := newMetricsFixture(t)
	ctx := context.Background()

	// One translation per case, each 10 words, so the arithmetic is obvious.
	// steps are the history rows, oldest first: kind and details.predict_outcome.
	type step struct{ kind, outcome string }
	cases := []struct {
		outcome string
		steps   []step
	}{
		// MT accepted as-is, then a person touched it: still one auto-approval,
		// in the mt bucket, and not credited to that person.
		{"approved", []step{{"mt", ""}, {"auto_approved", "approved"}, {"human", ""}}},
		// TM fixed by APE and the fix accepted.
		{"ape_approved", []step{{"tm", ""}, {"ape", "ape_approved"}}},
		// APE rewrote it but a person still has to look.
		{"ape_needs_review", []step{{"mt", ""}, {"ape", "ape_needs_review"}}},
		// Rejected writes no history row of its own.
		{"rejected", []step{{"mt", ""}}},
	}
	for i, c := range cases {
		seg := testutil.SeedSegment(t, f.q, f.fileA.ID, fmt.Sprintf("qp %d", i), int32(400+i))
		setWords(t, f.pool, seg.ID, 10)
		tr := testutil.SeedTranslation(t, f.q, seg.ID, "de", "needs_review")
		if _, err := f.pool.Exec(ctx,
			"UPDATE translations SET predict_outcome = $1 WHERE id = $2", c.outcome, tr.ID); err != nil {
			t.Fatalf("set outcome: %v", err)
		}
		for _, st := range c.steps {
			var by *int64
			if st.kind == "human" {
				by = &f.userA.ID
			}
			qpEvent(t, f.q, tr.ID, st.kind, st.outcome, by)
		}
	}
	// Approved a month ago and edited today: the approval is outside the range.
	old := testutil.SeedSegment(t, f.q, f.fileA.ID, "qp old", 450)
	setWords(t, f.pool, old.ID, 10)
	oldTr := testutil.SeedTranslation(t, f.q, old.ID, "de", "needs_review")
	if _, err := f.pool.Exec(ctx, "UPDATE translations SET predict_outcome = 'approved' WHERE id = $1", oldTr.ID); err != nil {
		t.Fatalf("set outcome: %v", err)
	}
	backdate(t, f.pool, qpEvent(t, f.q, oldTr.ID, "mt", "", nil), 31)
	backdate(t, f.pool, qpEvent(t, f.q, oldTr.ID, "auto_approved", "approved", nil), 30)
	qpEvent(t, f.q, oldTr.ID, "human", "", &f.userA.ID)

	res := f.run(t, f.tenantA.ID, metrics.Request{Measure: "auto_approved"})
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	// Only approved + ape_approved in the range: 20 words. ape_needs_review and
	// rejected both still cost a human, so counting them would overstate the saving.
	if got := asInt64(t, res.Rows[0]["words"]); got != 20 {
		t.Errorf("auto-approved words = %d, want 20 (approved + ape_approved, approved in range)", got)
	}

	// Splitting accepted-as-is from APE-corrected is the interesting question
	// behind the headline number.
	split := wordsBy(t, f.run(t, f.tenantA.ID, metrics.Request{
		Measure: "auto_approved", Dimensions: []string{"predict_outcome"},
	}), "predict_outcome")
	if len(split) != 2 || split["approved"] != 10 || split["ape_approved"] != 10 {
		t.Errorf("by predict_outcome = %v, want approved 10 and ape_approved 10", split)
	}

	// "By segment type": where the approved text came from, one bucket per
	// translation.
	byType := wordsBy(t, f.run(t, f.tenantA.ID, metrics.Request{
		Measure: "auto_approved", Dimensions: []string{"segment_type"},
	}), "segment_type")
	if len(byType) != 2 || byType["mt"] != 10 || byType["tm"] != 10 {
		t.Errorf("by segment_type = %v, want mt 10 and tm 10", byType)
	}

	// The later human edit is not the approval, so it is not credited.
	byUser := wordsBy(t, f.run(t, f.tenantA.ID, metrics.Request{
		Measure: "auto_approved", Dimensions: []string{"user"},
	}), "user")
	if len(byUser) != 1 || byUser[""] != 20 {
		t.Errorf("by user = %v, want only the unattributed approvals (20)", byUser)
	}

	// Tenant B has no auto-approved work and must not borrow tenant A's.
	resB := f.run(t, f.tenantB.ID, metrics.Request{Measure: "auto_approved"})
	for _, row := range resB.Rows {
		if asInt64(t, row["words"]) != 0 {
			t.Errorf("tenant B sees %v", row)
		}
	}
}

// wordsBy indexes a one-dimension result's words by that dimension's value.
func wordsBy(t *testing.T, res *metrics.Result, dim string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, row := range res.Rows {
		key, _ := row[dim].(string)
		out[key] += asInt64(t, row["words"])
	}
	return out
}

// A chart's range ends at the next midnight, exclusive: the filled series
// stops at today and never adds an empty bucket for tomorrow.
func TestIntegrationMetricsGapFillStopsAtTheExclusiveEnd(t *testing.T) {
	f := newMetricsFixture(t)
	now := time.Now().UTC()
	from, to, err := metrics.RangeFromLastDays(7, now)
	if err != nil {
		t.Fatal(err)
	}
	res := f.run(t, f.tenantA.ID, metrics.Request{Measure: "words_processed", Dimensions: []string{"day"}, From: from, To: to, RowBudget: 500})
	if len(res.Rows) == 0 {
		t.Fatal("no rows")
	}
	last, _ := res.Rows[len(res.Rows)-1]["day"].(string)
	if today := now.Format("2006-01-02"); last != today {
		t.Errorf("last day bucket = %q, want today %q (rows %d)", last, today, len(res.Rows))
	}
}

// source_volume counts each translation once, whatever its history: t1 has
// three history events and still counts as one segment of 10 words.
func TestIntegrationMetricsSourceVolumeCountsEachTranslationOnce(t *testing.T) {
	f := newMetricsFixture(t)
	ctx := context.Background()
	// t1 (10 words) and t3 (5) came from MT, t2 (7) from TM; t4 (3) has no
	// automatic origin (a person translated it, or nobody has yet).
	for words, origin := range map[int]string{10: "mt", 5: "mt", 7: "tm"} {
		if _, err := f.pool.Exec(ctx, `UPDATE translations t SET origin = $1::translation_origin
			FROM segments s WHERE s.id = t.segment_id AND s.file_id = $2 AND s.word_count = $3`, origin, f.fileA.ID, words); err != nil {
			t.Fatalf("set origin: %v", err)
		}
	}

	total := f.run(t, f.tenantA.ID, metrics.Request{Measure: "source_volume"})
	if len(total.Rows) != 1 {
		t.Fatalf("rows = %+v", total.Rows)
	}
	// Tenant B's 99-word segment would make this 124.
	if w, s := asInt64(t, total.Rows[0]["words"]), asInt64(t, total.Rows[0]["segments"]); w != 25 || s != 4 {
		t.Errorf("total = %d words / %d segments, want 25 / 4", w, s)
	}

	byOrigin := map[string]int64{}
	for _, r := range f.run(t, f.tenantA.ID, metrics.Request{Measure: "source_volume", Dimensions: []string{"origin"}}).Rows {
		name, _ := r["origin"].(string)
		byOrigin[name] = asInt64(t, r["words"])
	}
	if byOrigin["mt"] != 15 || byOrigin["tm"] != 7 || byOrigin[""] != 3 || len(byOrigin) != 3 {
		t.Errorf("by origin = %v, want mt:15 tm:7 (none):3", byOrigin)
	}

	byStatus := map[string]int64{}
	for _, r := range f.run(t, f.tenantA.ID, metrics.Request{Measure: "source_volume", Dimensions: []string{"status"}}).Rows {
		byStatus[r["status"].(string)] = asInt64(t, r["words"])
	}
	if byStatus["needs_review"] != 17 || byStatus["approved"] != 5 || byStatus["auto_approved"] != 3 || len(byStatus) != 3 {
		t.Errorf("by status = %v, want needs_review:17 approved:5 auto_approved:3", byStatus)
	}

	// Tenant B sees only its own segment.
	b := f.run(t, f.tenantB.ID, metrics.Request{Measure: "source_volume"})
	if len(b.Rows) != 1 || asInt64(t, b.Rows[0]["words"]) != 99 {
		t.Errorf("tenant B = %+v, want 99 words", b.Rows)
	}
}

// Every history kind is either an origin or a status event. A new kind fails
// here until it is placed, so it can't silently drop out of segments_by_origin.
func TestIntegrationMetricsHistoryKindsAreAllPlaced(t *testing.T) {
	f := newMetricsFixture(t)
	rows, err := f.pool.Query(context.Background(), "SELECT unnest(enum_range(NULL::translation_history_kind))::text")
	if err != nil {
		t.Fatalf("enum_range: %v", err)
	}
	defer rows.Close()
	var dbKinds []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scan: %v", err)
		}
		dbKinds = append(dbKinds, k)
	}
	placed := metrics.HistoryKindsForTest()
	slices.Sort(dbKinds)
	slices.Sort(placed)
	if !slices.Equal(dbKinds, placed) {
		t.Errorf("history kinds in the database %v, placed in originKinds/statusKinds %v", dbKinds, placed)
	}
}

// labelProject gives a project a value of one of its tenant's built-in labels.
func labelProject(t *testing.T, pool *pgxpool.Pool, project db.Project, systemKey, value string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		WITH d AS (SELECT id FROM label_definitions WHERE tenant_id = $1 AND system_key = $2 AND deleted_at IS NULL),
		     v AS (INSERT INTO label_values (label_definition_id, value) SELECT id, $3 FROM d RETURNING id, label_definition_id)
		INSERT INTO project_labels (project_id, label_definition_id, label_value_id) SELECT $4, label_definition_id, id FROM v`,
		project.TenantID, systemKey, value, project.ID); err != nil {
		t.Fatalf("label project: %v", err)
	}
}

// A project carries a Domain label next to its Customer label: facts are
// still counted once, and only the Customer value is a customer.
func TestIntegrationMetricsCustomerIgnoresOtherLabels(t *testing.T) {
	f := newMetricsFixture(t)
	labelProject(t, f.pool, f.projectA, "customer", "Acme")
	labelProject(t, f.pool, f.projectA, "domain", "Legal")

	total := f.run(t, f.tenantA.ID, metrics.Request{Measure: "source_volume"})
	if len(total.Rows) != 1 || asInt64(t, total.Rows[0]["words"]) != 25 {
		t.Fatalf("total = %+v, want 25 words", total.Rows)
	}
	byCustomer := map[string]int64{}
	for _, r := range f.run(t, f.tenantA.ID, metrics.Request{Measure: "source_volume", Dimensions: []string{"customer"}}).Rows {
		name, _ := r["customer"].(string)
		byCustomer[name] = asInt64(t, r["words"])
	}
	if byCustomer["Acme"] != 25 || len(byCustomer) != 1 {
		t.Errorf("by customer = %v, want Acme:25 only", byCustomer)
	}
}
