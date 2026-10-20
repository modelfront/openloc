package file_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/parser"
	"openloc/internal/testutil"
)

// reimportFixtureRow is a one-line spec for a (segment, translation) pair to
// pre-seed the DB before invoking re-import. Lets each test focus on the
// scenario rather than the boilerplate.
type reimportFixtureRow struct {
	Key        string
	SourceText string
	ActiveText string // becomes translation.active_text (if non-empty)
	ActiveRich string // becomes .active_rich_text (if non-empty)
	Status     db.TranslationStatus
	Locked     bool
}

type reimportFixture struct {
	TenantID          int64
	AdminID           int64
	MemberID          int64
	ProjectID         int64
	SettingsProfileID int64
	FileID            int64
	TargetTag         string
	// SegmentKey → translation_id, so tests can assert per-key state cheaply.
	TranslationByKey map[string]int64
}

// seedReimportFixture builds a tenant + admin user + member user + project +
// file + segments + translations matching the given specs. Returns the IDs
// tests need to either invoke the HTTP route or assert on DB state directly.
func seedReimportFixture(t *testing.T, pool *pgxpool.Pool, q db.Querier, tenantName, projectName string, targets []string, primaryTarget string, rows []reimportFixtureRow) reimportFixture {
	t.Helper()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, tenantName)
	admin := testutil.SeedUserWithRole(t, q, tenant.ID, fmt.Sprintf("admin-%s@test.com", tenantName), "pass", "admin")
	member := testutil.SeedUserWithRole(t, q, tenant.ID, fmt.Sprintf("member-%s@test.com", tenantName), "pass", "member")
	project := testutil.SeedProject(t, q, tenant.ID, projectName, "en", targets)
	file := testutil.SeedFile(t, q, project.ID, "test.xliff", db.FileFormatXliff)

	transByKey := make(map[string]int64, len(rows))
	for i, spec := range rows {
		key := spec.Key
		seg, err := q.CreateSegment(ctx, db.CreateSegmentParams{
			FileID:     file.ID,
			SourceText: spec.SourceText,
			SegmentKey: &key,
			Position:   int32(i),
		})
		if err != nil {
			t.Fatalf("seedReimportFixture: CreateSegment(%q): %v", spec.Key, err)
		}
		// Active_text / active_rich_text are the canonical visible
		// target.
		var (
			activeText, activeRich any
		)
		if spec.ActiveText != "" {
			activeText = spec.ActiveText
		}
		if spec.ActiveRich != "" {
			activeRich = spec.ActiveRich
		}
		var translationID int64
		err = pool.QueryRow(ctx, `
			INSERT INTO translations (segment_id, target_language, status, active_text, active_rich_text, locked)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id`,
			seg.ID, primaryTarget, spec.Status, activeText, activeRich, spec.Locked,
		).Scan(&translationID)
		if err != nil {
			t.Fatalf("seedReimportFixture: insert translation (%q): %v", spec.Key, err)
		}
		transByKey[spec.Key] = translationID
	}

	return reimportFixture{
		TenantID:          tenant.ID,
		AdminID:           admin.ID,
		MemberID:          member.ID,
		ProjectID:         project.ID,
		SettingsProfileID: project.SettingsProfileID,
		FileID:            file.ID,
		TargetTag:         primaryTarget,
		TranslationByKey:  transByKey,
	}
}

// buildXLIFF writes a minimal XLIFF 1.2 body with one trans-unit per spec.
// `target` may be empty (no <target/>) when the spec calls for it.
type xliffSpec struct {
	Key    string
	Source string
	Target string // empty → <target></target>
}

func buildXLIFF(t *testing.T, sourceLang, targetLang string, units []xliffSpec) string {
	t.Helper()
	body := `<?xml version="1.0" encoding="utf-8"?>
<xliff xmlns="urn:oasis:names:tc:xliff:document:1.2" version="1.2">
  <file source-language="` + sourceLang + `" target-language="` + targetLang + `">
    <body>
`
	for _, u := range units {
		body += fmt.Sprintf("      <trans-unit id=\"%s\">\n        <source>%s</source>\n", u.Key, u.Source)
		if u.Target != "" {
			body += fmt.Sprintf("        <target>%s</target>\n", u.Target)
		} else {
			body += "        <target></target>\n"
		}
		body += "      </trans-unit>\n"
	}
	body += `    </body>
  </file>
</xliff>`
	return body
}

// translationState is the post-reimport snapshot the integration tests check.
// Returns every column the re-import touches so assertions can be precise:
// the fast path (status flip, active_text) is one branch, but active_rich_text
// and edited_by need separate guards or future SQL refactors could silently
// regress them.
// The reimport writes land in active_text / active_rich_text (the canonical
// visible target); provenance is origin.
type translationState struct {
	Status         string
	ActiveText     string
	ActiveRichText string
	Locked         bool
	Origin         *string
	EditedBy       *int64
}

func loadTranslation(t *testing.T, pool *pgxpool.Pool, id int64) translationState {
	t.Helper()
	var s translationState
	err := pool.QueryRow(context.Background(),
		`SELECT status::text, COALESCE(active_text, ''), COALESCE(active_rich_text, ''),
		        locked, origin::text, edited_by
		 FROM translations WHERE id = $1`,
		id,
	).Scan(&s.Status, &s.ActiveText, &s.ActiveRichText, &s.Locked, &s.Origin, &s.EditedBy)
	if err != nil {
		t.Fatalf("loadTranslation(%d): %v", id, err)
	}
	return s
}

// reimportPath returns the route URL for a fixture's file + lang.
func reimportPath(fileID int64, lang, dryRun, force string) string {
	q := "?lang=" + lang
	if dryRun != "" {
		q += "&dry_run=" + dryRun
	}
	if force != "" {
		q += "&force=" + force
	}
	return fmt.Sprintf("/api/files/%d/reimport-xliff%s", fileID, q)
}

