package analysis_test

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// Saved-analysis worker. Drives the
// worker against a seeded DB and verifies:
//
//   - Each band's boundary lands segments in the right column.
//   - auto_approved metrics are a SUBSET of the band totals (a single
//     auto-approved segment increments BOTH columns, never just the subset).
//   - LEFT JOIN handling: a segment with NO translation row counts as `new`
//     and does NOT contribute to any `_auto_approved` column.
//   - Status transitions: pending → running → completed; on tenant mismatch,
//     pending → failed with error_message populated.
//
// tm_101 (101% in-context exact) is populated by the pipeline and
// is asserted as its own band here. Every source here is unique, so repetitions
// are legitimately zero — TestAnalysisWorker_RepetitionsSubtractBand
// covers the repeated-source path.

func TestAnalysisWorker_BandBoundariesAndAutoApprovedSubset(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "ana-bands")
	project := testutil.SeedProject(t, q, tenant.ID, "ana-bands-project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "ana-bands.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	// Seed 9 segments + their translations covering every band boundary.
	// Each segment is 5 words (so band word counts are predictable: 5 each).
	// Translations:
	//   seg 1: TM 100 + auto_approved (one of the "100" band, in the subset)
	//   seg 2: TM 100 + needs_review  (in band total, NOT in subset)
	//   seg 3: TM 99  + auto_approved (top of high fuzzy band)
	//   seg 4: TM 85  + needs_review  (bottom of high fuzzy band)
	//   seg 5: TM 84  + needs_review  (top of low fuzzy band)
	//   seg 6: TM 65  + needs_review  (bottom of low fuzzy band)
	//   seg 7: TM 64  + needs_review  (just under threshold → 'new' band)
	//   seg 8: NO translation row     (LEFT JOIN → 'new' band, NOT in subset)
	//   seg 9: TM 101 + approved      (in-context exact → 'tm_101' band)
	type segSpec struct {
		text         string
		matchPct     *int16 // nil → no translation row
		status       db.TranslationStatus
		origin       db.NullTranslationOrigin
		autoApproved bool
	}
	specs := []segSpec{
		{"alpha bravo charlie delta echo", ptr.To[int16](100), db.TranslationStatusAutoApproved, tmOrigin(), true},
		{"foxtrot golf hotel india juliet", ptr.To[int16](100), db.TranslationStatusNeedsReview, tmOrigin(), false},
		{"kilo lima mike november oscar", ptr.To[int16](99), db.TranslationStatusAutoApproved, tmOrigin(), true},
		{"papa quebec romeo sierra tango", ptr.To[int16](85), db.TranslationStatusNeedsReview, tmOrigin(), false},
		{"uniform victor whiskey xray yankee", ptr.To[int16](84), db.TranslationStatusNeedsReview, tmOrigin(), false},
		{"zulu one two three four", ptr.To[int16](65), db.TranslationStatusNeedsReview, tmOrigin(), false},
		{"five six seven eight nine", ptr.To[int16](64), db.TranslationStatusNeedsReview, tmOrigin(), false},
		{"ten eleven twelve thirteen fourteen", nil, "", db.NullTranslationOrigin{}, false},
		{"context match alpha beta gamma", ptr.To[int16](101), db.TranslationStatusApproved, tmOrigin(), false},
	}

	for i, sp := range specs {
		seg := testutil.SeedSegment(t, q, file.ID, sp.text, int32(i))
		// SeedSegment defaults word_count=0; the file parser would normally
		// populate it. For this test every segment is exactly 5 words.
		if _, err := pool.Exec(ctx, `UPDATE segments SET word_count = 5 WHERE id = $1`, seg.ID); err != nil {
			t.Fatalf("set word_count: %v", err)
		}
		if sp.matchPct != nil {
			_, err := pool.Exec(ctx, `
				INSERT INTO translations (segment_id, target_language, status, origin, tm_match_percent)
				VALUES ($1, 'de', $2, $3, $4)`,
				seg.ID, string(sp.status), "tm", *sp.matchPct)
			if err != nil {
				t.Fatalf("seed translation %d: %v", i, err)
			}
		}
	}

	// Seed the analysis row in pending state.
	var analysisID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO analyses (tenant_id, project_id, name, source_language, job_ids)
		VALUES ($1, $2, 'band-boundaries', 'en', ARRAY[$3::bigint])
		RETURNING id`, tenant.ID, project.ID, job.ID).Scan(&analysisID); err != nil {
		t.Fatalf("seed analysis: %v", err)
	}

	// Run the worker.
	svc := newService(pool, q)
	if err := svc.Run(ctx, analysisID, tenant.ID); err != nil {
		t.Fatalf("worker Run: %v", err)
	}

	// Status transitioned to completed.
	got, err := q.GetAnalysis(ctx, db.GetAnalysisParams{ID: analysisID, TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("reload analysis: %v", err)
	}
	if got.Status != db.AnalysisStatusCompleted {
		t.Errorf("status: want completed, got %s (err=%v)", got.Status, testutil.Show(got.ErrorMessage))
	}
	if !got.CompletedAt.Valid {
		t.Error("completed_at should be set on success")
	}

	// One job_analysis row was written for (analysis, job, de).
	breakdowns, err := q.ListJobAnalysisByAnalysis(ctx, analysisID)
	if err != nil {
		t.Fatalf("list breakdowns: %v", err)
	}
	if len(breakdowns) != 1 {
		t.Fatalf("breakdown rows: want 1, got %d", len(breakdowns))
	}
	row := breakdowns[0]

	// ── Band totals (each band gets the right segment count + 5 words/segment) ──
	// tm_100: 2 segments (specs 0 + 1)
	if row.Tm100Segments != 2 || row.Tm100Words != 10 {
		t.Errorf("tm_100: want 2 segs/10 words, got %d/%d", row.Tm100Segments, row.Tm100Words)
	}
	// tm_100 auto_approved subset: 1 segment (spec 0 only)
	if row.Tm100SegmentsAutoApproved != 1 || row.Tm100WordsAutoApproved != 5 {
		t.Errorf("tm_100 auto_approved: want 1 seg/5 words, got %d/%d", row.Tm100SegmentsAutoApproved, row.Tm100WordsAutoApproved)
	}

	// tm_85_99: 2 segments (specs 2 + 3 at 99 and 85)
	if row.Tm8599Segments != 2 {
		t.Errorf("tm_85_99: want 2 segs, got %d", row.Tm8599Segments)
	}
	// auto_approved subset: 1 (spec 2)
	if row.Tm8599SegmentsAutoApproved != 1 {
		t.Errorf("tm_85_99 auto_approved: want 1, got %d", row.Tm8599SegmentsAutoApproved)
	}

	// tm_65_84: 2 segments (specs 4 + 5 at 84 and 65)
	if row.Tm6584Segments != 2 {
		t.Errorf("tm_65_84: want 2 segs, got %d", row.Tm6584Segments)
	}
	if row.Tm6584SegmentsAutoApproved != 0 {
		t.Errorf("tm_65_84 auto_approved: want 0, got %d", row.Tm6584SegmentsAutoApproved)
	}

	// new: 2 segments (spec 6 at 64% which fell below threshold, spec 7 with NO translation row)
	if row.NewSegments != 2 {
		t.Errorf("new: want 2 segs, got %d", row.NewSegments)
	}
	if row.NewSegmentsAutoApproved != 0 {
		t.Errorf("new auto_approved: want 0, got %d", row.NewSegmentsAutoApproved)
	}

	// tm_101 (in-context exact): 1 segment / 5 words (spec 8). Its
	// auto_approved subset is 0 — 101% confirms to status='approved' (like a
	// plain 100% exact), which is NOT the 'auto_approved' the subset counts.
	if row.Tm101Segments != 1 || row.Tm101Words != 5 {
		t.Errorf("tm_101: want 1 seg/5 words, got %d/%d", row.Tm101Segments, row.Tm101Words)
	}
	if row.Tm101SegmentsAutoApproved != 0 {
		t.Errorf("tm_101 auto_approved: want 0, got %d", row.Tm101SegmentsAutoApproved)
	}
	// Every source is unique here → no repetitions.
	if row.RepetitionsSegments != 0 || row.RepetitionsSegmentsAutoApproved != 0 {
		t.Errorf("repetitions must be zero: segs=%d aa=%d", row.RepetitionsSegments, row.RepetitionsSegmentsAutoApproved)
	}

	// Mutual exclusion invariant: bands sum to total segments (9).
	totalSegs := row.Tm101Segments + row.Tm100Segments + row.Tm8599Segments + row.Tm6584Segments + row.NewSegments + row.RepetitionsSegments
	if totalSegs != 9 {
		t.Errorf("band sum: want 9 segments (mutual exclusion), got %d", totalSegs)
	}

	// Snapshot fields populated.
	if row.JobNameSnapshot != "ana-bands.txt" {
		t.Errorf("job_name_snapshot: want %q, got %q", "ana-bands.txt", row.JobNameSnapshot)
	}
	if row.JobID == nil || *row.JobID != job.ID {
		t.Errorf("job_id: want %d, got %v", job.ID, row.JobID)
	}
}

// TestAnalysisWorker_RepetitionsSubtractBand pins the repetitions rule: an
// identical source text repeated across two files in the same analysis is
// counted ONCE in its real leverage band (first occurrence) and the 2nd
// occurrence is moved to the repetitions bucket — subtracted from the band, not
// double-counted. Per-job attribution: the repetition lands on the file where
// the later occurrence lives. RED-proof: with the repetitions column hardcoded
// to zero and the repeat counted in tm_100, the job-B assertions fail.
func TestAnalysisWorker_RepetitionsSubtractBand(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "ana-reps")
	project := testutil.SeedProject(t, q, tenant.ID, "ana-reps-project", "en", []string{"de"})
	// fileA seeded first → lower id → its occurrence is the "first" (rn=1).
	fileA := testutil.SeedFile(t, q, project.ID, "reps-a.txt", db.FileFormatTxt)
	fileB := testutil.SeedFile(t, q, project.ID, "reps-b.txt", db.FileFormatTxt)
	jobA := testutil.SeedJob(t, q, fileA.ID, "de")
	jobB := testutil.SeedJob(t, q, fileB.ID, "de")

	// Same source in both files, both TM-100 auto_approved. Plus one genuinely
	// unique segment in file B so we can prove it stays in `new` (not a rep).
	const shared = "repeat one two three four"
	segA := testutil.SeedSegment(t, q, fileA.ID, shared, 0)
	segB := testutil.SeedSegment(t, q, fileB.ID, shared, 0)
	segUnique := testutil.SeedSegment(t, q, fileB.ID, "unique alpha beta gamma delta", 1)
	for _, id := range []int64{segA.ID, segB.ID, segUnique.ID} {
		if _, err := pool.Exec(ctx, `UPDATE segments SET word_count = 5 WHERE id = $1`, id); err != nil {
			t.Fatalf("set word_count: %v", err)
		}
	}
	// Both shared occurrences are TM-100 auto_approved; the unique one has no
	// translation row (LEFT JOIN → new).
	for _, segID := range []int64{segA.ID, segB.ID} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO translations (segment_id, target_language, status, origin, tm_match_percent)
			VALUES ($1, 'de', 'auto_approved', 'tm', 100)`, segID); err != nil {
			t.Fatalf("seed translation: %v", err)
		}
	}

	a, err := q.InsertAnalysis(ctx, db.InsertAnalysisParams{
		TenantID:         tenant.ID,
		ProjectID:        project.ID,
		Name:             "reps",
		SourceLanguage:   "en",
		JobIds:           []int64{jobA.ID, jobB.ID},
		SettingsSnapshot: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("insert analysis: %v", err)
	}

	svc := newService(pool, q)
	if err := svc.Run(ctx, a.ID, tenant.ID); err != nil {
		t.Fatalf("worker Run: %v", err)
	}

	breakdowns, err := q.ListJobAnalysisByAnalysis(ctx, a.ID)
	if err != nil {
		t.Fatalf("list breakdowns: %v", err)
	}
	byJob := map[int64]db.JobAnalysis{}
	for _, b := range breakdowns {
		if b.JobID != nil {
			byJob[*b.JobID] = b
		}
	}
	rowA, rowB := byJob[jobA.ID], byJob[jobB.ID]

	// Job A: the first occurrence keeps its real band (tm_100), no repetition.
	if rowA.Tm100Segments != 1 || rowA.Tm100SegmentsAutoApproved != 1 {
		t.Errorf("job A tm_100: want 1/1, got %d/%d", rowA.Tm100Segments, rowA.Tm100SegmentsAutoApproved)
	}
	if rowA.RepetitionsSegments != 0 {
		t.Errorf("job A repetitions: want 0, got %d", rowA.RepetitionsSegments)
	}

	// Job B: the 2nd occurrence is SUBTRACTED from tm_100 and counted as a
	// repetition (auto_approved subset carried through). The unique segment
	// stays in `new`.
	if rowB.Tm100Segments != 0 {
		t.Errorf("job B tm_100 must be subtracted to 0, got %d", rowB.Tm100Segments)
	}
	if rowB.RepetitionsSegments != 1 || rowB.RepetitionsWords != 5 {
		t.Errorf("job B repetitions: want 1 seg/5 words, got %d/%d", rowB.RepetitionsSegments, rowB.RepetitionsWords)
	}
	if rowB.RepetitionsSegmentsAutoApproved != 1 {
		t.Errorf("job B repetitions auto_approved: want 1, got %d", rowB.RepetitionsSegmentsAutoApproved)
	}
	if rowB.NewSegments != 1 {
		t.Errorf("job B new: want 1 (the unique segment), got %d", rowB.NewSegments)
	}

	// Mutual-exclusion invariant holds per job: bands + repetitions == segments.
	sumA := rowA.Tm101Segments + rowA.Tm100Segments + rowA.Tm8599Segments + rowA.Tm6584Segments + rowA.NewSegments + rowA.RepetitionsSegments
	sumB := rowB.Tm101Segments + rowB.Tm100Segments + rowB.Tm8599Segments + rowB.Tm6584Segments + rowB.NewSegments + rowB.RepetitionsSegments
	if sumA != 1 {
		t.Errorf("job A band sum: want 1, got %d", sumA)
	}
	if sumB != 2 {
		t.Errorf("job B band sum: want 2, got %d", sumB)
	}
}

