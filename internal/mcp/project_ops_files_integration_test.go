package mcp_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/testutil"
)

// stageUpload stores a file the way POST /api/chat/uploads does and returns
// its upload id.
func stageUpload(t *testing.T, f *fixture, store *file.MemoryStore, tenantID, userID int64, name string, content []byte) string {
	t.Helper()
	ctx := context.Background()
	key := fmt.Sprintf("chat-uploads/%d/%d/%s-%s", tenantID, userID, uuid.NewString(), name)
	if err := store.Put(ctx, key, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	row, err := f.q.InsertChatUpload(ctx, db.InsertChatUploadParams{
		TenantID: tenantID, UserID: userID, Filename: name, SizeBytes: int64(len(content)), StorageKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return uuid.UUID(row.ID.Bytes).String()
}

type fileOut struct {
	ID           int64  `json:"id"`
	ProjectID    int64  `json:"project_id"`
	Name         string `json:"name"`
	Format       string `json:"format"`
	SegmentCount int32  `json:"segment_count"`
	HasSource    bool   `json:"has_source"`
}

func TestIntegrationMCPAttachFileAndFileTools(t *testing.T) {
	store := file.NewMemoryStore()
	f := newFixtureWithStore(t, store)
	ctx := context.Background()
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	upload := stageUpload(t, f, store, f.tenantA.ID, f.ownerA.ID, "brochure.txt", []byte("Hello world\nSecond line\n"))
	var attached struct {
		File     fileOut  `json:"file"`
		Draft    bool     `json:"draft"`
		Warnings []string `json:"warnings"`
	}
	if isErr, text := callTool(t, s, "openloc_attach_uploaded_file", map[string]any{
		"project_id": f.projectA.ID, "upload_id": upload,
	}, &attached); isErr {
		t.Fatalf("attach_uploaded_file: %s", text)
	}
	if attached.Draft || attached.File.Name != "brochure.txt" || attached.File.Format != "txt" || attached.File.SegmentCount != 2 || !attached.File.HasSource {
		t.Fatalf("attach result: %+v", attached)
	}
	jobs, err := f.q.ListJobsByProject(ctx, f.projectA.ID)
	if err != nil {
		t.Fatal(err)
	}
	var newJob bool
	for _, j := range jobs {
		if j.FileID == attached.File.ID && j.TargetLanguage == "de" {
			newJob = true
		}
	}
	if !newJob {
		t.Fatalf("no de job for the attached file: %+v", jobs)
	}

	// Somebody else's upload, a made-up id, a foreign project: all not-found.
	colleague := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "colleague@test.com", "pass", "owner")
	theirs := stageUpload(t, f, store, f.tenantA.ID, colleague.ID, "theirs.txt", []byte("Nope\n"))
	for _, args := range []map[string]any{
		{"project_id": f.projectA.ID, "upload_id": theirs},
		{"project_id": f.projectA.ID, "upload_id": uuid.NewString()},
		{"project_id": f.projectB.ID, "upload_id": upload},
	} {
		isErr, text := callTool(t, s, "openloc_attach_uploaded_file", args, nil)
		if !isErr {
			t.Errorf("attached with %v", args)
			continue
		}
		assertOpaqueNotFound(t, text)
	}
	bad := stageUpload(t, f, store, f.tenantA.ID, f.ownerA.ID, "picture.png", []byte{0x89, 'P', 'N', 'G'})
	if isErr, text := callTool(t, s, "openloc_attach_uploaded_file", map[string]any{"project_id": f.projectA.ID, "upload_id": bad}, nil); !isErr || !strings.Contains(text, "unsupported file format") {
		t.Errorf("an unsupported format was attached: %v %s", isErr, text)
	}
	if isErr, _ := callTool(t, connect(t, f.url, f.tokenA), "openloc_attach_uploaded_file", map[string]any{"project_id": f.projectA.ID, "upload_id": upload}, nil); !isErr {
		t.Error("a read-only token attached a file")
	}

	// Draft: stored and segmented now, jobs at publish.
	draft := seedDraftProject(t, f.q, f.tenantA.ID, "Draft with file")
	draftUpload := stageUpload(t, f, store, f.tenantA.ID, f.ownerA.ID, "draft.txt", []byte("Draft text\n"))
	if isErr, text := callTool(t, s, "openloc_attach_uploaded_file", map[string]any{"project_id": draft.ID, "upload_id": draftUpload}, &attached); isErr || !attached.Draft {
		t.Fatalf("attach to draft: %v %s %+v", isErr, text, attached)
	}
	if jobs, _ := f.q.ListJobsByProject(ctx, draft.ID); len(jobs) != 0 {
		t.Fatalf("a draft got jobs before publishing: %+v", jobs)
	}
	// The published project answers with its customer from the Customer label.
	cust := systemLabelValue(t, f, f.tenantA.ID, "customer", "Draft Customer")
	pickLabels(t, f, f.tenantA.ID, draft.ID, cust.ID)
	var published struct {
		Status       string `json:"status"`
		CustomerName string `json:"customer_name"`
		CustomerID   *int64 `json:"customer_id"`
	}
	if isErr, text := callTool(t, s, "openloc_publish_project", map[string]any{"project_id": draft.ID}, &published); isErr {
		t.Fatalf("publish_project: %s", text)
	}
	if published.CustomerName != "Draft Customer" || published.CustomerID == nil || *published.CustomerID != cust.ID {
		t.Errorf("published customer = %q / %v, want the label's %q / %d", published.CustomerName, published.CustomerID, "Draft Customer", cust.ID)
	}
	if p, _ := f.q.GetProject(ctx, db.GetProjectParams{ID: draft.ID, TenantID: f.tenantA.ID}); p.Status != db.ProjectStatusInProgress || published.Status != "in_progress" {
		t.Fatalf("not published: %s / %s", p.Status, published.Status)
	}
	if jobs, _ := f.q.ListJobsByProject(ctx, draft.ID); len(jobs) != 1 || jobs[0].TargetLanguage != "de" {
		t.Fatalf("publish should create the draft file's job: %+v", jobs)
	}
	if isErr, text := callTool(t, s, "openloc_publish_project", map[string]any{"project_id": draft.ID}, nil); !isErr || !strings.Contains(text, "only a draft") {
		t.Errorf("published a live project: %v %s", isErr, text)
	}

	// get / rename.
	fid := attached.File.ID
	var got fileOut
	if isErr, text := callTool(t, s, "openloc_rename_file", map[string]any{"file_id": fid, "name": " Final.txt "}, &got); isErr || got.Name != "Final.txt" {
		t.Fatalf("rename_file: %v %s %+v", isErr, text, got)
	}
	if row, _ := f.q.GetFileTenant(ctx, db.GetFileTenantParams{ID: fid, TenantID: f.tenantA.ID}); row.Name != "Final.txt" {
		t.Fatalf("rename not stored: %q", row.Name)
	}
	if isErr, _ := callTool(t, s, "openloc_rename_file", map[string]any{"file_id": fid, "name": "  "}, nil); !isErr {
		t.Error("a blank name was accepted")
	}
	if isErr, text := callTool(t, s, "openloc_get_file", map[string]any{"file_id": fid}, &got); isErr || got.ProjectID != draft.ID {
		t.Fatalf("get_file: %v %s %+v", isErr, text, got)
	}
	fileB := seedJobWithSegments(t, f.q, f.projectB.ID, "b.txt", "fr", []string{"x"}).FileID
	for tool, args := range map[string]map[string]any{
		"openloc_get_file":        {"file_id": fileB},
		"openloc_rename_file":     {"file_id": fileB, "name": "x"},
		"openloc_export_file":     {"file_id": fileB, "target_language": "fr"},
		"openloc_download_source": {"file_id": fileB},
	} {
		isErr, text := callTool(t, s, tool, args, nil)
		if !isErr {
			t.Errorf("%s reached tenant B's file", tool)
			continue
		}
		assertOpaqueNotFound(t, text)
	}

	// Download links are the REST routes.
	var dl struct {
		DownloadPath string `json:"download_path"`
		Filename     string `json:"filename"`
	}
	if isErr, text := callTool(t, s, "openloc_export_file", map[string]any{"file_id": fid, "target_language": "DE", "format": "xliff"}, &dl); isErr {
		t.Fatalf("export_file: %s", text)
	}
	if dl.DownloadPath != fmt.Sprintf("/api/files/%d/export?format=xliff&lang=de", fid) || dl.Filename != "Final_de.xlf" {
		t.Fatalf("export link: %+v", dl)
	}
	if isErr, text := callTool(t, s, "openloc_export_file", map[string]any{"file_id": fid, "target_language": "fr"}, nil); !isErr || !strings.Contains(text, "target languages") {
		t.Errorf("exported a language the project lacks: %v %s", isErr, text)
	}
	if isErr, text := callTool(t, s, "openloc_download_source", map[string]any{"file_id": fid}, &dl); isErr || dl.DownloadPath != fmt.Sprintf("/api/files/%d/download", fid) || dl.Filename != "Final.txt" {
		t.Fatalf("download_source: %v %s %+v", isErr, text, dl)
	}

	// A linguist reaches a file only through an assignment, as on REST.
	ling := connect(t, f.url, f.tokenLingA)
	if isErr, text := callTool(t, ling, "openloc_download_source", map[string]any{"file_id": fid}, nil); !isErr {
		t.Error("an unassigned linguist got a download link")
	} else {
		assertOpaqueNotFound(t, text)
	}
	draftProject, err := f.q.GetProject(ctx, db.GetProjectParams{ID: draft.ID, TenantID: f.tenantA.ID})
	if err != nil {
		t.Fatal(err)
	}
	draftJobs, _ := f.q.ListJobsByProject(ctx, draft.ID)
	testutil.SeedJobStep(t, f.q, draftProject.SettingsProfileID, draftJobs[0].ID, &f.linguistA.ID)
	if isErr, text := callTool(t, ling, "openloc_export_file", map[string]any{"file_id": fid, "target_language": "de"}, &dl); isErr {
		t.Fatalf("an assigned linguist was refused: %s", text)
	}
}

func TestIntegrationMCPReimportXLIFF(t *testing.T) {
	store := file.NewMemoryStore()
	f := newFixtureWithStore(t, store)
	ctx := context.Background()
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	var attached struct {
		File fileOut `json:"file"`
	}
	if isErr, text := callTool(t, s, "openloc_attach_uploaded_file", map[string]any{
		"project_id": f.projectA.ID, "upload_id": stageUpload(t, f, store, f.tenantA.ID, f.ownerA.ID, "doc.txt", []byte("Hello world\nGood night\n")),
	}, &attached); isErr {
		t.Fatalf("attach: %s", text)
	}
	fid := attached.File.ID
	var key string
	if err := f.pool.QueryRow(ctx, `SELECT segment_key FROM segments WHERE file_id = $1 ORDER BY position LIMIT 1`, fid).Scan(&key); err != nil {
		t.Fatal(err)
	}
	xliff := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
  <file original="doc.txt" source-language="en" target-language="de" datatype="plaintext">
    <body>
      <trans-unit id=%q><source>Hello world</source><target>Hallo Welt</target></trans-unit>
    </body>
  </file>
</xliff>`, key)
	upload := stageUpload(t, f, store, f.tenantA.ID, f.ownerA.ID, "doc_de.xlf", []byte(xliff))
	target := func() string {
		var text *string
		if err := f.pool.QueryRow(ctx, `
			SELECT t.active_text FROM translations t JOIN segments s ON s.id = t.segment_id
			WHERE s.file_id = $1 AND s.segment_key = $2 AND t.target_language = 'de'`, fid, key).Scan(&text); err != nil {
			t.Fatal(err)
		}
		if text == nil {
			return ""
		}
		return *text
	}
	args := map[string]any{"file_id": fid, "target_language": "de", "upload_id": upload}

	var g guardedOut
	if isErr, text := callTool(t, s, "openloc_reimport_xliff", args, &g); isErr {
		t.Fatalf("reimport preview: %s", text)
	}
	if g.Preview == nil || !strings.Contains(g.Preview.Summary, "1 segment changes") {
		t.Fatalf("preview should count the change: %+v", g.Preview)
	}
	if got := target(); got != "" {
		t.Fatalf("the preview wrote a translation: %q", got)
	}
	var res struct {
		Applied bool `json:"applied"`
		Updated int  `json:"updated"`
	}
	if isErr, text := confirmedCall(t, s, "openloc_reimport_xliff", args, &res); isErr {
		t.Fatalf("reimport confirm: %s", text)
	}
	if !res.Applied || res.Updated != 1 || target() != "Hallo Welt" {
		t.Fatalf("reimport not applied: %+v, target %q", res, target())
	}

	// Wrong language, a project manager, an unassigned linguist, another tenant.
	if isErr, text := callTool(t, s, "openloc_reimport_xliff", map[string]any{"file_id": fid, "target_language": "fr", "upload_id": upload}, nil); !isErr || !strings.Contains(text, "not in the project") {
		t.Errorf("a language outside the project previewed: %v %s", isErr, text)
	}
	pm := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "pm@test.com", "pass", "project_manager")
	pmUpload := stageUpload(t, f, store, f.tenantA.ID, pm.ID, "doc_de.xlf", []byte(xliff))
	if isErr, text := callTool(t, connect(t, f.url, writeToken(t, f, f.tenantA.ID, pm.ID)), "openloc_reimport_xliff",
		map[string]any{"file_id": fid, "target_language": "de", "upload_id": pmUpload}, nil); isErr {
		t.Errorf("a project manager could not preview an import: %s", text)
	}
	lingUpload := stageUpload(t, f, store, f.tenantA.ID, f.linguistA.ID, "doc_de.xlf", []byte(xliff))
	if isErr, text := callTool(t, connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.linguistA.ID)), "openloc_reimport_xliff",
		map[string]any{"file_id": fid, "target_language": "de", "upload_id": lingUpload}, nil); !isErr || !strings.Contains(text, "not assigned") {
		t.Errorf("a linguist previewed an import into a task not assigned to them: %v %s", isErr, text)
	}
	fileB := seedJobWithSegments(t, f.q, f.projectB.ID, "b.txt", "fr", []string{"x"}).FileID
	if isErr, text := callTool(t, s, "openloc_reimport_xliff", map[string]any{"file_id": fileB, "target_language": "fr", "upload_id": upload}, nil); !isErr {
		t.Error("previewed a re-import into tenant B's file")
	} else {
		assertOpaqueNotFound(t, text)
	}
}

func TestIntegrationMCPLabelTools(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	type labelOut struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		System bool   `json:"system"`
		Values []struct {
			ID    int64  `json:"id"`
			Value string `json:"value"`
			InUse bool   `json:"in_use"`
		} `json:"values"`
	}
	var list struct {
		Labels []labelOut `json:"labels"`
	}
	if isErr, text := callTool(t, s, "openloc_list_labels", map[string]any{}, &list); isErr {
		t.Fatalf("list_labels: %s", text)
	}
	var customer labelOut
	for _, l := range list.Labels {
		if l.System && l.Name == "Customer" {
			customer = l
		}
	}
	if customer.ID == 0 {
		t.Fatalf("the built-in Customer label is missing: %+v", list.Labels)
	}

	var country labelOut
	if isErr, text := callTool(t, s, "openloc_create_label", map[string]any{"name": "Country", "values": []string{"Germany", " ", "Japan"}}, &country); isErr {
		t.Fatalf("create_label: %s", text)
	}
	if country.ID == 0 || len(country.Values) != 2 {
		t.Fatalf("create_label result (blank value dropped): %+v", country)
	}
	if isErr, text := callTool(t, s, "openloc_create_label", map[string]any{"name": "country"}, nil); !isErr || !strings.Contains(text, "already exists") {
		t.Errorf("a duplicate label name was accepted: %v %s", isErr, text)
	}
	var acme struct {
		ID int64 `json:"id"`
	}
	if isErr, text := callTool(t, s, "openloc_add_label_value", map[string]any{"label_id": customer.ID, "value": "Acme"}, &acme); isErr {
		t.Fatalf("add_label_value: %s", text)
	}
	foreignLabel, err := f.q.CreateLabelDefinition(ctx, db.CreateLabelDefinitionParams{TenantID: f.tenantB.ID, Name: "Theirs"})
	if err != nil {
		t.Fatal(err)
	}
	if isErr, text := callTool(t, s, "openloc_add_label_value", map[string]any{"label_id": foreignLabel.ID, "value": "x"}, nil); !isErr {
		t.Error("added a value to tenant B's label")
	} else {
		assertOpaqueNotFound(t, text)
	}

	// Pick Acme + Germany; the value decides the label.
	germany := country.Values[0].ID
	if isErr, text := callTool(t, s, "openloc_set_project_labels", map[string]any{"project_id": f.projectA.ID, "label_value_ids": []int64{acme.ID, germany}}, nil); isErr {
		t.Fatalf("set_project_labels: %s", text)
	}
	picks, err := f.q.ListProjectLabels(ctx, db.ListProjectLabelsParams{ProjectID: f.projectA.ID, TenantID: f.tenantA.ID})
	if err != nil || len(picks) != 2 {
		t.Fatalf("labels not stored: %+v (%v)", picks, err)
	}
	var settings projectSettingsOut
	if isErr, text := callTool(t, s, "openloc_get_project_settings", map[string]any{"project_id": f.projectA.ID}, &settings); isErr || len(settings.Labels) != 2 {
		t.Fatalf("settings should carry the labels: %v %s %+v", isErr, text, settings.Labels)
	}
	if isErr, _ := callTool(t, s, "openloc_set_project_labels", map[string]any{"project_id": f.projectA.ID, "label_value_ids": []int64{germany, country.Values[1].ID}}, nil); !isErr {
		t.Error("two values of one label were accepted")
	}
	foreignValue, err := f.q.CreateLabelValue(ctx, db.CreateLabelValueParams{LabelDefinitionID: foreignLabel.ID, Value: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if isErr, text := callTool(t, s, "openloc_set_project_labels", map[string]any{"project_id": f.projectA.ID, "label_value_ids": []int64{foreignValue.ID}}, nil); !isErr || !strings.Contains(text, "not found in this tenant") {
		t.Errorf("tenant B's label value was accepted: %v %s", isErr, text)
	}
	if picks, _ := f.q.ListProjectLabels(ctx, db.ListProjectLabelsParams{ProjectID: f.projectA.ID, TenantID: f.tenantA.ID}); len(picks) != 2 {
		t.Fatalf("a refused set changed the labels: %+v", picks)
	}

	deletedAt := func(table string, id int64) bool {
		var gone bool
		if err := f.pool.QueryRow(ctx, "SELECT deleted_at IS NOT NULL FROM "+table+" WHERE id = $1", id).Scan(&gone); err != nil {
			t.Fatalf("%s %d: %v", table, id, err)
		}
		return gone
	}

	// Values: an unused one goes soft.
	japan := country.Values[1].ID
	var g guardedOut
	if isErr, text := callTool(t, s, "openloc_delete_label_value", map[string]any{"value_id": japan}, &g); isErr || g.Preview == nil || !strings.Contains(g.Preview.Summary, "Japan") {
		t.Fatalf("value preview: %v %s %+v", isErr, text, g.Preview)
	}
	if deletedAt("label_values", japan) {
		t.Fatal("the preview deleted the value")
	}
	if isErr, text := confirmedCall(t, s, "openloc_delete_label_value", map[string]any{"value_id": japan}, nil); isErr {
		t.Fatalf("delete value: %s", text)
	}
	if !deletedAt("label_values", japan) {
		t.Fatal("the value was not soft-deleted")
	}

	// A label whose value a project uses is refused.
	if isErr, text := callTool(t, s, "openloc_delete_label", map[string]any{"label_id": country.ID}, nil); !isErr || !strings.Contains(text, "still using values of this label") {
		t.Errorf("an in-use label previewed a delete: %v %s", isErr, text)
	}

	// An in-use value can be deleted: the preview names the projects that lose
	// it, and the delete unsets it on them.
	g = guardedOut{}
	if isErr, text := callTool(t, s, "openloc_delete_label_value", map[string]any{"value_id": germany}, &g); isErr || g.Preview == nil ||
		len(g.Preview.Effects) != 1 || !strings.Contains(g.Preview.Effects[0], "Set on 1 project") {
		t.Fatalf("in-use value preview: %v %s %+v", isErr, text, g.Preview)
	}
	if isErr, text := confirmedCall(t, s, "openloc_delete_label_value", map[string]any{"value_id": germany}, nil); isErr {
		t.Fatalf("delete in-use value: %s", text)
	}
	if !deletedAt("label_values", germany) {
		t.Fatal("the in-use value was not soft-deleted")
	}
	var germanyPicks int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM project_labels WHERE label_value_id = $1", germany).Scan(&germanyPicks); err != nil {
		t.Fatal(err)
	}
	if germanyPicks != 0 {
		t.Errorf("the project kept its pick of the deleted value: %d rows", germanyPicks)
	}

	// With no value in use the label previews a delete.
	if isErr, text := callTool(t, s, "openloc_delete_label", map[string]any{"label_id": country.ID}, nil); isErr {
		t.Errorf("a label with no value in use was refused: %s", text)
	}

	// Labels: built in is refused; unused goes soft. A fresh token, so the
	// per-token rate limit does not trip on this test's call count.
	s = connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	if isErr, text := callTool(t, s, "openloc_delete_label", map[string]any{"label_id": customer.ID}, nil); !isErr || !strings.Contains(text, "built in") {
		t.Errorf("the Customer label previewed a delete: %v %s", isErr, text)
	}
	var spare labelOut
	if isErr, text := callTool(t, s, "openloc_create_label", map[string]any{"name": "Spare"}, &spare); isErr {
		t.Fatalf("create spare: %s", text)
	}
	pm := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "pm@test.com", "pass", "project_manager")
	if isErr, text := callTool(t, connect(t, f.url, writeToken(t, f, f.tenantA.ID, pm.ID)), "openloc_delete_label", map[string]any{"label_id": spare.ID}, nil); !isErr || !strings.Contains(text, "owner or admin") {
		t.Errorf("a project manager previewed a label delete: %v %s", isErr, text)
	}
	if isErr, text := callTool(t, s, "openloc_delete_label", map[string]any{"label_id": foreignLabel.ID}, nil); !isErr {
		t.Error("previewed a delete of tenant B's label")
	} else {
		assertOpaqueNotFound(t, text)
	}
	if isErr, text := confirmedCall(t, s, "openloc_delete_label", map[string]any{"label_id": spare.ID}, nil); isErr {
		t.Fatalf("delete label: %s", text)
	}
	if !deletedAt("label_definitions", spare.ID) {
		t.Fatal("the label was not soft-deleted")
	}
}

// A connected tool that cannot attach a file in the assistant (Claude,
// ChatGPT, Claude Code) sends a text document's content instead; it is
// ingested like an upload on the project page.
func TestIntegrationMCPUploadTextFile(t *testing.T) {
	store := file.NewMemoryStore()
	f := newFixtureWithStore(t, store)
	ctx := context.Background()
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	var added struct {
		File  fileOut `json:"file"`
		Draft bool    `json:"draft"`
	}
	if isErr, text := callTool(t, s, "openloc_upload_text_file", map[string]any{
		"project_id": f.projectA.ID, "filename": "../../docs/brochure.html",
		"content": "<html><body><p>Save the file. Then close it.</p><p>Done</p></body></html>",
	}, &added); isErr {
		t.Fatalf("upload_text_file: %s", text)
	}
	if added.File.Name != "brochure.html" || added.File.Format != "html" || added.File.SegmentCount != 3 || !added.File.HasSource {
		t.Fatalf("upload result: %+v", added)
	}
	jobs, err := f.q.ListJobsByProject(ctx, f.projectA.ID)
	if err != nil {
		t.Fatal(err)
	}
	var newJob bool
	for _, j := range jobs {
		newJob = newJob || (j.FileID == added.File.ID && j.TargetLanguage == "de")
	}
	if !newJob {
		t.Fatalf("no de job for the uploaded file: %+v", jobs)
	}

	refused := []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"project_id": f.projectA.ID, "filename": "report.docx", "content": "PK..."}, "project page"},
		{map[string]any{"project_id": f.projectA.ID, "filename": "big.txt", "content": strings.Repeat("a", 1<<20+1)}, "1 MB"},
		{map[string]any{"project_id": f.projectA.ID, "filename": "empty.txt", "content": "  \n "}, "empty"},
		{map[string]any{"project_id": f.projectA.ID, "filename": "..", "content": "Hello"}, "file name"},
		{map[string]any{"project_id": f.projectA.ID, "filename": "notes", "content": "Hello"}, "unsupported file format"},
	}
	for _, c := range refused {
		isErr, text := callTool(t, s, "openloc_upload_text_file", c.args, nil)
		if !isErr || !strings.Contains(text, c.want) {
			t.Errorf("%v %v: %v %s, want an error mentioning %q", c.args["filename"], len(fmt.Sprint(c.args["content"])), isErr, text, c.want)
		}
	}
	// Another organization's project is not found; a read-only token cannot write.
	isErr, text := callTool(t, s, "openloc_upload_text_file", map[string]any{"project_id": f.projectB.ID, "filename": "a.txt", "content": "Hello"}, nil)
	if !isErr {
		t.Error("uploaded into another organization's project")
	} else {
		assertOpaqueNotFound(t, text)
	}
	if isErr, _ := callTool(t, connect(t, f.url, f.tokenA), "openloc_upload_text_file", map[string]any{"project_id": f.projectA.ID, "filename": "a.txt", "content": "Hello"}, nil); !isErr {
		t.Error("a read-only token uploaded a file")
	}
}

// A tool outside OpenLoc cannot open a download link in the user's browser,
// so a text-format export can come back as content, with the upload's limits.
func TestIntegrationMCPExportFileContent(t *testing.T) {
	store := file.NewMemoryStore()
	f := newFixtureWithStore(t, store)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))

	var added struct {
		File fileOut `json:"file"`
	}
	if isErr, text := callTool(t, s, "openloc_upload_text_file", map[string]any{
		"project_id": f.projectA.ID, "filename": "notes.txt", "content": "Hello world. Second one.\n",
	}, &added); isErr {
		t.Fatalf("upload: %s", text)
	}

	// The first sentence has an approved translation; the second has none.
	ctx := context.Background()
	segs, err := f.q.ListSegmentsByFile(ctx, added.File.ID)
	if err != nil || len(segs) != 2 {
		t.Fatalf("segments = %+v, %v", segs, err)
	}
	tag, err := f.pool.Exec(ctx, `UPDATE translations SET active_text = 'Hallo Welt.', status = 'approved'
		WHERE segment_id = $1 AND target_language = 'de'`, segs[0].ID)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("approve translation: %v (%d rows)", err, tag.RowsAffected())
	}

	var out struct {
		DownloadPath string `json:"download_path"`
		Filename     string `json:"filename"`
		Content      string `json:"content"`
		ContentError string `json:"content_error"`
	}
	if isErr, text := callTool(t, s, "openloc_export_file", map[string]any{
		"file_id": added.File.ID, "target_language": "de", "include_content": true,
	}, &out); isErr {
		t.Fatalf("export with content: %s", text)
	}
	if out.Content != "Hallo Welt. Second one.\n" || out.DownloadPath == "" || out.Filename == "" {
		t.Errorf("export = %+v, want the translated file's text and the link", out)
	}
	// Without include_content, only the link.
	out.Content = ""
	if isErr, text := callTool(t, s, "openloc_export_file", map[string]any{"file_id": added.File.ID, "target_language": "de"}, &out); isErr || out.Content != "" {
		t.Errorf("export without content: %v %s %+v", isErr, text, out)
	}
	// A bilingual XLIFF is text too, with the translation as the target.
	if isErr, text := callTool(t, s, "openloc_export_file", map[string]any{
		"file_id": added.File.ID, "target_language": "de", "format": "xliff", "include_content": true,
	}, &out); isErr || !strings.Contains(out.Content, "<xliff") || !strings.Contains(out.Content, ">Hallo Welt.</target>") {
		t.Errorf("xliff with content: %v %s %+v", isErr, text, out)
	}
	// Each export served as content is in the project's history, like an
	// export from the page; a link alone is recorded when it is opened.
	events := testutil.ProjectEvents(t, f.q, f.projectA.ID, f.tenantA.ID, "job.exported")
	if len(events) != 2 {
		t.Errorf("job.exported events = %d, want one per content export (2)", len(events))
	}

	// A Word file is not text: the link comes back with the reason.
	docx := testutil.SeedFile(t, f.q, f.projectA.ID, "report.docx", db.FileFormatDocx)
	out = struct {
		DownloadPath string `json:"download_path"`
		Filename     string `json:"filename"`
		Content      string `json:"content"`
		ContentError string `json:"content_error"`
	}{}
	if isErr, text := callTool(t, s, "openloc_export_file", map[string]any{
		"file_id": docx.ID, "target_language": "de", "include_content": true,
	}, &out); isErr || out.DownloadPath == "" || out.Content != "" || !strings.Contains(out.ContentError, "not a text format") {
		t.Errorf("docx with content: %v %s %+v", isErr, text, out)
	}

	// An export over the 1 MB limit, or one that is not UTF-8, also keeps the
	// link and says why, and is not recorded: nothing was served.
	if _, err := f.pool.Exec(ctx, `UPDATE translations SET active_text = $2
		WHERE segment_id = $1 AND target_language = 'de'`, segs[0].ID, strings.Repeat("x", 1<<20)); err != nil {
		t.Fatal(err)
	}
	latin1 := testutil.SeedFile(t, f.q, f.projectA.ID, "latin1.txt", db.FileFormatTxt)
	if err := store.Put(ctx, latin1.StoragePath, strings.NewReader("Caf\xe9 au lait.\n")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		fileID int64
		reason string
	}{{added.File.ID, "1 MB"}, {latin1.ID, "UTF-8"}} {
		out.DownloadPath, out.Content, out.ContentError = "", "", ""
		if isErr, text := callTool(t, s, "openloc_export_file", map[string]any{
			"file_id": c.fileID, "target_language": "de", "include_content": true,
		}, &out); isErr || out.DownloadPath == "" || out.Content != "" || !strings.Contains(out.ContentError, c.reason) {
			t.Errorf("%s: %v %s %+v", c.reason, isErr, text, out)
		}
	}
	if events := testutil.ProjectEvents(t, f.q, f.projectA.ID, f.tenantA.ID, "job.exported"); len(events) != 2 {
		t.Errorf("job.exported events = %d after refused content, want still 2", len(events))
	}

	// A linguist reads only a language assigned to them, even in a file they
	// work on in another language.
	two := testutil.SeedProject(t, f.q, f.tenantA.ID, "Two languages", "en", []string{"de", "fr"})
	if isErr, text := callTool(t, s, "openloc_upload_text_file", map[string]any{
		"project_id": two.ID, "filename": "two.txt", "content": "Hello world.\n",
	}, &added); isErr {
		t.Fatalf("upload: %s", text)
	}
	jobs, err := f.q.ListJobsByProject(context.Background(), two.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.FileID == added.File.ID && j.TargetLanguage == "fr" {
			testutil.SeedJobStep(t, f.q, two.SettingsProfileID, j.ID, &f.linguistA.ID)
		}
	}
	ling := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.linguistA.ID))
	if isErr, text := callTool(t, ling, "openloc_export_file", map[string]any{
		"file_id": added.File.ID, "target_language": "fr", "include_content": true,
	}, &out); isErr || out.Content != "Hello world.\n" {
		t.Errorf("linguist, assigned language: %v %s %+v", isErr, text, out)
	}
	if isErr, text := callTool(t, ling, "openloc_export_file", map[string]any{
		"file_id": added.File.ID, "target_language": "de", "include_content": true,
	}, nil); !isErr || !strings.Contains(text, "not assigned to you") {
		t.Errorf("linguist, another language: %v %s", isErr, text)
	}
}