func decodeReimportResult(t *testing.T, body []byte) struct {
	Data struct {
		Updated             int      `json:"updated"`
		SkippedLocked       int      `json:"skipped_locked"`
		SkippedApproved     int      `json:"skipped_approved"`
		UntouchedNoChange   int      `json:"untouched_no_change"`
		ExtraInUpload       int      `json:"extra_in_upload"`
		MissingFromUpload   int      `json:"missing_from_upload"`
		ForceRequired       bool     `json:"force_required"`
		Applied             bool     `json:"applied"`
		SkippedApprovedKeys []string `json:"skipped_approved_keys"`
	} `json:"data"`
} {
	t.Helper()
	var resp struct {
		Data struct {
			Updated             int      `json:"updated"`
			SkippedLocked       int      `json:"skipped_locked"`
			SkippedApproved     int      `json:"skipped_approved"`
			UntouchedNoChange   int      `json:"untouched_no_change"`
			ExtraInUpload       int      `json:"extra_in_upload"`
			MissingFromUpload   int      `json:"missing_from_upload"`
			ForceRequired       bool     `json:"force_required"`
			Applied             bool     `json:"applied"`
			SkippedApprovedKeys []string `json:"skipped_approved_keys"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decodeReimportResult: %v (body: %s)", err, string(body))
	}
	return resp
}

// =====================================================================
// HTTP-level integration tests
// =====================================================================

func TestIntegrationReimportXLIFF_HappyPath_AdminApply(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportHappy", "FrJob", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
		{Key: "u2", SourceText: "Goodbye", Status: db.TranslationStatusNeedsReview},
	})

	xliff := buildXLIFF(t, "en", "fr", []xliffSpec{
		{Key: "u1", Source: "Hello", Target: "Bonjour"},
		{Key: "u2", Source: "Goodbye", Target: "Au revoir"},
	})

	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, fx.TargetTag, "", ""),
		"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)

	res := decodeReimportResult(t, rec.Body.Bytes())
	if res.Data.Updated != 2 || !res.Data.Applied || res.Data.ForceRequired {
		t.Fatalf("happy path counts wrong: %+v", res.Data)
	}

	// Anti-tautology guard: assert DB state actually changed (not just that the
	// API claims to have applied). Asserts active_text and active_rich_text
	// together — the SQL writes both, and a test that checks only one would
	// miss a refactor that splits them.
	expectedTexts := map[string]string{"u1": "Bonjour", "u2": "Au revoir"}
	for _, key := range []string{"u1", "u2"} {
		st := loadTranslation(t, pool, fx.TranslationByKey[key])
		want := expectedTexts[key]
		if st.Status != "needs_review" {
			t.Errorf("%s: status want needs_review, got %s", key, st.Status)
		}
		if st.Origin == nil || *st.Origin != "file_import" {
			t.Errorf("%s: origin want file_import, got %v", key, st.Origin)
		}
		if st.ActiveText != want {
			t.Errorf("%s active_text: want %q, got %q", key, want, st.ActiveText)
		}
		// edited_by: must be the admin who triggered the re-import (audit trail).
		if st.EditedBy == nil || *st.EditedBy != fx.AdminID {
			t.Errorf("%s edited_by: want admin %d, got %v", key, fx.AdminID, st.EditedBy)
		}
	}
}

func TestIntegrationReimportXLIFF_DryRun_DoesNotWrite(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	fx := seedReimportFixture(t, pool, q, "ReimportDryRun", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
	})
	xliff := buildXLIFF(t, "en", "fr", []xliffSpec{{Key: "u1", Source: "Hello", Target: "Bonjour"}})

	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, fx.TargetTag, "true", ""),
		"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)

	res := decodeReimportResult(t, rec.Body.Bytes())
	if res.Data.Updated != 1 {
		t.Errorf("DryRun: expected count to still report 1, got %d", res.Data.Updated)
	}
	if res.Data.Applied {
		t.Error("DryRun: Applied must be false")
	}

	// The DB must NOT have been mutated.
	st := loadTranslation(t, pool, fx.TranslationByKey["u1"])
	if st.Status != string(db.TranslationStatusNeedsReview) {
		t.Errorf("DryRun mutated status: %s", st.Status)
	}
	if st.ActiveText != "" {
		t.Errorf("DryRun mutated text: active=%q", st.ActiveText)
	}
	if st.EditedBy != nil {
		t.Errorf("DryRun stamped edited_by: %v", st.EditedBy)
	}
}

func TestIntegrationReimportXLIFF_LockedSkipped(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportLocked", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", ActiveText: "OldValue", Status: db.TranslationStatusNeedsReview, Locked: true},
		{Key: "u2", SourceText: "Goodbye", Status: db.TranslationStatusNeedsReview, Locked: false},
	})
	xliff := buildXLIFF(t, "en", "fr", []xliffSpec{
		{Key: "u1", Source: "Hello", Target: "NewValue"},
		{Key: "u2", Source: "Goodbye", Target: "Au revoir"},
	})

	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, fx.TargetTag, "", "true"),
		"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)

	res := decodeReimportResult(t, rec.Body.Bytes())
	if res.Data.SkippedLocked != 1 || res.Data.Updated != 1 {
		t.Fatalf("locked-skip counts wrong: %+v", res.Data)
	}

	// Anti-tautology guard: even with force=true, the locked row's active_text
	// must remain "OldValue".
	stU1 := loadTranslation(t, pool, fx.TranslationByKey["u1"])
	if stU1.ActiveText != "OldValue" {
		t.Errorf("locked row mutated despite force=true: active=%q", stU1.ActiveText)
	}
	if !stU1.Locked {
		t.Error("locked flag flipped to false unexpectedly")
	}

	// And the unlocked row was applied.
	stU2 := loadTranslation(t, pool, fx.TranslationByKey["u2"])
	if stU2.ActiveText != "Au revoir" {
		t.Errorf("u2 should have been updated: active=%q", stU2.ActiveText)
	}
}

func TestIntegrationReimportXLIFF_ApprovedRequiresForce(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportApproved", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", ActiveText: "Bonjour", Status: db.TranslationStatusApproved},
	})
	xliff := buildXLIFF(t, "en", "fr", []xliffSpec{{Key: "u1", Source: "Hello", Target: "Salut"}})

	// Without force: must NOT apply, must signal force_required.
	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, fx.TargetTag, "", ""),
		"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)
	res := decodeReimportResult(t, rec.Body.Bytes())
	if !res.Data.ForceRequired || res.Data.Applied || res.Data.SkippedApproved != 1 {
		t.Fatalf("approved-no-force counts wrong: %+v", res.Data)
	}
	if len(res.Data.SkippedApprovedKeys) != 1 || res.Data.SkippedApprovedKeys[0] != "u1" {
		t.Errorf("SkippedApprovedKeys: expected [u1], got %v", res.Data.SkippedApprovedKeys)
	}
	// DB unchanged.
	st := loadTranslation(t, pool, fx.TranslationByKey["u1"])
	if st.Status != "approved" || st.ActiveText != "Bonjour" {
		t.Errorf("approved row mutated without force: status=%s, active=%q", st.Status, st.ActiveText)
	}

	// With force=true: must apply, and edited_by must record the admin.
	rec2 := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, fx.TargetTag, "", "true"),
		"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec2, http.StatusOK)
	res2 := decodeReimportResult(t, rec2.Body.Bytes())
	if !res2.Data.Applied || res2.Data.Updated != 1 {
		t.Fatalf("approved-with-force counts wrong: %+v", res2.Data)
	}
	st = loadTranslation(t, pool, fx.TranslationByKey["u1"])
	if st.Status != "needs_review" || st.ActiveText != "Salut" {
		t.Errorf("force-overridden row not updated: status=%s, active=%q", st.Status, st.ActiveText)
	}
	if st.EditedBy == nil || *st.EditedBy != fx.AdminID {
		t.Errorf("force-overridden row missing edited_by: want %d, got %v", fx.AdminID, st.EditedBy)
	}
}

func TestIntegrationReimportXLIFF_EmptyTargetIsNoop(t *testing.T) {
	// R4: empty <target/> in the upload must NOT wipe the row.
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportEmpty", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", ActiveText: "Bonjour", Status: db.TranslationStatusApproved},
	})
	xliff := buildXLIFF(t, "en", "fr", []xliffSpec{{Key: "u1", Source: "Hello", Target: ""}})

	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, fx.TargetTag, "", "true"),
		"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)

	res := decodeReimportResult(t, rec.Body.Bytes())
	if res.Data.UntouchedNoChange != 1 || res.Data.Updated != 0 {
		t.Fatalf("empty-target-noop counts wrong: %+v", res.Data)
	}
	st := loadTranslation(t, pool, fx.TranslationByKey["u1"])
	if st.ActiveText != "Bonjour" {
		t.Errorf("approved row was wiped by empty target — R4 broken: got %q", st.ActiveText)
	}
}

func TestIntegrationReimportXLIFF_ExtraAndMissing(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportEM", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
		{Key: "u2", SourceText: "World", Status: db.TranslationStatusNeedsReview},
	})
	// Upload has u1 (matches) + u_extra (not in DB). Missing: u2.
	xliff := buildXLIFF(t, "en", "fr", []xliffSpec{
		{Key: "u1", Source: "Hello", Target: "Bonjour"},
		{Key: "u_extra", Source: "Stranger", Target: "Étranger"},
	})

	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, fx.TargetTag, "", ""),
		"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)

	res := decodeReimportResult(t, rec.Body.Bytes())
	if res.Data.Updated != 1 || res.Data.ExtraInUpload != 1 || res.Data.MissingFromUpload != 1 {
		t.Fatalf("extra+missing counts wrong: %+v", res.Data)
	}
}

func TestIntegrationReimportXLIFF_CrossTenant_NotFound(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	// Tenant A owns the file.
	fxA := seedReimportFixture(t, pool, q, "ReimportTA", "PA", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
	})

	// Tenant B's admin tries to re-import against A's file.
	tenantB := testutil.SeedTenant(t, q, "ReimportTB")
	adminB := testutil.SeedUserWithRole(t, q, tenantB.ID, "ab@test.com", "pass", "admin")

	xliff := buildXLIFF(t, "en", "fr", []xliffSpec{{Key: "u1", Source: "Hello", Target: "Bonjour"}})
	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fxA.FileID, "fr", "", ""),
		"reimport.xliff", xliff, adminB.ID, tenantB.ID, "admin")
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// Anti-tautology guard: the tenant-A row must be untouched.
	st := loadTranslation(t, pool, fxA.TranslationByKey["u1"])
	if st.ActiveText != "" {
		t.Errorf("cross-tenant attempt mutated A's data: %q", st.ActiveText)
	}
}

// Import is open to every role; a linguist needs an assignment on the task of
// the language being imported, not only on some task of the file.
func TestIntegrationReimportXLIFF_RoleAccess(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	cases := []struct {
		name       string
		role       string
		assignLang string // "" = no task assignment
		wantStatus int
	}{
		{"project manager", "project_manager", "", http.StatusOK},
		{"linguist on the task", "linguist", "fr", http.StatusOK},
		{"linguist on another language's task", "linguist", "de", http.StatusForbidden},
		{"linguist with no task", "linguist", "", http.StatusForbidden},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := seedReimportFixture(t, pool, q, fmt.Sprintf("ReimportRole%d", i), "P", []string{"fr", "de"}, "fr", []reimportFixtureRow{
				{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
			})
			user := testutil.SeedUserWithRole(t, q, fx.TenantID, fmt.Sprintf("role%d@test.com", i), "pass", tc.role)
			for _, lang := range []string{"fr", "de"} {
				job := testutil.SeedJob(t, q, fx.FileID, lang)
				var assignee *int64
				if lang == tc.assignLang {
					assignee = &user.ID
				}
				testutil.SeedJobStep(t, q, fx.SettingsProfileID, job.ID, assignee)
			}

			xliff := buildXLIFF(t, "en", "fr", []xliffSpec{{Key: "u1", Source: "Hello", Target: "Bonjour"}})
			rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, fx.TargetTag, "", ""),
				"reimport.xliff", xliff, user.ID, fx.TenantID, tc.role)
			testutil.AssertStatus(t, rec, tc.wantStatus)

			want := ""
			if tc.wantStatus == http.StatusOK {
				want = "Bonjour"
			}
			if st := loadTranslation(t, pool, fx.TranslationByKey["u1"]); st.ActiveText != want {
				t.Errorf("active_text = %q, want %q", st.ActiveText, want)
			}
		})
	}
}

func TestIntegrationReimportXLIFF_NonXLIFFFormat_BadRequest(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportFmt", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
	})

	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, fx.TargetTag, "", ""),
		"upload.json", `{"hello":"world"}`, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}

func TestIntegrationReimportXLIFF_LangNotInProject_BadRequest(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportLang", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
	})
	xliff := buildXLIFF(t, "en", "de", []xliffSpec{{Key: "u1", Source: "Hello", Target: "Hallo"}})

	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, "de", "", ""),
		"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}

func TestIntegrationReimportXLIFF_MissingLangParam_BadRequest(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportNoLang", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
	})
	xliff := buildXLIFF(t, "en", "fr", []xliffSpec{{Key: "u1", Source: "Hello", Target: "Bonjour"}})

	rec := testutil.DoFileUploadWithRole(t, router, fmt.Sprintf("/api/files/%d/reimport-xliff", fx.FileID),
		"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}

func TestIntegrationReimportXLIFF_LangParamIsCaseInsensitive(t *testing.T) {
	// Auditor HIGH A: project's target_languages are stored normalized
	// (lowercase BCP-47). A caller passing `?lang=FR` must match the
	// normalized "fr" entry, not 400 with "not in target_languages."
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportLangCase", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
	})
	xliff := buildXLIFF(t, "en", "fr", []xliffSpec{{Key: "u1", Source: "Hello", Target: "Bonjour"}})

	// Pass `lang=FR` (uppercase) — must normalize to "fr" and succeed.
	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, "FR", "", ""),
		"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)

	res := decodeReimportResult(t, rec.Body.Bytes())
	if res.Data.Updated != 1 || !res.Data.Applied {
		t.Fatalf("uppercase lang param failed normalization: %+v", res.Data)
	}
}

func TestIntegrationReimportXLIFF_DuplicateKeys_BadRequest(t *testing.T) {
	// Auditor MEDIUM B: a duplicate-key XLIFF would silently drop the first
	// occurrence — the worst possible failure for round-trip. Reject with 400.
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportDup", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
	})
	// Two `<trans-unit id="u1">` — only the second would land otherwise.
	xliff := buildXLIFF(t, "en", "fr", []xliffSpec{
		{Key: "u1", Source: "Hello", Target: "Bonjour-A"},
		{Key: "u1", Source: "Hello", Target: "Bonjour-B"},
	})

	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, fx.TargetTag, "", ""),
		"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Anti-tautology guard: the row must not have been mutated by the
	// "second wins" silent-drop path.
	st := loadTranslation(t, pool, fx.TranslationByKey["u1"])
	if st.ActiveText != "" {
		t.Errorf("dup-keys upload silently mutated the row: active_text=%q", st.ActiveText)
	}
}

func TestIntegrationReimportXLIFF_LangParamTooLong_BadRequest(t *testing.T) {
	// Auditor MEDIUM D: very long `?lang=` values must 400 generically without
	// echoing the raw input in either response or log.
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportLangTooLong", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
	})
	xliff := buildXLIFF(t, "en", "fr", []xliffSpec{{Key: "u1", Source: "Hello", Target: "Bonjour"}})

	bogusLang := strings.Repeat("a", 100)
	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, bogusLang, "", ""),
		"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Body must NOT contain the bogus value (anti-log-spam guard).
	if strings.Contains(rec.Body.String(), bogusLang) {
		t.Errorf("response echoed bogus lang value: %s", rec.Body.String())
	}
}

func TestIntegrationReimportXLIFF_XLIFFTargetLanguageMismatch_BadRequest(t *testing.T) {
	// Self-audit HIGH-1: if the user passes `?lang=fr` but uploads an XLIFF
	// with `target-language="de"`, every German <target> would otherwise land
	// in the French translations. Architecture spec §E required a cross-check.
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportLangMismatch", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
	})
	// XLIFF declares target-language="de"; user requests lang=fr. Mismatch.
	xliff := buildXLIFF(t, "en", "de", []xliffSpec{{Key: "u1", Source: "Hello", Target: "Hallo"}})

	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, "fr", "", ""),
		"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// Anti-tautology: the French row must NOT have German text.
	st := loadTranslation(t, pool, fx.TranslationByKey["u1"])
	if st.ActiveText != "" {
		t.Errorf("lang-mismatch upload corrupted row: active=%q", st.ActiveText)
	}
}

func TestIntegrationReimportXLIFF_RichTextRoundTrip(t *testing.T) {
	// Asserts active_rich_text as well as active_text: a SQL refactor that
	// drops the rich column would still pass the tests that check only the
	// plain one.
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportRich", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello world", Status: db.TranslationStatusNeedsReview},
	})
	// XLIFF with inline tag <ph id="1"/> in the target.
	body := `<?xml version="1.0" encoding="utf-8"?>
<xliff xmlns="urn:oasis:names:tc:xliff:document:1.2" version="1.2">
  <file source-language="en" target-language="fr">
    <body>
      <trans-unit id="u1">
        <source>Hello world</source>
        <target>Bonjour <ph id="1"/>monde</target>
      </trans-unit>
    </body>
  </file>
</xliff>`

	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, fx.TargetTag, "", ""),
		"reimport.xliff", body, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)
	res := decodeReimportResult(t, rec.Body.Bytes())
	if res.Data.Updated != 1 || !res.Data.Applied {
		t.Fatalf("rich-text run: %+v", res.Data)
	}

	st := loadTranslation(t, pool, fx.TranslationByKey["u1"])
	if !strings.Contains(st.ActiveRichText, `<ph id="1"`) {
		t.Errorf("active_rich_text did not preserve inline tag: %q", st.ActiveRichText)
	}
	// Plain text strips tags via the parser — verify the plain side made it too.
	if st.ActiveText != "Bonjour monde" {
		t.Errorf("active_text: want 'Bonjour monde', got %q", st.ActiveText)
	}
}

func TestIntegrationReimportXLIFF_RevisionLock_MatchingPasses(t *testing.T) {
	// C: when the client passes ?revision=<files.updated_at>, matching value
	// passes through (no 409). Validates the optimistic-lock path doesn't
	// false-positive when nothing has changed.
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportRevOK", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
	})

	var updatedAt time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT updated_at FROM files WHERE id = $1`, fx.FileID).Scan(&updatedAt); err != nil {
		t.Fatalf("read files.updated_at: %v", err)
	}
	rfc := updatedAt.UTC().Format(time.RFC3339Nano)

	xliff := buildXLIFF(t, "en", "fr", []xliffSpec{{Key: "u1", Source: "Hello", Target: "Bonjour"}})
	path := fmt.Sprintf("/api/files/%d/reimport-xliff?lang=fr&revision=%s", fx.FileID, url.QueryEscape(rfc))
	rec := testutil.DoFileUploadWithRole(t, router, path, "reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)
	res := decodeReimportResult(t, rec.Body.Bytes())
	if res.Data.Updated != 1 || !res.Data.Applied {
		t.Errorf("matching revision should pass: %+v", res.Data)
	}
}

func TestIntegrationReimportXLIFF_RevisionLock_MismatchReturns409(t *testing.T) {
	// C: stale `?revision=` returns 409 with current revision in body.
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportRev409", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
	})

	staleRev := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339Nano)
	xliff := buildXLIFF(t, "en", "fr", []xliffSpec{{Key: "u1", Source: "Hello", Target: "Bonjour"}})
	path := fmt.Sprintf("/api/files/%d/reimport-xliff?lang=fr&revision=%s", fx.FileID, url.QueryEscape(staleRev))
	rec := testutil.DoFileUploadWithRole(t, router, path, "reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusConflict)

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["current_revision"] == "" || resp["current_revision"] == nil {
		t.Errorf("response should include current_revision: %+v", resp)
	}

	// Anti-tautology: row must NOT have been updated.
	st := loadTranslation(t, pool, fx.TranslationByKey["u1"])
	if st.ActiveText != "" {
		t.Errorf("revision-mismatch upload mutated row: %q", st.ActiveText)
	}
}

