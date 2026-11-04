package file_test

// An upload is parsed with a filter profile: the uploader's pick for the
// file, else the project's for its format, else the format's Default. The
// file keeps a snapshot of it, and an Excel profile's rules give a workbook
// its column layout without asking.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/filterprofile"
	"openloc/internal/parser"
	"openloc/internal/testutil"
)

func TestIntegrationUploadFilterProfile(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	ctx := testutil.Ctx()
	tenant := testutil.SeedTenant(t, q, "FilterUpload")
	other := testutil.SeedTenant(t, q, "FilterUploadOther")
	user := testutil.SeedUser(t, q, tenant.ID, "u@filter-upload.test", "pass")
	svc := filterprofile.NewService(q, pool)
	off := false

	plainJSON, err := svc.Create(ctx, tenant.ID, 0, filterprofile.Input{Name: "Plain JSON", Format: "json_i18n", Options: &filterprofile.Options{InlineHTML: &off}})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := svc.Create(ctx, other.ID, 0, filterprofile.Input{Name: "Theirs", Format: "json_i18n", Options: &filterprofile.Options{InlineHTML: &off}})
	if err != nil {
		t.Fatal(err)
	}
	columnB, err := svc.Create(ctx, tenant.ID, 0, filterprofile.Input{Name: "Column B", Format: "xlsx", Options: &filterprofile.Options{
		XLSX: &filterprofile.XLSXRules{Sheets: []string{"Products"}, HeaderRow: true, Columns: []string{"B"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	upload := func(t *testing.T, projectID int64, name string, content []byte, fields map[string]string) (int, int64) {
		t.Helper()
		rec := testutil.DoMultipartUpload(t, router, fmt.Sprintf("/api/projects/%d/files", projectID), name, content, fields, user.ID, tenant.ID, "")
		if rec.Code != http.StatusCreated {
			return rec.Code, 0
		}
		return rec.Code, uploadID(t, rec)
	}
	snapshot := func(t *testing.T, fileID int64) filterprofile.Snapshot {
		t.Helper()
		var raw []byte
		if err := pool.QueryRow(ctx, `SELECT filter_profile FROM files WHERE id = $1`, fileID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var s filterprofile.Snapshot
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("snapshot %s: %v", raw, err)
		}
		return s
	}

	t.Run("default_when_none_picked", func(t *testing.T) {
		project := testutil.SeedProject(t, q, tenant.ID, "Default", "en", []string{"fr"})
		code, id := upload(t, project.ID, "strings.json", []byte(taggedJSON), nil)
		if code != http.StatusCreated {
			t.Fatalf("upload = %d", code)
		}
		s := snapshot(t, id)
		if !s.Builtin || s.Name != "Default" || s.Format != "json_i18n" {
			t.Errorf("snapshot = %+v, want the JSON Default", s)
		}
		if mode, _ := fileMode(t, q, id); !mode {
			t.Error("files.inline_html = false with the Default, want true")
		}
	})

	t.Run("project_pick_then_upload_override", func(t *testing.T) {
		project := testutil.SeedProject(t, q, tenant.ID, "Picked", "en", []string{"fr"})
		if err := filterprofile.SetChoices(ctx, q, tenant.ID, project.SettingsProfileID, map[string]*int64{"json_i18n": &plainJSON.ID}); err != nil {
			t.Fatal(err)
		}
		_, id := upload(t, project.ID, "strings.json", []byte(taggedJSON), nil)
		if s := snapshot(t, id); s.ID != plainJSON.ID {
			t.Errorf("snapshot id = %d, want the project's %d", s.ID, plainJSON.ID)
		}
		if mode, segs := fileMode(t, q, id); mode || segs[0].rich != nil {
			t.Errorf("project's plain-text profile: inline_html = %v, rich = %v; want plain", mode, segs[0].rich)
		}

		def, err := q.GetDefaultFilterProfile(ctx, "json_i18n")
		if err != nil {
			t.Fatal(err)
		}
		_, id = upload(t, project.ID, "strings.json", []byte(taggedJSON), map[string]string{"filter_profile_id": strconv.FormatInt(def.ID, 10)})
		if s := snapshot(t, id); s.ID != def.ID {
			t.Errorf("override snapshot id = %d, want %d", s.ID, def.ID)
		}
		if mode, segs := fileMode(t, q, id); !mode || segs[0].rich == nil {
			t.Errorf("override to the Default: inline_html = %v, rich = %v; want chips", mode, segs[0].rich)
		}
	})

	t.Run("refuses_bad_overrides", func(t *testing.T) {
		project := testutil.SeedProject(t, q, tenant.ID, "Refused", "en", []string{"fr"})
		for name, c := range map[string]struct {
			file string
			id   string
		}{
			"other tenant":   {"strings.json", strconv.FormatInt(theirs.ID, 10)},
			"wrong format":   {"strings.json", strconv.FormatInt(columnB.ID, 10)},
			"no profiles":    {"notes.md", strconv.FormatInt(plainJSON.ID, 10)},
			"not an id":      {"strings.json", "abc"},
			"not a positive": {"strings.json", "0"},
		} {
			content := []byte(taggedJSON)
			if c.file == "notes.md" {
				content = []byte("# Notes\n\nSome text.\n")
			}
			if code, _ := upload(t, project.ID, c.file, content, map[string]string{"filter_profile_id": c.id}); code != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400", name, code)
			}
		}
		if n := testutil.CountRows(t, pool, `SELECT count(*) FROM files WHERE project_id = $1`, project.ID); n != 0 {
			t.Errorf("refused uploads stored %d files", n)
		}
	})

	// A .xml with a <resources> root is Android string resources, which have
	// no filter profiles: the uploader saw an XML picker, so an XML pick is
	// ignored rather than refused. Another organization's pick still is not.
	t.Run("android_xml_ignores_an_xml_pick", func(t *testing.T) {
		project := testutil.SeedProject(t, q, tenant.ID, "Android", "en", []string{"fr"})
		docsXML, err := svc.Create(ctx, tenant.ID, 0, filterprofile.Input{Name: "Docs", Format: "xml", Options: &filterprofile.Options{}})
		if err != nil {
			t.Fatal(err)
		}
		android := []byte(`<?xml version="1.0" encoding="utf-8"?><resources><string name="hello">Hello</string></resources>`)
		code, id := upload(t, project.ID, "strings.xml", android, map[string]string{"filter_profile_id": strconv.FormatInt(docsXML.ID, 10)})
		if code != http.StatusCreated {
			t.Fatalf("Android strings.xml with an XML pick: status = %d, want 201", code)
		}
		var raw []byte
		if err := pool.QueryRow(ctx, `SELECT filter_profile FROM files WHERE id = $1`, id).Scan(&raw); err != nil || raw != nil {
			t.Errorf("Android file snapshot = %s (%v), want none", raw, err)
		}
		if code, _ := upload(t, project.ID, "strings.xml", android, map[string]string{"filter_profile_id": strconv.FormatInt(plainJSON.ID, 10)}); code != http.StatusBadRequest {
			t.Errorf("Android strings.xml with a JSON pick: status = %d, want 400", code)
		}
	})

	t.Run("excel_rules_give_the_layout", func(t *testing.T) {
		project := testutil.SeedProject(t, q, tenant.ID, "Excel", "en", []string{"fr"})
		_, id := upload(t, project.ID, "catalog.xlsx", layoutWorkbookBytes(t), map[string]string{"filter_profile_id": strconv.FormatInt(columnB.ID, 10)})
		var layout []byte
		if err := pool.QueryRow(ctx, `SELECT xlsx_layout FROM files WHERE id = $1`, id).Scan(&layout); err != nil {
			t.Fatal(err)
		}
		if want := `{"sheets": [{"name": "Products", "columns": ["B"], "header_row": true}]}`; string(layout) != want {
			t.Errorf("xlsx_layout = %s, want %s", layout, want)
		}
		if got := segmentTexts(t, pool, id); got != "Blue chair|Red table" {
			t.Errorf("segments = %q, want the English column below the header", got)
		}
		// The uploader's own layout wins over the rules.
		_, id = upload(t, project.ID, "catalog.xlsx", layoutWorkbookBytes(t), map[string]string{
			"filter_profile_id": strconv.FormatInt(columnB.ID, 10), "xlsx_layout": `{"sheets":[{"name":"Notes"}]}`,
		})
		if got := segmentTexts(t, pool, id); got != "Handle with care|Keep dry" {
			t.Errorf("with an uploaded layout, segments = %q, want the Notes sheet", got)
		}
	})

	// An HTML profile's options decide the segments, and the file keeps them.
	t.Run("html_options_reach_the_parser", func(t *testing.T) {
		buttons, err := svc.Create(ctx, tenant.ID, 0, filterprofile.Input{Name: "Buttons", Format: "html", Options: &filterprofile.Options{
			HTML: &parser.HTMLOptions{Attributes: map[string][]string{"button": {"aria-label"}}, SkipPre: true},
		}})
		if err != nil {
			t.Fatal(err)
		}
		project := testutil.SeedProject(t, q, tenant.ID, "HTML", "en", []string{"fr"})
		page := []byte(`<html><head><meta name="description" content="About us"></head><body><button aria-label="Close the dialog">X</button></body></html>`)
		_, def := upload(t, project.ID, "page.html", page, nil)
		_, custom := upload(t, project.ID, "page.html", page, map[string]string{"filter_profile_id": strconv.FormatInt(buttons.ID, 10)})
		if got := segmentTexts(t, pool, def); !strings.Contains(got, "About us") || strings.Contains(got, "Close the dialog") {
			t.Errorf("Default segments = %q, want the meta description and no aria-label", got)
		}
		if got := segmentTexts(t, pool, custom); strings.Contains(got, "About us") || !strings.Contains(got, "Close the dialog") {
			t.Errorf("profile segments = %q, want the aria-label and no meta description", got)
		}
		if s := snapshot(t, custom); s.Options.HTML == nil || s.Options.HTML.MetaDescription {
			t.Errorf("snapshot html options = %+v", s.Options.HTML)
		}
	})

	// The upload dialog's layout step starts from the profile's rules.
	t.Run("inspect_suggests_the_rules", func(t *testing.T) {
		project := testutil.SeedProject(t, q, tenant.ID, "Inspect", "en", []string{"fr"})
		inspect := func(fields map[string]string) string {
			t.Helper()
			rec := testutil.DoMultipartUpload(t, router, fmt.Sprintf("/api/projects/%d/files/inspect", project.ID), "catalog.xlsx",
				layoutWorkbookBytes(t), fields, user.ID, tenant.ID, "")
			testutil.AssertStatus(t, rec, http.StatusOK)
			var resp struct {
				Data struct {
					Suggested json.RawMessage `json:"suggested_layout"`
				} `json:"data"`
			}
			testutil.DecodeJSON(t, rec, &resp)
			return string(resp.Data.Suggested)
		}
		want := `{"sheets":[{"name":"Products","header_row":true,"columns":["B"]}]}`
		if got := inspect(map[string]string{"filter_profile_id": strconv.FormatInt(columnB.ID, 10)}); got != want {
			t.Errorf("with the profile picked, suggested = %s, want %s", got, want)
		}
		if got := inspect(nil); got == want {
			t.Error("with no pick, the Default suggested the profile's layout")
		}
		if err := filterprofile.SetChoices(ctx, q, tenant.ID, project.SettingsProfileID, map[string]*int64{"xlsx": &columnB.ID}); err != nil {
			t.Fatal(err)
		}
		if got := inspect(nil); got != want {
			t.Errorf("with the project's pick, suggested = %s, want %s", got, want)
		}
	})
}

// segmentTexts is a file's segment texts in order, joined by "|".
func segmentTexts(t *testing.T, pool *pgxpool.Pool, fileID int64) string {
	t.Helper()
	rows, err := pool.Query(testutil.Ctx(), `SELECT source_text FROM segments WHERE file_id = $1 ORDER BY position`, fileID)
	if err != nil {
		t.Fatal(err)
	}
	texts, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(texts, "|")
}

// The export reads an HTML file with the options it was imported with: the
// profile's custom attribute comes back translated, and the meta description
// it left out stays as written, whatever the project picks later.
func TestIntegrationFilterProfileExportKeepsHTMLOptions(t *testing.T) {
	f := newSnapshotFixture(t, "FilterExport")
	ctx := testutil.Ctx()
	project := testutil.SeedProject(t, f.q, f.tenant.ID, "Export", "en", []string{"fr"})
	sections, err := filterprofile.NewService(f.q, f.pool).Create(ctx, f.tenant.ID, 0, filterprofile.Input{Name: "Sections", Format: "html", Options: &filterprofile.Options{
		HTML: &parser.HTMLOptions{Attributes: map[string][]string{"section": {"aria-label"}}, SkipPre: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := filterprofile.SetChoices(ctx, f.q, f.tenant.ID, project.SettingsProfileID, map[string]*int64{"html": &sections.ID}); err != nil {
		t.Fatal(err)
	}
	page := `<html><head><meta name="description" content="About us"></head><body><section aria-label="Main area"><p>Hello</p></section></body></html>`
	fileID := f.uploadAs(t, project.ID, "page.html", "text/html", page)

	// The project goes back to the Default: the stored file keeps its options.
	if err := filterprofile.SetChoices(ctx, f.q, f.tenant.ID, project.SettingsProfileID, map[string]*int64{"html": nil}); err != nil {
		t.Fatal(err)
	}
	out := f.export(t, fileID)
	if !strings.Contains(out, `aria-label="FR:Main area"`) {
		t.Errorf("export lost the translated aria-label:\n%s", out)
	}
	if !strings.Contains(out, `content="About us"`) {
		t.Errorf("export changed the meta description the profile left out:\n%s", out)
	}
}
