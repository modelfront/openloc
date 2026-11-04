package filterprofile_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/filterprofile"
	"openloc/internal/httputil"
	"openloc/internal/parser"
	"openloc/internal/testutil"
)

type profileResp struct {
	Data filterprofile.Profile `json:"data"`
}

// Every format has a Default, every string format a Plain text strings
// built-in, and the stored options are the normalized form the API returns,
// so a copy of a built-in starts from exactly what the list shows.
func TestIntegrationFilterBuiltins(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	tenant := testutil.SeedTenant(t, q, "FilterBuiltins")
	svc := filterprofile.NewService(q, pool)

	for _, format := range filterprofile.Formats {
		if _, err := q.GetDefaultFilterProfile(testutil.Ctx(), format); err != nil {
			t.Errorf("%s has no Default built-in: %v", format, err)
		}
	}
	rows, err := q.ListFilterProfiles(testutil.Ctx(), db.ListFilterProfilesParams{TenantID: &tenant.ID})
	if err != nil {
		t.Fatal(err)
	}
	plain := map[string]bool{}
	for _, row := range rows {
		p, err := svc.Get(testutil.Ctx(), tenant.ID, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		var stored filterprofile.Options
		if err := json.Unmarshal(row.Options, &stored); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(stored.InlineHTML, p.Options.InlineHTML) {
			t.Errorf("%s: stored inline_html %v, served %v", *row.BuiltinKey, stored.InlineHTML, p.Options.InlineHTML)
		}
		if p.Options.InlineHTML != nil && !*p.Options.InlineHTML {
			plain[p.Format] = true
		}
	}
	for _, format := range []string{"json_i18n", "po", "txt", "xlsx"} {
		if !plain[format] {
			t.Errorf("%s has no Plain text strings built-in", format)
		}
	}
}

func TestIntegrationFilterHTTP_OtherTenantGets404Everywhere(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	a := testutil.SeedTenant(t, q, "FilterHTTPA")
	b := testutil.SeedTenant(t, q, "FilterHTTPB")
	ownerA := testutil.SeedUser(t, q, a.ID, "a@filter-http.test", "pass")
	ownerB := testutil.SeedUser(t, q, b.ID, "b@filter-http.test", "pass")

	rec := testutil.DoRequest(t, router, "POST", "/api/filter-profiles",
		map[string]any{"name": "A's", "format": "xlsx", "options": map[string]any{}}, ownerA.ID, a.ID)
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created profileResp
	testutil.DecodeJSON(t, rec, &created)
	path := fmt.Sprintf("/api/filter-profiles/%d", created.Data.ID)

	for _, c := range []struct {
		method string
		body   any
	}{
		{"GET", nil},
		{"PUT", map[string]any{"name": "x", "options": map[string]any{}, "updated_at": created.Data.UpdatedAt}},
		{"DELETE", nil},
	} {
		rec := testutil.DoRequest(t, router, c.method, path, c.body, ownerB.ID, b.ID)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s as another org: %d %s, want 404", c.method, path, rec.Code, rec.Body.String())
		}
	}
	// Copying another org's profile is refused, and B's list does not show it.
	rec = testutil.DoRequest(t, router, "POST", "/api/filter-profiles",
		map[string]any{"name": "Copy", "from": created.Data.ID}, ownerB.ID, b.ID)
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	rec = testutil.DoRequest(t, router, "GET", "/api/filter-profiles", nil, ownerB.ID, b.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var list struct {
		Data []filterprofile.Profile `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &list)
	for _, p := range list.Data {
		if p.ID == created.Data.ID || !p.Builtin {
			t.Errorf("B's list shows %q (builtin=%v)", p.Name, p.Builtin)
		}
	}
}

func TestIntegrationFilterHTTP_WritesAndErrors(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "FilterWrites")
	owner := testutil.SeedUser(t, q, tenant.ID, "o@filter-writes.test", "pass")
	do := func(method, path string, body any) *profileResp {
		t.Helper()
		rec := testutil.DoRequest(t, router, method, path, body, owner.ID, tenant.ID)
		if rec.Code >= 300 {
			t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body.String())
		}
		var out profileResp
		if rec.Code != http.StatusNoContent {
			testutil.DecodeJSON(t, rec, &out)
		}
		return &out
	}
	status := func(method, path string, body any) int {
		t.Helper()
		return testutil.DoRequest(t, router, method, path, body, owner.ID, tenant.ID).Code
	}

	t.Run("create_normalizes", func(t *testing.T) {
		got := do("POST", "/api/filter-profiles", map[string]any{
			"name": "  Product sheet  ", "format": "xlsx",
			"options": map[string]any{"inline_html": false, "xlsx": map[string]any{"columns": []string{" b", "B"}, "sheets": []string{"Main", "Main"}, "header_row": true, "target_columns": []map[string]string{{"language": "FR", "column": "c"}}}},
		}).Data
		if got.Name != "Product sheet" || got.Builtin || got.Format != "xlsx" {
			t.Errorf("created %+v", got)
		}
		x := got.Options.XLSX
		if x == nil || !reflect.DeepEqual(x.Columns, []string{"B"}) || !reflect.DeepEqual(x.Sheets, []string{"Main"}) || !x.HeaderRow ||
			!reflect.DeepEqual(x.TargetColumns, []parser.XLSXTargetColumn{{Language: "fr", Column: "C"}}) {
			t.Errorf("xlsx rules = %+v, want columns [B], sheets [Main], header row, fr in C", x)
		}
		if got.Options.InlineHTML == nil || *got.Options.InlineHTML {
			t.Errorf("inline_html = %v, want false", got.Options.InlineHTML)
		}
		// An HTML profile carries no string or Excel options.
		html := do("POST", "/api/filter-profiles", map[string]any{
			"name": "Page", "format": "html", "options": map[string]any{"inline_html": false, "xlsx": map[string]any{"columns": []string{"B"}}},
		}).Data
		if html.Options.InlineHTML != nil || html.Options.XLSX != nil {
			t.Errorf("html options = %+v, want none of the other formats'", html.Options)
		}
	})

	t.Run("refuses_bad_input", func(t *testing.T) {
		for name, body := range map[string]map[string]any{
			"no name":            {"name": " ", "format": "xlsx", "options": map[string]any{}},
			"no options":         {"name": "x", "format": "xlsx"},
			"bad format":         {"name": "x", "format": "docx", "options": map[string]any{}},
			"bad column":         {"name": "x", "format": "xlsx", "options": map[string]any{"xlsx": map[string]any{"columns": []string{"B2"}}}},
			"target over source": {"name": "x", "format": "xlsx", "options": map[string]any{"xlsx": map[string]any{"columns": []string{"B"}, "target_columns": []map[string]string{{"language": "fr", "column": "b"}}}}},
			"long sheet":         {"name": "x", "format": "xlsx", "options": map[string]any{"xlsx": map[string]any{"sheets": []string{"0123456789012345678901234567890123"}}}},
			"bad list key":       {"name": "x", "format": "", "options": map[string]any{}},
		} {
			if got := status("POST", "/api/filter-profiles", body); got != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400", name, got)
			}
		}
		if got := status("GET", "/api/filter-profiles?format=docx", nil); got != http.StatusBadRequest {
			t.Errorf("list ?format=docx: status = %d, want 400", got)
		}
	})

	t.Run("names_are_unique_per_format", func(t *testing.T) {
		do("POST", "/api/filter-profiles", map[string]any{"name": "Shared", "format": "po", "options": map[string]any{}})
		if got := status("POST", "/api/filter-profiles", map[string]any{"name": "shared", "format": "po", "options": map[string]any{}}); got != http.StatusConflict {
			t.Errorf("same name and format: status = %d, want 409", got)
		}
		do("POST", "/api/filter-profiles", map[string]any{"name": "Shared", "format": "txt", "options": map[string]any{}})
	})

	t.Run("update_is_optimistic_and_keeps_format", func(t *testing.T) {
		p := do("POST", "/api/filter-profiles", map[string]any{"name": "Edit me", "format": "json_i18n", "options": map[string]any{}}).Data
		path := fmt.Sprintf("/api/filter-profiles/%d", p.ID)
		if got := status("PUT", path, map[string]any{"name": "x", "format": "po", "options": map[string]any{}, "updated_at": p.UpdatedAt}); got != http.StatusBadRequest {
			t.Errorf("format change: status = %d, want 400", got)
		}
		if got := status("PUT", path, map[string]any{"name": "x", "options": map[string]any{}}); got != http.StatusBadRequest {
			t.Errorf("no updated_at: status = %d, want 400", got)
		}
		updated := do("PUT", path, map[string]any{"name": "Edited", "options": map[string]any{"inline_html": false}, "updated_at": p.UpdatedAt}).Data
		if updated.Name != "Edited" || *updated.Options.InlineHTML {
			t.Errorf("updated = %+v", updated)
		}
		if got := status("PUT", path, map[string]any{"name": "Stale", "options": map[string]any{}, "updated_at": p.UpdatedAt}); got != http.StatusConflict {
			t.Errorf("stale save: status = %d, want 409", got)
		}
	})

	t.Run("builtins_are_read_only_and_copyable", func(t *testing.T) {
		def, err := q.GetDefaultFilterProfile(testutil.Ctx(), "xlsx")
		if err != nil {
			t.Fatal(err)
		}
		path := fmt.Sprintf("/api/filter-profiles/%d", def.ID)
		if got := status("PUT", path, map[string]any{"name": "Mine", "options": map[string]any{}, "updated_at": def.UpdatedAt.Time}); got != http.StatusConflict {
			t.Errorf("PUT a built-in: status = %d, want 409", got)
		}
		if got := status("DELETE", path, nil); got != http.StatusConflict {
			t.Errorf("DELETE a built-in: status = %d, want 409", got)
		}
		cp := do("POST", "/api/filter-profiles", map[string]any{"name": "My Excel", "from": def.ID}).Data
		if cp.Format != "xlsx" || cp.BasedOn == nil || *cp.BasedOn != def.ID {
			t.Errorf("copy = %+v, want an xlsx profile based on %d", cp, def.ID)
		}
		if got := status("POST", "/api/filter-profiles", map[string]any{"name": "Wrong", "from": def.ID, "format": "po"}); got != http.StatusBadRequest {
			t.Errorf("copy into another format: status = %d, want 400", got)
		}
	})
}

// A delete is refused while a template uses the profile; the projects that
// use it go back to the format's Default; the row is soft-deleted.
func TestIntegrationFilterDeleteWhileInUse(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := testutil.Ctx()
	tenant := testutil.SeedTenant(t, q, "FilterDelete")
	svc := filterprofile.NewService(q, pool)
	p, err := svc.Create(ctx, tenant.ID, 0, filterprofile.Input{Name: "Used", Format: "xlsx", Options: &filterprofile.Options{}})
	if err != nil {
		t.Fatal(err)
	}
	project := testutil.SeedProject(t, q, tenant.ID, "Uses it", "en", []string{"fr"})
	tmpl := testutil.SeedTemplate(t, q, tenant.ID, "Uses it too")
	for _, sp := range []int64{project.SettingsProfileID, tmpl.SettingsProfileID} {
		if err := filterprofile.SetChoices(ctx, q, tenant.ID, sp, map[string]*int64{"xlsx": &p.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if projects, templates, err := svc.Uses(ctx, tenant.ID, p.ID); err != nil || projects != 1 || templates != 1 {
		t.Fatalf("uses = %d projects, %d templates, %v; want 1, 1", projects, templates, err)
	}

	if err := svc.Delete(ctx, tenant.ID, p.ID); err == nil {
		t.Fatal("delete while a template uses it succeeded")
	}
	if err := filterprofile.SetChoices(ctx, q, tenant.ID, tmpl.SettingsProfileID, map[string]*int64{"xlsx": nil}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, tenant.ID, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if choices, err := filterprofile.Choices(ctx, q, project.SettingsProfileID); err != nil || len(choices) != 0 {
		t.Errorf("project still names %v (%v), want the Default", choices, err)
	}
	if n := testutil.CountRows(t, pool, `SELECT count(*) FROM filter_profiles WHERE id = $1 AND deleted_at IS NOT NULL`, p.ID); n != 1 {
		t.Errorf("soft-deleted rows = %d, want 1", n)
	}
	// A deleted profile can no longer be picked.
	if err := filterprofile.SetChoices(ctx, q, tenant.ID, project.SettingsProfileID, map[string]*int64{"xlsx": &p.ID}); err == nil {
		t.Error("picking a deleted profile succeeded")
	}
}

// HTML and XML profiles store their parser options: the defaults when none
// are given, names cleaned up, and attributes that could run script or load a
// resource refused.
func TestIntegrationFilterHTMLAndXMLOptions(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := testutil.Ctx()
	tenant := testutil.SeedTenant(t, q, "FilterMarkup")
	svc := filterprofile.NewService(q, pool)

	html, err := svc.Create(ctx, tenant.ID, 0, filterprofile.Input{Name: "Defaults", Format: "html", Options: &filterprofile.Options{}})
	if err != nil {
		t.Fatal(err)
	}
	if html.Options.HTML == nil || !html.Options.HTML.SkipPre || !html.Options.HTML.MetaDescription || len(html.Options.HTML.Attributes["img"]) != 2 {
		t.Errorf("an HTML profile with no options = %+v, want the parser defaults", html.Options.HTML)
	}
	custom, err := svc.Create(ctx, tenant.ID, 0, filterprofile.Input{Name: "Buttons", Format: "html", Options: &filterprofile.Options{
		HTML: &parser.HTMLOptions{Attributes: map[string][]string{" Button ": {"ARIA-label", "aria-label"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := custom.Options.HTML.Attributes; !reflect.DeepEqual(got, map[string][]string{"button": {"aria-label"}}) {
		t.Errorf("attributes = %v, want button: [aria-label]", got)
	}
	for name, opts := range map[string]filterprofile.Options{
		"onclick":       {HTML: &parser.HTMLOptions{Attributes: map[string][]string{"button": {"onclick"}}}},
		"href":          {HTML: &parser.HTMLOptions{Attributes: map[string][]string{"a": {"href"}}}},
		"bad element":   {HTML: &parser.HTMLOptions{Attributes: map[string][]string{"<script>": {"title"}}}},
		"bad xml name":  {XML: &parser.XMLOptions{SkipElements: []string{"1code"}}},
		"empty no-name": {XML: &parser.XMLOptions{NoTranslate: []parser.XMLAttrValue{{Name: "bad name", Value: "x"}}}},
		"empty marker":  {XML: &parser.XMLOptions{NoTranslate: []parser.XMLAttrValue{{Name: "translate", Value: ""}}}},
		"meta content":  {HTML: &parser.HTMLOptions{Attributes: map[string][]string{"meta": {"content"}}}},
		"too many":      {XML: &parser.XMLOptions{SkipElements: manyNames(5000)}},
		// Refused by length before normalizing: the copies would collapse to one.
		"too many copies": {XML: &parser.XMLOptions{SkipElements: copies("note", 5000)}},
	} {
		format := "html"
		if opts.XML != nil {
			format = "xml"
		}
		if _, err := svc.Create(ctx, tenant.ID, 0, filterprofile.Input{Name: name, Format: format, Options: &opts}); err == nil {
			t.Errorf("%s: create succeeded, want refused", name)
		} else if _, ok := httputil.AsClientError(err); !ok {
			t.Errorf("%s: %v, want a client error", name, err)
		}
	}

	xml, err := svc.Create(ctx, tenant.ID, 0, filterprofile.Input{Name: "Skip code", Format: "xml", Options: &filterprofile.Options{
		XML: &parser.XMLOptions{InlineElements: []string{"term", "term"}, SkipElements: []string{"code"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if x := xml.Options.XML; !reflect.DeepEqual(x.InlineElements, []string{"term"}) || !reflect.DeepEqual(x.SkipElements, []string{"code"}) || len(x.NoTranslate) != 0 {
		t.Errorf("xml options = %+v", x)
	}
	def, err := q.GetDefaultFilterProfile(ctx, "xml")
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, tenant.ID, def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*got.Options.XML, parser.DefaultXMLOptions()) {
		t.Errorf("the XML Default built-in = %+v, want the parser defaults", got.Options.XML)
	}
}

// manyNames is n distinct element names.
func manyNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("e%d", i)
	}
	return out
}

// copies is n copies of name.
func copies(name string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = name
	}
	return out
}