func TestIntegrationReimportXLIFF_RevisionLock_BadFormatReturns400(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	fx := seedReimportFixture(t, pool, q, "ReimportRevBad", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
	})
	xliff := buildXLIFF(t, "en", "fr", []xliffSpec{{Key: "u1", Source: "Hello", Target: "Bonjour"}})
	path := fmt.Sprintf("/api/files/%d/reimport-xliff?lang=fr&revision=garbage", fx.FileID)
	rec := testutil.DoFileUploadWithRole(t, router, path, "reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
}

func TestIntegrationReimportXLIFF_RenumberedIDsDoNotMatch(t *testing.T) {
	// Segments match by trans-unit id only: an XLIFF whose ids an external
	// tool renumbered writes nothing, even though every source is unchanged.
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportSrc", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "old-1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
		{Key: "old-2", SourceText: "Goodbye", Status: db.TranslationStatusNeedsReview},
	})
	xliff := buildXLIFF(t, "en", "fr", []xliffSpec{
		{Key: "new-A", Source: "Hello", Target: "Bonjour"},
		{Key: "new-B", Source: "Goodbye", Target: "Au revoir"},
	})

	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, fx.TargetTag, "", ""),
		"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)
	res := decodeReimportResult(t, rec.Body.Bytes())
	if res.Data.Updated != 0 || res.Data.MissingFromUpload != 2 || res.Data.ExtraInUpload != 2 {
		t.Fatalf("want 0/2/2 updated/missing/extra, got %+v", res.Data)
	}
	if st := loadTranslation(t, pool, fx.TranslationByKey["old-1"]); st.ActiveText == "Bonjour" {
		t.Error("old-1 took the renumbered unit's target")
	}
}

