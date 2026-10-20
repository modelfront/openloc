package analysis_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// seedExportAnalysis inserts a completed analysis with one breakdown row for
// a German job: 12 new words and 30 100%-match words.
func seedExportAnalysis(t *testing.T, pool *pgxpool.Pool, q db.Querier, tenantID, projectID int64, name string) db.Analysis {
	t.Helper()
	ctx := context.Background()
	a, err := q.InsertAnalysis(ctx, db.InsertAnalysisParams{
		TenantID:         tenantID,
		ProjectID:        projectID,
		Name:             name,
		SourceLanguage:   "en",
		JobIds:           []int64{},
		SettingsSnapshot: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("seed analysis: %v", err)
	}
	if err := q.InsertJobAnalysis(ctx, db.InsertJobAnalysisParams{
		AnalysisID:      a.ID,
		TargetLanguage:  "de",
		JobNameSnapshot: "manual.docx",
		NewSegments:     2,
		NewWords:        12,
		NewCharacters:   70,
		Tm100Segments:   3,
		Tm100Words:      30,
		Tm100Characters: 150,
	}); err != nil {
		t.Fatalf("seed breakdown: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE analyses SET status='completed', completed_at=NOW() WHERE id=$1`, a.ID); err != nil {
		t.Fatalf("complete analysis: %v", err)
	}
	return a
}

func exportPath(id int64, format string) string {
	return fmt.Sprintf("/api/analyses/%d/export?format=%s", id, format)
}

func TestHandleExport_CSV(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	tenant := testutil.SeedTenant(t, q, "ana-export-csv")
	user := testutil.SeedUser(t, q, tenant.ID, "export@test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "export-proj", "en", []string{"de"})
	a := seedExportAnalysis(t, pool, q, tenant.ID, project.ID, "Quote batch")

	rec := testutil.DoRequest(t, testutil.NewTestRouter(pool), http.MethodGet, exportPath(a.ID, "csv"), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type: got %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "Quote batch ") || !strings.Contains(cd, ".csv") {
		t.Errorf("Content-Disposition: got %q", cd)
	}
	records, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(rec.Body.Bytes(), []byte("\xEF\xBB\xBF")))).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("lines: want header + 1, got %d", len(records))
	}
	cols := map[string]int{}
	for i, h := range records[0] {
		cols[h] = i
	}
	row := records[1]
	if row[cols["File"]] != "manual.docx" || row[cols["Target language"]] != "de" {
		t.Errorf("row identity: %v", row[:2])
	}
	if row[cols["New words"]] != "12" || row[cols["100% words"]] != "30" {
		t.Errorf("words: new=%s 100%%=%s", row[cols["New words"]], row[cols["100% words"]])
	}
	if _, ok := cols["Total words"]; ok {
		t.Errorf("unexpected Total words column")
	}
}

func TestHandleExport_XLSX(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	tenant := testutil.SeedTenant(t, q, "ana-export-xlsx")
	user := testutil.SeedUser(t, q, tenant.ID, "export@test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "export-proj", "en", []string{"de"})
	a := seedExportAnalysis(t, pool, q, tenant.ID, project.ID, "Quote batch")

	rec := testutil.DoRequest(t, testutil.NewTestRouter(pool), http.MethodGet, exportPath(a.ID, "xlsx"), nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); ct != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Errorf("Content-Type: got %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, ".xlsx") {
		t.Errorf("Content-Disposition: got %q", cd)
	}
	f, err := excelize.OpenReader(rec.Body)
	if err != nil {
		t.Fatalf("open xlsx: %v", err)
	}
	defer f.Close()
	rows, err := f.GetRows("Analysis")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1][0] != "manual.docx" {
		t.Errorf("rows: want header and manual.docx; got %v", rows)
	}
}

func TestHandleExport_Rejections(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	tenantA := testutil.SeedTenant(t, q, "ana-export-a")
	tenantB := testutil.SeedTenant(t, q, "ana-export-b")
	userA := testutil.SeedUser(t, q, tenantA.ID, "a@test", "pass")
	linguist := testutil.SeedUserWithRole(t, q, tenantA.ID, "ling@test", "pass", "linguist")
	projectA := testutil.SeedProject(t, q, tenantA.ID, "pa", "en", []string{"de"})
	projectB := testutil.SeedProject(t, q, tenantB.ID, "pb", "en", []string{"de"})

	done := seedExportAnalysis(t, pool, q, tenantA.ID, projectA.ID, "done")
	other := seedExportAnalysis(t, pool, q, tenantB.ID, projectB.ID, "other tenant")
	deleted := seedExportAnalysis(t, pool, q, tenantA.ID, projectA.ID, "deleted")
	if _, err := q.DeleteAnalysis(ctx, db.DeleteAnalysisParams{ID: deleted.ID, TenantID: tenantA.ID}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	pending, err := q.InsertAnalysis(ctx, db.InsertAnalysisParams{
		TenantID: tenantA.ID, ProjectID: projectA.ID, Name: "pending",
		SourceLanguage: "en", JobIds: []int64{}, SettingsSnapshot: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("seed pending: %v", err)
	}

	router := testutil.NewTestRouter(pool)
	cases := []struct {
		name string
		path string
		want int
	}{
		{"unknown format", exportPath(done.ID, "pdf"), http.StatusBadRequest},
		{"missing format", fmt.Sprintf("/api/analyses/%d/export", done.ID), http.StatusBadRequest},
		{"not finished", exportPath(pending.ID, "csv"), http.StatusConflict},
		{"other tenant", exportPath(other.ID, "csv"), http.StatusNotFound},
		{"deleted", exportPath(deleted.ID, "xlsx"), http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := testutil.DoRequest(t, router, http.MethodGet, c.path, nil, userA.ID, tenantA.ID)
			testutil.AssertStatus(t, rec, c.want)
		})
	}

	t.Run("linguist", func(t *testing.T) {
		rec := testutil.DoRequestWithRole(t, router, http.MethodGet, exportPath(done.ID, "csv"), nil, linguist.ID, tenantA.ID, "linguist")
		testutil.AssertStatus(t, rec, http.StatusForbidden)
	})
}
