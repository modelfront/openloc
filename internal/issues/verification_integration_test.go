package issues_test

import (
	"net/http"
	"strings"
	"testing"

	"openloc/internal/issues"
	"openloc/internal/ptr"
	"openloc/internal/testutil"
)

// signer signs checks with the key the test router derives from its secret,
// as the assistant's preview does.
func signer(f *fixture) *issues.Service {
	return issues.NewService(f.pool, nil, nil, nil, []string{operatorEmail}, "").
		WithVerificationKey(issues.DeriveVerificationKey([]byte(testutil.TestJWTSecret)))
}

const verifiedNote = "The job has 120 segments; the editor lists 100."

// fieldsWithCheck is a bug report from the form with the assistant's check.
func (f *fixture) fieldsWithCheck(t *testing.T, title, result, token string) map[string]string {
	t.Helper()
	fields := bugFields(title)
	fields["verification"], fields["verification_note"], fields["verification_token"] = result, verifiedNote, token
	return fields
}

func (f *fixture) reporterToken(t *testing.T, result string) string {
	t.Helper()
	tok, err := signer(f).SignVerification(issues.Member{TenantID: f.tenant.ID, UserID: f.reporter.ID, Role: "linguist"},
		issues.Verification{Result: result, Note: verifiedNote})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// The form sends the assistant's check with its token: the support team sees
// it, and the reporter's org never gets the field, in the list or the issue.
func TestIntegrationIssueVerification_SignedCheckReachesTheSupportTeamOnly(t *testing.T) {
	f := newFixture(t)
	const title = "Editor stops at 100"
	// The raw response, so a field the view type does not know still shows.
	rec := f.asMember(t, "/api/issues", f.reporter.ID, f.tenant.ID,
		f.fieldsWithCheck(t, title, issues.VerificationVerified, f.reporterToken(t, issues.VerificationVerified)))
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), title) {
		t.Fatalf("report: %d %s", rec.Code, rec.Body.String())
	}
	var env struct{ Data issues.IssueView }
	testutil.DecodeJSON(t, rec, &env)
	id := env.Data.ID

	got := f.platformGet(t, id).Issue
	if ptr.Deref(got.ChatVerification) != issues.VerificationVerified || ptr.Deref(got.ChatVerificationNote) != verifiedNote {
		t.Errorf("support team sees %v / %v", got.ChatVerification, got.ChatVerificationNote)
	}
	for name, body := range map[string]string{
		"create response": rec.Body.String(),
		"issue":           f.orgRead(t, issuePath(id, ""), f.reporter.ID, title),
		"manager's issue": f.orgRead(t, issuePath(id, ""), f.colleague.ID, title),
		"list":            f.orgRead(t, "/api/issues", f.reporter.ID, title),
	} {
		if strings.Contains(body, "verification") || strings.Contains(body, verifiedNote) {
			t.Errorf("the org's %s carries the check: %s", name, body)
		}
	}
}

// The browser cannot claim a check the assistant did not give: without a
// valid token the report is filed without one.
func TestIntegrationIssueVerification_ForgedCheckIsDropped(t *testing.T) {
	f := newFixture(t)
	unverified := f.reporterToken(t, issues.VerificationUnverified)
	peerToken, err := signer(f).SignVerification(issues.Member{TenantID: f.tenant.ID, UserID: f.peer.ID, Role: "linguist"},
		issues.Verification{Result: issues.VerificationVerified, Note: verifiedNote})
	if err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{
		"no token":                    "",
		"made up":                     "AAAA.4102444800.AAAA",
		"signed for another result":   unverified,
		"signed for another reporter": peerToken,
	} {
		t.Run(name, func(t *testing.T) {
			created := f.reportWith(t, f.fieldsWithCheck(t, "Forged "+name, issues.VerificationVerified, token))
			if got := f.platformGet(t, created.ID).Issue; got.ChatVerification != nil || got.ChatVerificationNote != nil {
				t.Errorf("kept %v / %v", ptr.Deref(got.ChatVerification), ptr.Deref(got.ChatVerificationNote))
			}
		})
	}
}

// A token proves one check for one report: the same token on a second report,
// also after the first was deleted, files that report without the check.
func TestIntegrationIssueVerification_TokenWorksOnce(t *testing.T) {
	f := newFixture(t)
	token := f.reporterToken(t, issues.VerificationVerified)
	first := f.reportWith(t, f.fieldsWithCheck(t, "First", issues.VerificationVerified, token))
	if got := f.platformGet(t, first.ID).Issue; ptr.Deref(got.ChatVerification) != issues.VerificationVerified {
		t.Fatalf("the first report lost its check: %v", got.ChatVerification)
	}

	second := f.reportWith(t, f.fieldsWithCheck(t, "Second", issues.VerificationVerified, token))
	if got := f.platformGet(t, second.ID).Issue; got.ChatVerification != nil || got.ChatVerificationNote != nil {
		t.Errorf("a reused token put the check on a second report: %v / %v",
			ptr.Deref(got.ChatVerification), ptr.Deref(got.ChatVerificationNote))
	}

	if rec := f.asOperator(t, http.MethodDelete, platformPath(first.ID, ""), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	third := f.reportWith(t, f.fieldsWithCheck(t, "Third", issues.VerificationVerified, token))
	if got := f.platformGet(t, third.ID).Issue; got.ChatVerification != nil {
		t.Errorf("the token worked again after the first report was deleted: %v", ptr.Deref(got.ChatVerification))
	}
}

// The support team filters their queue by the assistant's check.
func TestIntegrationIssueVerification_PlatformFilter(t *testing.T) {
	f := newFixture(t)
	f.reportWith(t, f.fieldsWithCheck(t, "Checked", issues.VerificationVerified, f.reporterToken(t, issues.VerificationVerified)))
	plain := f.report(t, "Not checked")

	titles := func(query string) []string {
		rec := f.asOperator(t, http.MethodGet, "/api/platform/issues"+query, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("list %s: %d %s", query, rec.Code, rec.Body.String())
		}
		var page struct{ Data []issues.PlatformIssueView }
		testutil.DecodeJSON(t, rec, &page)
		var out []string
		for _, i := range page.Data {
			out = append(out, i.Title)
		}
		return out
	}
	if got := titles("?verification=verified"); len(got) != 1 || got[0] != "Checked" {
		t.Errorf("verified = %v", got)
	}
	if got := titles("?verification=unverified,not_reproduced"); len(got) != 0 {
		t.Errorf("unverified, not reproduced = %v", got)
	}
	if got := titles(""); len(got) != 2 {
		t.Errorf("no filter = %v (want both, incl. %q)", got, plain.Title)
	}
	if rec := f.asOperator(t, http.MethodGet, "/api/platform/issues?verification=maybe", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown verification: %d", rec.Code)
	}
}