func TestIntegrationReimportXLIFF_TxAtomicity_FKViolationRollsBackAll(t *testing.T) {
	// Self-audit A: prove that any SQL error mid-loop rolls back EVERY queued
	// update. Approach: pass an EditedBy user ID that doesn't exist; the FK
	// constraint on translations.edited_by fires immediately on UPDATE,
	// causing the tx to abort. Anti-tautology: if my code accidentally
	// committed per-iteration (or ignored UPDATE errors), some translations
	// would have active_text set despite the FK error.
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	fx := seedReimportFixture(t, pool, q, "ReimportAtomic", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
		{Key: "u2", SourceText: "Goodbye", Status: db.TranslationStatusNeedsReview},
		{Key: "u3", SourceText: "World", Status: db.TranslationStatusNeedsReview},
	})

	// Bypass the HTTP handler — call the primitive directly so we can pass
	// a deliberately-invalid EditedBy. (The handler always uses a real JWT
	// user ID, so this path can't be exercised end-to-end.)
	const phantomUserID = int64(9_999_999)
	_, err := file.ReimportXLIFF(context.Background(), pool, file.ReimportXLIFFRequest{
		FileID:         fx.FileID,
		TargetLanguage: "fr",
		IncomingSegments: []parser.Segment{
			{Key: "u1", Target: "Bonjour"},
			{Key: "u2", Target: "Au revoir"},
			{Key: "u3", Target: "Monde"},
		},
		EditedBy: phantomUserID,
		DryRun:   false,
		Force:    false,
	})
	if err == nil {
		t.Fatal("expected FK violation error, got nil — atomicity test useless without a forced failure")
	}
	if !strings.Contains(err.Error(), "violates foreign key") && !strings.Contains(err.Error(), "fkey") && !strings.Contains(err.Error(), "constraint") {
		// Sanity check: make sure we got the EXPECTED kind of error. If a
		// future SQL change drops the FK, this test would silently start
		// passing for the wrong reason — fail loudly instead.
		t.Fatalf("expected FK constraint error, got: %v", err)
	}

	// Anti-tautology: NO row should have a non-empty active_text.
	// If atomicity is broken, at least one would have been written.
	for _, key := range []string{"u1", "u2", "u3"} {
		st := loadTranslation(t, pool, fx.TranslationByKey[key])
		if st.ActiveText != "" {
			t.Errorf("%s: partial write detected after FK error — atomicity broken: active=%q",
				key, st.ActiveText)
		}
		// The initial/cleared state is needs_review (untranslated is
		// derived from active_text IS NULL, asserted above). Status unchanged
		// means still needs_review.
		if st.Status != "needs_review" {
			t.Errorf("%s: status changed despite tx abort: %s", key, st.Status)
		}
	}
}

