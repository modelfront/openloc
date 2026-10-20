package spellcheck_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"openloc/internal/testutil"
)

// checkLocked locks the scenario's segment and runs a job check with the given
// extra request fields.
func (s scenario) checkLocked(t *testing.T, extra map[string]any) spellcheckJobResponse {
	t.Helper()
	if err := s.q.LockTranslation(testutil.Ctx(), s.translation.ID); err != nil {
		t.Fatalf("lock translation: %v", err)
	}
	body := map[string]any{"translation_ids": []int64{s.translation.ID}}
	for k, v := range extra {
		body[k] = v
	}
	rec := testutil.DoRequest(t, s.router, http.MethodPost,
		fmt.Sprintf("/api/jobs/%d/spellcheck", s.jobID), body, s.userID, s.tenantID)
	if rec.Code != http.StatusOK {
		t.Fatalf("spellcheck: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp spellcheckJobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return resp
}

// By default a locked segment is not spell-checked, even when its id is
// requested: a lock means "don't touch".
func TestIntegrationSpellcheckJob_SkipsLockedSegments(t *testing.T) {
	lt := ltStub(t, "adresse", "Adresse")
	defer lt.Close()
	s := setupScenario(t, lt.URL, "Senden Sie es an die adresse unten.")

	if got := s.checkLocked(t, nil).Data.Results; len(got) != 0 {
		t.Errorf("locked segment was checked: got %d results, want 0", len(got))
	}
}

// `include_locked` is the "All segments (includes locked)" scope: the locked
// segment is checked like any other.
func TestIntegrationSpellcheckJob_ChecksLockedSegments(t *testing.T) {
	lt := ltStub(t, "adresse", "Adresse")
	defer lt.Close()
	s := setupScenario(t, lt.URL, "Senden Sie es an die adresse unten.")

	resp := s.checkLocked(t, map[string]any{"include_locked": true})
	if len(resp.Data.Results) != 1 {
		t.Fatalf("with include_locked the locked segment must be checked; got %d results", len(resp.Data.Results))
	}
	if resp.Data.Results[0].TranslationID != s.translation.ID {
		t.Errorf("translation_id = %d, want %d", resp.Data.Results[0].TranslationID, s.translation.ID)
	}
}
