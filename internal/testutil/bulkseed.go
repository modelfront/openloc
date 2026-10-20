package testutil

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SeedSegmentsBulk adds n segments to the file at positions from+1 … from+n,
// each with a translation into lang in status needs_review carrying text as
// its active text, in two statements. Scale tests use it to grow a file
// without n round trips. It returns the new translation ids.
func SeedSegmentsBulk(t testing.TB, pool *pgxpool.Pool, fileID int64, lang string, from, n int, text string) []int64 {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO segments (file_id, position, source_text)
		SELECT $1, $2 + g, 'Bulk source ' || g FROM generate_series(1, $3) g`,
		fileID, from, n); err != nil {
		t.Fatalf("SeedSegmentsBulk segments: %v", err)
	}
	rows, err := pool.Query(ctx, `
		INSERT INTO translations (segment_id, target_language, status, active_text)
		SELECT s.id, $2, 'needs_review', $5 FROM segments s
		WHERE s.file_id = $1 AND s.position > $3 AND s.position <= $3 + $4
		ORDER BY s.position
		RETURNING id`, fileID, lang, from, n, text)
	if err != nil {
		t.Fatalf("SeedSegmentsBulk translations: %v", err)
	}
	defer rows.Close()
	ids := make([]int64, 0, n)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("SeedSegmentsBulk scan: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("SeedSegmentsBulk rows: %v", err)
	}
	return ids
}

// SeedTMEntriesBulk adds n entries to the TM in one statement; text gives the
// source and target of entry i, for i from 1 to n.
func SeedTMEntriesBulk(t testing.TB, pool *pgxpool.Pool, tmID int64, sourceLang, targetLang string, n int, text func(i int) (source, target string)) {
	t.Helper()
	sources, targets := make([]string, n), make([]string, n)
	for i := range n {
		sources[i], targets[i] = text(i + 1)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO translation_memory (tm_id, source_language, target_language, source_text, target_text, created_by, tu_id)
		SELECT $1, $2, $3, s, t, 'test', '' FROM unnest($4::text[], $5::text[]) AS e(s, t)`,
		tmID, sourceLang, targetLang, sources, targets); err != nil {
		t.Fatalf("SeedTMEntriesBulk: %v", err)
	}
}