func TestIntegrationReimportXLIFF_HardCap_TooManySegments(t *testing.T) {
	// B-lite: > MaxReimportSegments returns 413. Build a synthetic large
	// XLIFF (just over the cap) and verify rejection. We don't seed
	// matching DB rows — the cap fires on segment count alone, before any
	// DB work.
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportCap", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
	})

	// Build an XLIFF with MaxReimportSegments + 1 trans-units. Use a string
	// builder for speed; each unit is ~80 bytes so 50001 units = ~4MB body.
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	b.WriteString(`<xliff xmlns="urn:oasis:names:tc:xliff:document:1.2" version="1.2">` + "\n")
	b.WriteString(`  <file source-language="en" target-language="fr"><body>` + "\n")
	for i := 0; i <= file.MaxReimportSegments; i++ {
		fmt.Fprintf(&b, `<trans-unit id="u%d"><source>S%d</source><target>T%d</target></trans-unit>`+"\n", i, i, i)
	}
	b.WriteString(`</body></file></xliff>`)

	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, fx.TargetTag, "", ""),
		"big.xliff", b.String(), fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusRequestEntityTooLarge)
	if got := rec.Header().Get("X-Reimport-Max-Segments"); got != fmt.Sprintf("%d", file.MaxReimportSegments) {
		t.Errorf("X-Reimport-Max-Segments header: want %d, got %q", file.MaxReimportSegments, got)
	}

	// Anti-tautology: even though the upload was rejected, no DB row should
	// have changed.
	st := loadTranslation(t, pool, fx.TranslationByKey["u1"])
	if st.ActiveText != "" {
		t.Errorf("oversize upload mutated DB despite 413: %q", st.ActiveText)
	}
}

