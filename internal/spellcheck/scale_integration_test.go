package spellcheck_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

// A document check walks the file in small chunks, so one chunk request must
// read only the rows it asked for, not every translation of the file.
func TestIntegrationSpellcheckJob_ChunkReadsOnlyRequestedRows(t *testing.T) {
	lt := ltStub(t, "adresse", "Adresse")
	defer lt.Close()
	s := setupScenario(t, lt.URL, "Senden Sie es an die adresse unten.")
	countPool, log := testutil.CountingPool(t)
	testutil.SeedSegmentsBulk(t, countPool, s.fileID, "de", 1, 500, "Ein Satz.")
	router := testutil.NewTestRouterWithSpellcheck(countPool, nil, lt.URL)

	log.Reset()
	rec := testutil.DoRequest(t, router, http.MethodPost,
		fmt.Sprintf("/api/jobs/%d/spellcheck", s.jobID),
		map[string]any{"translation_ids": []int64{s.translation.ID}},
		s.userID, s.tenantID)
	if rec.Code != http.StatusOK {
		t.Fatalf("spellcheck: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp spellcheckJobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data.Results) != 1 || resp.Data.Results[0].TranslationID != s.translation.ID {
		t.Fatalf("want the one requested result, got %+v", resp.Data.Results)
	}
	// The file has 501 translations; the request names one.
	if rows := log.TotalRows(); rows > 50 {
		t.Errorf("one-row chunk read %d rows from the database, want at most 50", rows)
	}
}
