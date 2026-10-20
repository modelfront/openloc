package demodata_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/demodata"
	"openloc/internal/testutil"
)

// Demo history is backdated; a translation must not look newer than its first
// event, or source volume (dated by translation) lands in a different week
// from the MT and human activity on the same segments.
func TestIntegrationAlignTranslationDates(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	seed := func(name string) (db.Tenant, db.Translation, db.Translation) {
		tenant := testutil.SeedTenant(t, q, name)
		p := testutil.SeedProject(t, q, tenant.ID, name+" project", "en", []string{"de"})
		f := testutil.SeedFile(t, q, p.ID, "a.txt", "txt")
		withHistory := testutil.SeedTranslation(t, q, testutil.SeedSegment(t, q, f.ID, "one", 1).ID, "de", "needs_review")
		without := testutil.SeedTranslation(t, q, testutil.SeedSegment(t, q, f.ID, "two", 2).ID, "de", "needs_review")
		for _, daysAgo := range []int{30, 10} {
			var id int64
			if err := pool.QueryRow(ctx, `INSERT INTO translation_history (translation_id, kind, details, created_at)
				VALUES ($1, 'mt', '{}', now() - make_interval(days => $2)) RETURNING id`, withHistory.ID, daysAgo).Scan(&id); err != nil {
				t.Fatalf("insert history: %v", err)
			}
		}
		return tenant, withHistory, without
	}
	tenantA, aHist, aNone := seed("AlignA")
	_, bHist, _ := seed("AlignB")

	createdAt := func(id int64) time.Time {
		var at time.Time
		if err := pool.QueryRow(ctx, `SELECT created_at FROM translations WHERE id = $1`, id).Scan(&at); err != nil {
			t.Fatalf("read created_at: %v", err)
		}
		return at
	}
	before := createdAt(aNone.ID)

	// Already older than its first event: aligning must never move a row later.
	older := testutil.SeedTranslation(t, q, testutil.SeedSegment(t, q, testutil.SeedFile(t, q,
		testutil.SeedProject(t, q, tenantA.ID, "Older project", "en", []string{"de"}).ID, "b.txt", "txt").ID, "three", 1).ID, "de", "needs_review")
	if _, err := pool.Exec(ctx, `UPDATE translations SET created_at = now() - interval '60 days' WHERE id = $1`, older.ID); err != nil {
		t.Fatalf("age translation: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO translation_history (translation_id, kind, details, created_at)
		VALUES ($1, 'mt', '{}', now() - interval '20 days')`, older.ID); err != nil {
		t.Fatalf("insert history: %v", err)
	}
	olderAt := createdAt(older.ID)

	if err := demodata.AlignTranslationDates(ctx, pool, tenantA.ID); err != nil {
		t.Fatalf("align: %v", err)
	}

	if age := time.Since(createdAt(aHist.ID)); age < 29*24*time.Hour || age > 31*24*time.Hour {
		t.Errorf("translation with history is %v old, want its first event's 30 days", age)
	}
	if got := createdAt(older.ID); !got.Equal(olderAt) {
		t.Errorf("translation older than its first event moved: %v → %v", olderAt, got)
	}
	if !createdAt(aNone.ID).Equal(before) {
		t.Errorf("translation without history moved: %v → %v", before, createdAt(aNone.ID))
	}
	// Only the named tenant is touched.
	if age := time.Since(createdAt(bHist.ID)); age > time.Hour {
		t.Errorf("tenant B's translation moved (%v old); only tenant A was aligned", age)
	}
}

// A seeded translation with an origin but no history gets the event real
// pre-translation writes, so MT usage and source volume agree.
func TestIntegrationHealOriginHistory(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, "HealOrigin")
	engine := testutil.SeedMTEngine(t, q, tenant.ID, "Engine One", "deepl")
	p := testutil.SeedProject(t, q, tenant.ID, "Heal project", "en", []string{"de"})
	f := testutil.SeedFile(t, q, p.ID, "a.txt", "txt")
	mt := testutil.SeedTranslation(t, q, testutil.SeedSegment(t, q, f.ID, "one", 1).ID, "de", "needs_review")
	tm := testutil.SeedTranslation(t, q, testutil.SeedSegment(t, q, f.ID, "two", 2).ID, "de", "needs_review")
	none := testutil.SeedTranslation(t, q, testutil.SeedSegment(t, q, f.ID, "three", 3).ID, "de", "needs_review")
	for id, origin := range map[int64]string{mt.ID: "mt", tm.ID: "tm"} {
		if _, err := pool.Exec(ctx, `UPDATE translations SET origin = $1::translation_origin, active_text = 'x' WHERE id = $2`, origin, id); err != nil {
			t.Fatalf("set origin: %v", err)
		}
	}

	for range 2 { // idempotent: the second run adds nothing
		if err := demodata.HealOriginHistory(ctx, pool, tenant.ID); err != nil {
			t.Fatalf("heal: %v", err)
		}
	}

	rows := func(id int64) []string {
		r, err := pool.Query(ctx, `SELECT kind::text || ':' || COALESCE(mt_engine_id::text, '-') FROM translation_history WHERE translation_id = $1`, id)
		if err != nil {
			t.Fatalf("read history: %v", err)
		}
		defer r.Close()
		var out []string
		for r.Next() {
			var s string
			if err := r.Scan(&s); err != nil {
				t.Fatalf("scan: %v", err)
			}
			out = append(out, s)
		}
		return out
	}
	if got := rows(mt.ID); len(got) != 1 || got[0] != fmt.Sprintf("mt:%d", engine.ID) {
		t.Errorf("mt translation history = %v, want one mt event on engine %d", got, engine.ID)
	}
	if got := rows(tm.ID); len(got) != 1 || got[0] != "tm:-" {
		t.Errorf("tm translation history = %v, want one tm event", got)
	}
	if got := rows(none.ID); len(got) != 0 {
		t.Errorf("translation without origin got history %v", got)
	}
}