func TestIntegrationReimportXLIFF_KeysTruncatedAt100(t *testing.T) {
	// F: per-category key list is capped at MaxKeysPerCategory; truncated
	// flag is set when the cap is hit. Counts are NOT capped.
	if file.MaxKeysPerCategory != 100 {
		t.Skipf("test pinned to MaxKeysPerCategory=100; got %d", file.MaxKeysPerCategory)
	}
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	// Seed 150 approved rows. Re-import with 150 different targets. Without
	// force, all 150 are skipped_approved; the keys list should be capped at
	// 100 with truncated=true.
	rows := make([]reimportFixtureRow, 150)
	for i := range rows {
		rows[i] = reimportFixtureRow{
			Key:        fmt.Sprintf("k%03d", i),
			SourceText: fmt.Sprintf("Source %d", i),
			ActiveText: fmt.Sprintf("Old%d", i),
			Status:     db.TranslationStatusApproved,
		}
	}
	fx := seedReimportFixture(t, pool, q, "ReimportTrunc", "P", []string{"fr"}, "fr", rows)

	units := make([]xliffSpec, 150)
	for i := range units {
		units[i] = xliffSpec{Key: fmt.Sprintf("k%03d", i), Source: fmt.Sprintf("Source %d", i), Target: fmt.Sprintf("New%d", i)}
	}
	xliff := buildXLIFF(t, "en", "fr", units)

	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, fx.TargetTag, "", ""),
		"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)

	var resp struct {
		Data struct {
			SkippedApproved          int      `json:"skipped_approved"`
			SkippedApprovedKeys      []string `json:"skipped_approved_keys"`
			SkippedApprovedTruncated bool     `json:"skipped_approved_truncated"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.SkippedApproved != 150 {
		t.Errorf("count: want 150, got %d", resp.Data.SkippedApproved)
	}
	if len(resp.Data.SkippedApprovedKeys) != 100 {
		t.Errorf("keys list: want 100 (capped), got %d", len(resp.Data.SkippedApprovedKeys))
	}
	if !resp.Data.SkippedApprovedTruncated {
		t.Errorf("truncated flag should be true when keys list capped")
	}
}

func TestIntegrationReimportXLIFF_ProjectEventOnApply(t *testing.T) {
	// An applied re-import is recorded as a file.reimported project event,
	// with the admin who ran it and the report counts.
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportEvent", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
		{Key: "u2", SourceText: "World", Status: db.TranslationStatusNeedsReview},
	})
	xliff := buildXLIFF(t, "en", "fr", []xliffSpec{
		{Key: "u1", Source: "Hello", Target: "Bonjour"},
		{Key: "u2", Source: "World", Target: "Monde"},
	})

	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, fx.TargetTag, "", ""),
		"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)

	var (
		count   int
		actorID *int64
		details struct {
			TargetLanguage string `json:"target_language"`
			Updated        int    `json:"updated"`
		}
		raw []byte
	)
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM project_events WHERE file_id = $1 AND event_type = 'file.reimported'`, fx.FileID).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 1 {
		t.Fatalf("want 1 file.reimported event, got %d", count)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT actor_id, details FROM project_events WHERE file_id = $1 AND event_type = 'file.reimported'`, fx.FileID).Scan(&actorID, &raw); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if err := json.Unmarshal(raw, &details); err != nil {
		t.Fatalf("decode details: %v", err)
	}
	if actorID == nil || *actorID != fx.AdminID {
		t.Errorf("actor_id: want admin %d, got %v", fx.AdminID, actorID)
	}
	if details.TargetLanguage != "fr" || details.Updated != 2 {
		t.Errorf("details: want fr/2 updated, got %+v", details)
	}
}

func TestIntegrationReimportXLIFF_NoProjectEventOnDryRun(t *testing.T) {
	// A preview changes nothing, so it records nothing.
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportEventDry", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
	})
	xliff := buildXLIFF(t, "en", "fr", []xliffSpec{{Key: "u1", Source: "Hello", Target: "Bonjour"}})

	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, fx.TargetTag, "true", ""),
		"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM project_events WHERE file_id = $1 AND event_type = 'file.reimported'`, fx.FileID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("a dry run recorded %d file.reimported events", count)
	}
}

