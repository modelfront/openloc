package file_test

// "Strings contain HTML": an upload is parsed in the mode of the project's
// setting and the file keeps that mode. Each test runs the same file with the
// setting on and off, so the "on" half proves the "off" half is not passing
// just because the parser never makes chips.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

const taggedJSON = `{"bold": "Use <b>bold</b> here"}`

// setInlineHTML stores the project's "Strings contain HTML" setting.
func setInlineHTML(t *testing.T, q db.Querier, profileID int64, on bool) {
	t.Helper()
	ctx := context.Background()
	p, err := q.GetSettingsProfile(ctx, profileID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.UpdateSettingsProfile(ctx, db.UpdateSettingsProfileParams{
		ID: p.ID, FuzzyThreshold: p.FuzzyThreshold, MtOnUpload: p.MtOnUpload, TmOnUpload: p.TmOnUpload,
		QaProfileID: p.QaProfileID, InlineHtml: &on,
	}); err != nil {
		t.Fatal(err)
	}
}

type storedSegment struct {
	text string
	rich *string
}

func fileMode(t *testing.T, q db.Querier, fileID int64) (bool, []storedSegment) {
	t.Helper()
	ctx := context.Background()
	f, err := q.GetFile(ctx, fileID)
	if err != nil {
		t.Fatal(err)
	}
	segs, err := q.ListSegmentsByFile(ctx, fileID)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]storedSegment, len(segs))
	for i, s := range segs {
		out[i] = storedSegment{text: s.SourceText, rich: s.SourceRichText}
	}
	return f.InlineHtml, out
}

func uploadID(t *testing.T, rec *httptest.ResponseRecorder) int64 {
	t.Helper()
	var resp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &resp)
	return resp.Data.ID
}

func TestIntegrationUploadFollowsInlineHTMLSetting(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "InlineHTMLUpload")
	user := testutil.SeedUser(t, q, tenant.ID, "u@inline-html.test", "pass")

	for _, on := range []bool{true, false} {
		t.Run(fmt.Sprintf("inline_html=%v", on), func(t *testing.T) {
			project := testutil.SeedProject(t, q, tenant.ID, fmt.Sprintf("P %v", on), "en", []string{"fr"})
			if !on {
				setInlineHTML(t, q, project.SettingsProfileID, false)
			}
			rec := testutil.DoFileUpload(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
				"strings.json", "application/json", taggedJSON, user.ID, tenant.ID)
			testutil.AssertStatus(t, rec, http.StatusCreated)
			stored, segs := fileMode(t, q, uploadID(t, rec))

			if stored != on {
				t.Errorf("files.inline_html = %v, want %v", stored, on)
			}
			if len(segs) != 1 {
				t.Fatalf("segments = %d, want 1", len(segs))
			}
			if on {
				if segs[0].rich == nil {
					t.Errorf("HTML strings: source_rich_text is NULL, want chips for <b>")
				}
				return
			}
			if segs[0].rich != nil {
				t.Errorf("plain text: source_rich_text = %q, want NULL", *segs[0].rich)
			}
			if segs[0].text != "Use <b>bold</b> here" {
				t.Errorf("plain text: source_text = %q, want the literal tags", segs[0].text)
			}
		})
	}
}

// The setting applies to JSON, PO, TXT and XLSX only: a file of another
// format is stored with its strings read as HTML, so the editor keeps its
// chips whatever the project says.
func TestIntegrationUploadOtherFormatIgnoresInlineHTMLSetting(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "InlineHTMLOther")
	user := testutil.SeedUser(t, q, tenant.ID, "u@inline-html-other.test", "pass")
	project := testutil.SeedProject(t, q, tenant.ID, "P html", "en", []string{"fr"})
	setInlineHTML(t, q, project.SettingsProfileID, false)

	rec := testutil.DoFileUpload(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
		"page.html", "text/html", `<p>Use <b>bold</b> here</p>`, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	stored, segs := fileMode(t, q, uploadID(t, rec))
	if !stored {
		t.Error("files.inline_html = false for an HTML file, want true")
	}
	if len(segs) != 1 || segs[0].rich == nil {
		t.Errorf("HTML file lost its chips: %+v", segs)
	}
}

// A draft's files are parsed again at publish; they keep the mode they were
// uploaded with even when the setting changed in between.
func TestIntegrationDraftPublishKeepsFileInlineHTML(t *testing.T) {
	f := newDraftUploadFixture(t, "InlineHTMLPublish")
	project, err := f.q.GetProject(context.Background(), db.GetProjectParams{ID: f.draftID, TenantID: f.tenant.ID})
	if err != nil {
		t.Fatal(err)
	}
	setInlineHTML(t, f.q, project.SettingsProfileID, false)
	fileID := f.upload(t, "strings.json", taggedJSON)
	setInlineHTML(t, f.q, project.SettingsProfileID, true)

	rec := testutil.DoRequestWithRole(t, f.router, "POST", fmt.Sprintf("/api/projects/%d/publish", f.draftID),
		map[string]any{"analyze": false}, f.pm.ID, f.tenant.ID, "project_manager")
	testutil.AssertStatus(t, rec, http.StatusOK)

	stored, segs := fileMode(t, f.q, fileID)
	if stored {
		t.Errorf("files.inline_html = true after publish, want false (the upload's mode)")
	}
	if len(segs) != 1 || segs[0].rich != nil || segs[0].text != "Use <b>bold</b> here" {
		t.Errorf("segments after publish = %+v, want one plain segment with the literal tags", segs)
	}
}

// The target columns of a plain-text workbook are imported as text.
func TestIntegrationUploadXLSXTargetsFollowInlineHTML(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "InlineHTMLXLSX")
	user := testutil.SeedUser(t, q, tenant.ID, "u@inline-xlsx.test", "pass")
	xlsx := buildXLSXBilingual(t, []string{"en", "fr"}, [][]string{{"Use <b>bold</b>", "Utilisez <b>gras</b>"}})

	for _, on := range []bool{true, false} {
		t.Run(fmt.Sprintf("inline_html=%v", on), func(t *testing.T) {
			project := testutil.SeedProject(t, q, tenant.ID, fmt.Sprintf("X %v", on), "en", []string{"fr"})
			if !on {
				setInlineHTML(t, q, project.SettingsProfileID, false)
			}
			rec := testutil.DoMultipartUpload(t, router, fmt.Sprintf("/api/projects/%d/files", project.ID),
				"strings.xlsx", xlsx, nil, user.ID, tenant.ID, "")
			testutil.AssertStatus(t, rec, http.StatusCreated)
			fileID := uploadID(t, rec)

			var text string
			var rich *string
			if err := pool.QueryRow(ctx, `
				SELECT t.active_text, t.active_rich_text FROM translations t
				JOIN segments s ON s.id = t.segment_id WHERE s.file_id = $1`, fileID).Scan(&text, &rich); err != nil {
				t.Fatal(err)
			}
			if on {
				if rich == nil {
					t.Errorf("HTML strings: active_rich_text is NULL, want chips for <b>")
				}
				return
			}
			if rich != nil || text != "Utilisez <b>gras</b>" {
				t.Errorf("plain text: active_text = %q, active_rich_text = %v; want the literal tags and NULL", text, rich)
			}
		})
	}
}