func TestAnalysisWorker_FailsOnCrossTenantJob(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Two tenants. Tenant A owns the analysis; we plant tenant B's job in
	// the array to verify the worker rejects it.
	tenantA := testutil.SeedTenant(t, q, "ana-tenantA")
	tenantB := testutil.SeedTenant(t, q, "ana-tenantB")
	projectA := testutil.SeedProject(t, q, tenantA.ID, "ana-pA", "en", []string{"de"})
	projectB := testutil.SeedProject(t, q, tenantB.ID, "ana-pB", "en", []string{"de"})
	fileA := testutil.SeedFile(t, q, projectA.ID, "a.txt", db.FileFormatTxt)
	fileB := testutil.SeedFile(t, q, projectB.ID, "b.txt", db.FileFormatTxt)
	jobA := testutil.SeedJob(t, q, fileA.ID, "de")
	jobB := testutil.SeedJob(t, q, fileB.ID, "de")

	// One legit job + one cross-tenant job.
	a, err := q.InsertAnalysis(ctx, db.InsertAnalysisParams{
		TenantID:         tenantA.ID,
		ProjectID:        projectA.ID,
		Name:             "cross-tenant-test",
		SourceLanguage:   "en",
		JobIds:           []int64{jobA.ID, jobB.ID},
		SettingsSnapshot: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("insert analysis: %v", err)
	}

	svc := newService(pool, q)
	if err := svc.Run(ctx, a.ID, tenantA.ID); err == nil {
		t.Fatal("expected error from cross-tenant job, got nil")
	}

	reloaded, err := q.GetAnalysis(ctx, db.GetAnalysisParams{ID: a.ID, TenantID: tenantA.ID})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status != db.AnalysisStatusFailed {
		t.Errorf("status: want failed, got %s", reloaded.Status)
	}
	if reloaded.ErrorMessage == nil || *reloaded.ErrorMessage == "" {
		t.Error("error_message should be populated on tenant-mismatch failure")
	}
}

func TestAnalysisWorker_IdempotentOnRetry(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "ana-idempotent")
	project := testutil.SeedProject(t, q, tenant.ID, "ana-idem-project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "idem.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")
	testutil.SeedSegment(t, q, file.ID, "one two three", 0)

	var analysisID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO analyses (tenant_id, project_id, name, source_language, job_ids)
		VALUES ($1, $2, 'idempotent', 'en', ARRAY[$3::bigint])
		RETURNING id`, tenant.ID, project.ID, job.ID).Scan(&analysisID); err != nil {
		t.Fatalf("seed: %v", err)
	}

	svc := newService(pool, q)
	if err := svc.Run(ctx, analysisID, tenant.ID); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	// Second invocation (asynq retry of a completed task) must no-op cleanly.
	if err := svc.Run(ctx, analysisID, tenant.ID); err != nil {
		t.Fatalf("second Run (idempotent retry): %v", err)
	}

	// Still exactly one breakdown row, not duplicated.
	breakdowns, err := q.ListJobAnalysisByAnalysis(ctx, analysisID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(breakdowns) != 1 {
		t.Errorf("breakdown rows after retry: want 1 (idempotent), got %d", len(breakdowns))
	}
}

// ── helpers ──

func tmOrigin() db.NullTranslationOrigin {
	return db.NullTranslationOrigin{TranslationOrigin: db.TranslationOriginTm, Valid: true}
}