func TestIntegrationReimportXLIFF_Idempotent_SecondRunIsAllNoop(t *testing.T) {
	// Running the same re-import twice should produce updates the first time
	// and only no-op counts the second time. Useful regression guard for the
	// no-change detection.
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportIdem", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
	})
	xliff := buildXLIFF(t, "en", "fr", []xliffSpec{{Key: "u1", Source: "Hello", Target: "Bonjour"}})

	// First run: applied.
	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, fx.TargetTag, "", ""),
		"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)
	if r := decodeReimportResult(t, rec.Body.Bytes()); r.Data.Updated != 1 || !r.Data.Applied {
		t.Fatalf("first run wrong: %+v", r.Data)
	}

	// Second run: must be all untouched_no_change.
	rec2 := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, fx.TargetTag, "", ""),
		"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec2, http.StatusOK)
	r2 := decodeReimportResult(t, rec2.Body.Bytes())
	if r2.Data.Updated != 0 || r2.Data.UntouchedNoChange != 1 {
		t.Fatalf("second run not idempotent: %+v", r2.Data)
	}
	if !r2.Data.Applied {
		// "Applied=true" with zero writes is still semantically correct (no
		// force_required + not dry_run = the apply branch runs, just with no
		// rows to write).
		t.Errorf("second run Applied want true, got false")
	}
}

// A re-import overwrites existing targets, so those rows' stored QA findings —
// and any ignore on them — describe text that is gone.
// Unlike the initial bilingual upload, the translations here already existed
// and can already carry findings. Locked rows are skipped by the diff, so
// theirs must survive.
//
// Anti-tautology RED proof: drop the DeleteQAResultsForTranslations call from
// applyReimport and the first assertion fails while the locked one still passes.
func TestIntegrationReimportXLIFF_DropsStaleQAFindings(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportQA", "FrJob", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", Status: db.TranslationStatusNeedsReview},
		{Key: "u2", SourceText: "Goodbye", Status: db.TranslationStatusNeedsReview, Locked: true},
	})

	seedQAFinding(t, q, fx, "u1", "u2")

	xliff := buildXLIFF(t, "en", "fr", []xliffSpec{
		{Key: "u1", Source: "Hello", Target: "Bonjour"},
		{Key: "u2", Source: "Goodbye", Target: "Au revoir"},
	})
	rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, fx.TargetTag, "", ""),
		"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
	testutil.AssertStatus(t, rec, http.StatusOK)

	if n := countQA(t, q, fx, "u1"); n != 0 {
		t.Errorf("re-imported row must lose its findings, got %d", n)
	}
	if n := countQA(t, q, fx, "u2"); n != 1 {
		t.Errorf("locked row is skipped by the re-import; its findings must survive, got %d", n)
	}
}

