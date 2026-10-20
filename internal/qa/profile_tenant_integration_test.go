package qa_test

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Rules are stored and listed by profile id alone, so the profile lookup by id
// and tenant at the top of each route is the only tenant check. Another org's
// profile id gets 404, none of its rules, and writes no rule; the profile's
// own org on the same routes is the positive control.
func TestIntegrationQAProfile_OtherTenantGets404(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()

	victimUser, victim := testutil.RegisterUser(t, q, "qaprof-victim")
	attackerUser, attacker := testutil.RegisterUser(t, q, "qaprof-attacker")
	profile, err := q.CreateQAProfile(ctx, db.CreateQAProfileParams{TenantID: victim, Name: "Victim Strict"})
	if err != nil {
		t.Fatalf("CreateQAProfile: %v", err)
	}
	if _, err := q.UpsertQARule(ctx, db.UpsertQARuleParams{
		ProfileID: profile.ID, CheckType: "punctuation", Enabled: true, Severity: "warning", Config: []byte("{}"),
	}); err != nil {
		t.Fatalf("UpsertQARule: %v", err)
	}
	profilePath := fmt.Sprintf("/api/qa-profiles/%d", profile.ID)
	rulesPath := profilePath + "/rules"

	// storedRules maps check type to severity, straight from the database.
	storedRules := func(t *testing.T) map[string]string {
		t.Helper()
		rules, err := q.ListQARules(ctx, profile.ID)
		if err != nil {
			t.Fatalf("ListQARules: %v", err)
		}
		out := map[string]string{}
		for _, r := range rules {
			out[r.CheckType] = r.Severity
		}
		return out
	}
	assertNoLeak := func(t *testing.T, body string) {
		t.Helper()
		if strings.Contains(body, "punctuation") || strings.Contains(body, "Victim Strict") {
			t.Errorf("response leaks the other org's profile: %s", body)
		}
	}

	t.Run("GET profile", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, http.MethodGet, profilePath, nil, attackerUser, attacker)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
		assertNoLeak(t, rec.Body.String())

		rec = testutil.DoRequest(t, router, http.MethodGet, profilePath, nil, victimUser, victim)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data struct {
				Profile struct {
					Name string `json:"name"`
				} `json:"profile"`
				Rules []struct {
					CheckType string `json:"check_type"`
				} `json:"rules"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		if resp.Data.Profile.Name != "Victim Strict" || len(resp.Data.Rules) != 1 || resp.Data.Rules[0].CheckType != "punctuation" {
			t.Errorf("owner's GET: got %+v, want Victim Strict with its punctuation rule", resp.Data)
		}
	})

	t.Run("GET rules", func(t *testing.T) {
		rec := testutil.DoRequest(t, router, http.MethodGet, rulesPath, nil, attackerUser, attacker)
		testutil.AssertStatus(t, rec, http.StatusNotFound)
		assertNoLeak(t, rec.Body.String())

		rec = testutil.DoRequest(t, router, http.MethodGet, rulesPath, nil, victimUser, victim)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data []struct {
				CheckType string `json:"check_type"`
			} `json:"data"`
		}
		testutil.DecodeJSON(t, rec, &resp)
		if len(resp.Data) != 1 || resp.Data[0].CheckType != "punctuation" {
			t.Errorf("owner's GET rules: got %+v, want the punctuation rule", resp.Data)
		}
	})

	t.Run("POST rules", func(t *testing.T) {
		want := map[string]string{"punctuation": "warning"}
		for _, body := range []map[string]any{
			{"check_type": "punctuation", "enabled": false, "severity": "info"},
			{"check_type": "empty_target", "severity": "error"},
		} {
			rec := testutil.DoRequest(t, router, http.MethodPost, rulesPath, body, attackerUser, attacker)
			testutil.AssertStatus(t, rec, http.StatusNotFound)
		}
		if got := storedRules(t); !maps.Equal(got, want) {
			t.Fatalf("another org's POST changed the rules: got %v, want %v", got, want)
		}

		rec := testutil.DoRequest(t, router, http.MethodPost, rulesPath,
			map[string]any{"check_type": "empty_target", "severity": "error"}, victimUser, victim)
		testutil.AssertStatus(t, rec, http.StatusOK)
		want["empty_target"] = "error"
		if got := storedRules(t); !maps.Equal(got, want) {
			t.Errorf("owner's POST: rules = %v, want %v", got, want)
		}
	})
}