// A segment another user has open is skipped and listed, in the preview and
// on apply; the importer's own hold does not block them.
func TestIntegrationReimportXLIFF_HeldSkipped(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)

	fx := seedReimportFixture(t, pool, q, "ReimportHeld", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", ActiveText: "OldValue", Status: db.TranslationStatusNeedsReview},
		{Key: "u2", SourceText: "Goodbye", Status: db.TranslationStatusNeedsReview},
	})
	job := testutil.SeedJob(t, q, fx.FileID, "fr")
	holdRow(t, q, fx, job.ID, "u1", fx.MemberID)
	holdRow(t, q, fx, job.ID, "u2", fx.AdminID)
	xliff := buildXLIFF(t, "en", "fr", []xliffSpec{
		{Key: "u1", Source: "Hello", Target: "NewValue"},
		{Key: "u2", Source: "Goodbye", Target: "Au revoir"},
	})

	for _, dryRun := range []string{"true", ""} {
		rec := testutil.DoFileUploadWithRole(t, router, reimportPath(fx.FileID, fx.TargetTag, dryRun, ""),
			"reimport.xliff", xliff, fx.AdminID, fx.TenantID, "admin")
		testutil.AssertStatus(t, rec, http.StatusOK)
		var resp struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		res := decodeHeld(t, resp.Data)
		if res.SkippedHeld != 1 || len(res.SkippedHeldKeys) != 1 || res.SkippedHeldKeys[0] != "u1" || res.Updated != 1 {
			t.Errorf("dry_run=%q: held %d %v, updated %d; want 1 [u1], 1", dryRun, res.SkippedHeld, res.SkippedHeldKeys, res.Updated)
		}
	}
	if st := loadTranslation(t, pool, fx.TranslationByKey["u1"]); st.ActiveText != "OldValue" {
		t.Errorf("held row written by the re-import: %q", st.ActiveText)
	}
	if st := loadTranslation(t, pool, fx.TranslationByKey["u2"]); st.ActiveText != "Au revoir" {
		t.Errorf("row the importer holds not updated: %q", st.ActiveText)
	}
}

// A hold taken after the diff read the rows still stops the write: the row is
// reported as held, not updated, and keeps its history and QA findings.
//
// Anti-tautology: dropping the stale findings of every planned row instead of
// only the rows written fails the u1 QA assertion.
func TestIntegrationReimportXLIFF_HoldTakenDuringImportSkipsTheRow(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	fx := seedReimportFixture(t, pool, q, "ReimportHeldRace", "P", []string{"fr"}, "fr", []reimportFixtureRow{
		{Key: "u1", SourceText: "Hello", ActiveText: "OldValue", Status: db.TranslationStatusNeedsReview},
		{Key: "u2", SourceText: "Goodbye", Status: db.TranslationStatusNeedsReview},
	})
	job := testutil.SeedJob(t, q, fx.FileID, "fr")
	seedQAFinding(t, q, fx, "u1", "u2")
	res, err := file.ReimportXLIFF(ctx, pool, file.ReimportXLIFFRequest{
		FileID: fx.FileID, TargetLanguage: "fr", EditedBy: fx.AdminID,
		ProjectID: fx.ProjectID, TenantID: fx.TenantID, FileName: "test.xliff", ActorEmail: "admin@test.com",
		IncomingSegments: []parser.Segment{
			{Key: "u1", Text: "Hello", Target: "NewValue"},
			{Key: "u2", Text: "Goodbye", Target: "Au revoir"},
		},
		// Commits on its own connection, after the diff and before the writes.
		BeforeWrite: func(context.Context, pgx.Tx) error {
			holdRow(t, q, fx, job.ID, "u1", fx.MemberID)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeHeld(t, raw); got.SkippedHeld != 1 || len(got.SkippedHeldKeys) != 1 || got.SkippedHeldKeys[0] != "u1" || got.Updated != 1 || !res.Applied {
		t.Errorf("result = %s, want u1 held, 1 updated, applied", raw)
	}
	if st := loadTranslation(t, pool, fx.TranslationByKey["u1"]); st.ActiveText != "OldValue" {
		t.Errorf("held row written: %q", st.ActiveText)
	}
	if n := testutil.CountRows(t, pool, "SELECT count(*) FROM translation_history WHERE translation_id = $1", fx.TranslationByKey["u1"]); n != 0 {
		t.Errorf("held row got %d history rows, want 0", n)
	}
	if n := countQA(t, q, fx, "u1"); n != 1 {
		t.Errorf("held row keeps its text, so its findings must survive, got %d", n)
	}
	if n := countQA(t, q, fx, "u2"); n != 0 {
		t.Errorf("written row must lose its findings, got %d", n)
	}
}

func seedQAFinding(t *testing.T, q db.Querier, fx reimportFixture, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if err := q.UpsertQAResult(context.Background(), db.UpsertQAResultParams{
			TranslationID: fx.TranslationByKey[key],
			CheckType:     "punctuation",
			Severity:      "error",
			Message:       "stale finding",
		}); err != nil {
			t.Fatalf("seed qa_result(%s): %v", key, err)
		}
	}
}

func countQA(t *testing.T, q db.Querier, fx reimportFixture, key string) int {
	t.Helper()
	rows, err := q.ListQAResultsForTranslation(context.Background(), fx.TranslationByKey[key])
	if err != nil {
		t.Fatalf("list qa_results(%s): %v", key, err)
	}
	return len(rows)
}

func holdRow(t *testing.T, q db.Querier, fx reimportFixture, jobID int64, key string, userID int64) {
	t.Helper()
	testutil.SeedHold(t, q, fx.TenantID, jobID, fx.TranslationByKey[key], userID)
}

// heldResult is the part of a re-import report the hold tests read.
type heldResult struct {
	Updated         int      `json:"updated"`
	SkippedHeld     int      `json:"skipped_held"`
	SkippedHeldKeys []string `json:"skipped_held_keys"`
}

func decodeHeld(t *testing.T, raw []byte) heldResult {
	t.Helper()
	var out heldResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	return out
}
