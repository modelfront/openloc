package mcp_test

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/testutil"
)

// resourcesFixture is the standard two-tenant fixture on a router that keeps
// files in memory, so the import tools can read assistant uploads.
type resourcesFixture struct {
	*fixture
	pool  *pgxpool.Pool
	store *file.MemoryStore
	pmA   db.User
}

func newResourcesFixture(t *testing.T) *resourcesFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	store := file.NewMemoryStore()
	srv := httptest.NewServer(testutil.NewTestRouterWithStorage(pool, nil, store))
	t.Cleanup(srv.Close)

	f := &fixture{q: q, url: srv.URL}
	f.tenantA = testutil.SeedTenant(t, q, "ResTenantA")
	f.tenantB = testutil.SeedTenant(t, q, "ResTenantB")
	f.ownerA = testutil.SeedUserWithRole(t, q, f.tenantA.ID, "res-a@test.com", "pass", "owner")
	f.linguistA = testutil.SeedUserWithRole(t, q, f.tenantA.ID, "res-l@test.com", "pass", "linguist")
	f.ownerB = testutil.SeedUserWithRole(t, q, f.tenantB.ID, "res-b@test.com", "pass", "owner")
	f.projectA = testutil.SeedProject(t, q, f.tenantA.ID, "Res Alpha", "en", []string{"de"})
	f.tokenA = testutil.SeedAPIToken(t, q, f.tenantA.ID, f.ownerA.ID, auth.CapabilityRead, time.Hour)
	pm := testutil.SeedUserWithRole(t, q, f.tenantA.ID, "res-pm@test.com", "pass", "project_manager")
	return &resourcesFixture{fixture: f, pool: pool, store: store, pmA: pm}
}

// stageUpload stores a file the way POST /api/chat/uploads does and returns
// its upload_id.
func (rf *resourcesFixture) stageUpload(t *testing.T, tenantID, userID int64, name, body string) string {
	t.Helper()
	ctx := context.Background()
	key := "chat-uploads/test/" + uuid.NewString() + "-" + name
	if err := rf.store.Put(ctx, key, bytes.NewReader([]byte(body))); err != nil {
		t.Fatalf("put upload: %v", err)
	}
	row, err := rf.q.InsertChatUpload(ctx, db.InsertChatUploadParams{
		TenantID: tenantID, UserID: userID, Filename: name, SizeBytes: int64(len(body)), StorageKey: key,
	})
	if err != nil {
		t.Fatalf("insert upload: %v", err)
	}
	return uuid.UUID(row.ID.Bytes).String()
}

func seedTMEntry(t *testing.T, q db.Querier, tmID int64, src, tgt string) db.TranslationMemory {
	t.Helper()
	e, err := q.InsertTM(context.Background(), db.InsertTMParams{
		TmID: tmID, SourceLanguage: "en", TargetLanguage: "de", SourceText: src, TargetText: tgt, CreatedBy: "seed",
	})
	if err != nil {
		t.Fatalf("seed TM entry: %v", err)
	}
	return e
}

type tmEntryOut struct {
	EntryID    int64  `json:"entry_id"`
	SourceText string `json:"source_text"`
	TargetText string `json:"target_text"`
	HasTags    bool   `json:"has_tags"`
	Version    string `json:"version"`
}

type tmEntryPageOut struct {
	Entries    []tmEntryOut `json:"entries"`
	NextCursor int64        `json:"next_cursor"`
	HasMore    bool         `json:"has_more"`
	Total      int64        `json:"total"`
}

func TestIntegrationMCPTMCreateRenameAndEntries(t *testing.T) {
	rf := newResourcesFixture(t)
	s := connect(t, rf.url, writeToken(t, rf.fixture, rf.tenantA.ID, rf.ownerA.ID))

	var created struct {
		TMID            int64    `json:"tm_id"`
		SourceLanguage  string   `json:"source_language"`
		TargetLanguages []string `json:"target_languages"`
	}
	if isErr, text := callTool(t, s, "openloc_create_tm", map[string]any{
		"name": "Legal EN-DE", "source_language": "EN", "target_languages": []string{"de", "fr-FR"},
	}, &created); isErr {
		t.Fatalf("create_tm: %s", text)
	}
	tm, err := rf.q.GetTMCollection(context.Background(), db.GetTMCollectionParams{ID: created.TMID, TenantID: rf.tenantA.ID})
	if err != nil {
		t.Fatalf("created TM not in tenant A: %v", err)
	}
	if tm.CreatedBy == nil || *tm.CreatedBy != rf.ownerA.ID || created.SourceLanguage != "en" || len(created.TargetLanguages) != 2 {
		t.Errorf("create_tm stored %+v / returned %+v", tm, created)
	}
	// The same validation as the REST form: a target equal to the source is refused.
	if isErr, text := callTool(t, s, "openloc_create_tm", map[string]any{
		"name": "Bad", "source_language": "en", "target_languages": []string{"en"},
	}, nil); !isErr || !strings.Contains(text, "cannot be the same as source") {
		t.Errorf("source==target should be refused with the service message, got isErr=%v %q", isErr, text)
	}

	if isErr, text := callTool(t, s, "openloc_rename_tm", map[string]any{"tm_id": tm.ID, "name": "  Legal v2 "}, nil); isErr {
		t.Fatalf("rename_tm: %s", text)
	}
	tm, _ = rf.q.GetTMCollection(context.Background(), db.GetTMCollectionParams{ID: tm.ID, TenantID: rf.tenantA.ID})
	if tm.Name != "Legal v2" {
		t.Errorf("rename_tm stored %q", tm.Name)
	}

	for _, pair := range [][2]string{{"Contract", "Vertrag"}, {"Contract term", "Vertragslaufzeit"}, {"Invoice", "Rechnung"}} {
		seedTMEntry(t, rf.q, tm.ID, pair[0], pair[1])
	}
	var list struct {
		TMs []struct {
			TMID       int64 `json:"tm_id"`
			EntryCount int64 `json:"entry_count"`
		} `json:"tms"`
	}
	if isErr, text := callTool(t, s, "openloc_list_tms", map[string]any{}, &list); isErr {
		t.Fatalf("list_tms: %s", text)
	}
	if len(list.TMs) != 1 || list.TMs[0].EntryCount != 3 {
		t.Errorf("list_tms = %+v, want the one TM with 3 entries", list.TMs)
	}

	// Search narrows, and the cursor pages.
	var page tmEntryPageOut
	if isErr, text := callTool(t, s, "openloc_list_tm_entries", map[string]any{"tm_id": tm.ID, "query": "contract", "limit": 1}, &page); isErr {
		t.Fatalf("list_tm_entries: %s", text)
	}
	if page.Total != 2 || len(page.Entries) != 1 || !page.HasMore {
		t.Fatalf("search page 1 = %+v, want 1 of 2 with more", page)
	}
	var page2 tmEntryPageOut
	callTool(t, s, "openloc_list_tm_entries", map[string]any{"tm_id": tm.ID, "query": "contract", "limit": 1, "cursor": page.NextCursor}, &page2)
	if len(page2.Entries) != 1 || page2.Entries[0].EntryID == page.Entries[0].EntryID {
		t.Fatalf("search page 2 = %+v, want the other match", page2)
	}

	// The query is plain text: "Con.ract" is not a pattern, so it matches nothing.
	var literal tmEntryPageOut
	if isErr, text := callTool(t, s, "openloc_list_tm_entries", map[string]any{"tm_id": tm.ID, "query": "Con.ract"}, &literal); isErr {
		t.Fatalf("list_tm_entries literal: %s", text)
	}
	if literal.Total != 0 || len(literal.Entries) != 0 {
		t.Errorf("a dot must match only a dot, got %+v", literal)
	}
	// A search the service refuses comes back with its own message, not a generic failure.
	if isErr, text := callTool(t, s, "openloc_list_tm_entries", map[string]any{"tm_id": tm.ID, "query": strings.Repeat("a", 1001)}, nil); !isErr || !strings.Contains(text, "The search text is too long") {
		t.Errorf("an over-long query should be refused with the service message, got isErr=%v %q", isErr, text)
	}

	// Edit with the version from the listing; the old text lands in history.
	target := page.Entries[0]
	var edited tmEntryOut
	if isErr, text := callTool(t, s, "openloc_update_tm_entry", map[string]any{
		"tm_id": tm.ID, "entry_id": target.EntryID, "source_text": target.SourceText,
		"target_text": "Neuer Text", "version": target.Version,
	}, &edited); isErr {
		t.Fatalf("update_tm_entry: %s", text)
	}
	row, err := rf.q.GetTMEntryScoped(context.Background(), db.GetTMEntryScopedParams{ID: target.EntryID, TmID: tm.ID, TenantID: rf.tenantA.ID})
	if err != nil || row.TargetText != "Neuer Text" {
		t.Fatalf("entry after edit = %+v, %v", row, err)
	}
	history, err := rf.q.ListTMEntryHistory(context.Background(), target.EntryID)
	if err != nil || len(history) != 1 || history[0].TargetText != target.TargetText {
		t.Errorf("history = %+v, %v; want the pre-edit text", history, err)
	}
	// The old version is now stale: a second edit with it is refused, not applied.
	if isErr, text := callTool(t, s, "openloc_update_tm_entry", map[string]any{
		"tm_id": tm.ID, "entry_id": target.EntryID, "source_text": target.SourceText,
		"target_text": "Überschrieben", "version": target.Version,
	}, nil); !isErr || !strings.Contains(text, "re-read") {
		t.Errorf("stale version should be refused as a conflict, got isErr=%v %q", isErr, text)
	}
	row, _ = rf.q.GetTMEntryScoped(context.Background(), db.GetTMEntryScopedParams{ID: target.EntryID, TmID: tm.ID, TenantID: rf.tenantA.ID})
	if row.TargetText != "Neuer Text" {
		t.Errorf("a stale edit overwrote the entry: %q", row.TargetText)
	}

	var exp struct {
		DownloadPath string `json:"download_path"`
		Filename     string `json:"filename"`
	}
	if isErr, text := callTool(t, s, "openloc_export_tmx", map[string]any{"tm_id": tm.ID}, &exp); isErr {
		t.Fatalf("export_tmx: %s", text)
	}
	if exp.DownloadPath != "/api/tms/"+itoa(tm.ID)+"/export" || exp.Filename != "Legal v2.tmx" {
		t.Errorf("export_tmx = %+v", exp)
	}
}

func TestIntegrationMCPTMEntryWithTagsKeepsTags(t *testing.T) {
	rf := newResourcesFixture(t)
	s := connect(t, rf.url, writeToken(t, rf.fixture, rf.tenantA.ID, rf.ownerA.ID))
	tm := testutil.SeedTMWithLangs(t, rf.q, rf.tenantA.ID, "Tagged", "en", []string{"de"})
	rich := `[{"type":"text","text":"Click "},{"type":"tag","id":"1"}]`
	e, err := rf.q.InsertTM(context.Background(), db.InsertTMParams{
		TmID: tm.ID, SourceLanguage: "en", TargetLanguage: "de", SourceText: "Click OK",
		TargetText: "OK klicken", CreatedBy: "seed", SourceRichText: rich,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if isErr, text := callTool(t, s, "openloc_update_tm_entry", map[string]any{
		"tm_id": tm.ID, "entry_id": e.ID, "source_text": "Click Cancel", "target_text": "OK klicken",
	}, nil); !isErr || !strings.Contains(text, "inline tags") {
		t.Errorf("editing the tagged side should be refused, got isErr=%v %q", isErr, text)
	}
	if isErr, text := callTool(t, s, "openloc_update_tm_entry", map[string]any{
		"tm_id": tm.ID, "entry_id": e.ID, "source_text": "Click OK", "target_text": "Auf OK klicken",
	}, nil); isErr {
		t.Fatalf("editing the untagged side: %s", text)
	}
	row, _ := rf.q.GetTMEntryScoped(context.Background(), db.GetTMEntryScopedParams{ID: e.ID, TmID: tm.ID, TenantID: rf.tenantA.ID})
	if row.TargetText != "Auf OK klicken" || row.SourceRichText == nil || *row.SourceRichText != rich {
		t.Errorf("after edit: target=%q source_rich=%v; the source tags must survive", row.TargetText, row.SourceRichText)
	}
}

const twoUnitTMX = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header creationtool="test" creationtoolversion="1" datatype="plaintext" segtype="sentence" adminlang="en" srclang="en" o-tmf="x"/>
  <body>
    <tu tuid="1"><tuv xml:lang="en"><seg>Good morning</seg></tuv><tuv xml:lang="de"><seg>Guten Morgen</seg></tuv></tu>
    <tu tuid="2"><tuv xml:lang="en"><seg>Thank you</seg></tuv><tuv xml:lang="de"><seg>Danke</seg></tuv></tu>
  </body>
</tmx>`

func TestIntegrationMCPImportTMX(t *testing.T) {
	rf := newResourcesFixture(t)
	s := connect(t, rf.url, writeToken(t, rf.fixture, rf.tenantA.ID, rf.pmA.ID))
	tm := testutil.SeedTMWithLangs(t, rf.q, rf.tenantA.ID, "Import target", "en", []string{"de"})

	upload := rf.stageUpload(t, rf.tenantA.ID, rf.pmA.ID, "greetings.tmx", twoUnitTMX)
	var imp struct {
		ImportID int64  `json:"import_id"`
		FileName string `json:"file_name"`
		Hint     string `json:"hint"`
	}
	if isErr, text := callTool(t, s, "openloc_import_tmx", map[string]any{"tm_id": tm.ID, "upload_id": upload}, &imp); isErr {
		t.Fatalf("import_tmx: %s", text)
	}
	if imp.ImportID == 0 || imp.FileName != "greetings.tmx" || !strings.Contains(imp.Hint, "openloc_list_tm_imports") {
		t.Errorf("import_tmx = %+v", imp)
	}

	// The upsert runs in the background; follow it the way the model would.
	var status string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var list struct {
			Imports []struct {
				Status   string `json:"status"`
				Imported int32  `json:"imported"`
			} `json:"imports"`
		}
		if isErr, text := callTool(t, s, "openloc_list_tm_imports", map[string]any{"tm_id": tm.ID}, &list); isErr {
			t.Fatalf("list_tm_imports: %s", text)
		}
		if len(list.Imports) == 1 && list.Imports[0].Status == "completed" {
			status = list.Imports[0].Status
			if list.Imports[0].Imported != 2 {
				t.Errorf("imported = %d, want 2", list.Imports[0].Imported)
			}
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if status != "completed" {
		t.Fatal("the import never completed")
	}
	n, err := rf.q.CountTMEntries(context.Background(), db.CountTMEntriesParams{TmID: tm.ID})
	if err != nil || n != 2 {
		t.Errorf("TM holds %d entries (%v), want 2", n, err)
	}

	// A file that is not TMX is a caller mistake with the service's message.
	bad := rf.stageUpload(t, rf.tenantA.ID, rf.pmA.ID, "notes.tmx", "not xml at all <")
	if isErr, text := callTool(t, s, "openloc_import_tmx", map[string]any{"tm_id": tm.ID, "upload_id": bad}, nil); !isErr || !strings.Contains(text, "invalid TMX") {
		t.Errorf("malformed TMX should be refused, got isErr=%v %q", isErr, text)
	}
	// Another user's upload is not found, even in the same tenant.
	owners := rf.stageUpload(t, rf.tenantA.ID, rf.ownerA.ID, "owner.tmx", twoUnitTMX)
	if isErr, text := callTool(t, s, "openloc_import_tmx", map[string]any{"tm_id": tm.ID, "upload_id": owners}, nil); !isErr {
		t.Error("another user's upload was imported")
	} else {
		assertOpaqueNotFound(t, text)
	}
}

func TestIntegrationMCPDeleteTMIsGuardedSoftDeleteOwnerAdminOnly(t *testing.T) {
	rf := newResourcesFixture(t)
	owner := connect(t, rf.url, writeToken(t, rf.fixture, rf.tenantA.ID, rf.ownerA.ID))
	pm := connect(t, rf.url, writeToken(t, rf.fixture, rf.tenantA.ID, rf.pmA.ID))
	tm := testutil.SeedTMWithLangs(t, rf.q, rf.tenantA.ID, "Old memory", "en", []string{"de"})
	seedTMEntry(t, rf.q, tm.ID, "One", "Eins")
	seedTMEntry(t, rf.q, tm.ID, "Two", "Zwei")

	// A project manager may not delete a TM — not even preview it.
	if isErr, text := callTool(t, pm, "openloc_delete_tm", map[string]any{"tm_id": tm.ID}, nil); !isErr || !strings.Contains(text, "owner or admin") {
		t.Errorf("PM delete_tm should be refused, got isErr=%v %q", isErr, text)
	}

	var g guardedOut
	if isErr, text := callTool(t, owner, "openloc_delete_tm", map[string]any{"tm_id": tm.ID}, &g); isErr {
		t.Fatalf("preview: %s", text)
	}
	if g.Status != "needs_confirmation" || !strings.Contains(g.Preview.Summary, "Old memory") ||
		len(g.Preview.Effects) != 1 || !strings.Contains(g.Preview.Effects[0], "2 entries") {
		t.Fatalf("preview should name the TM and its 2 entries, got %+v", g.Preview)
	}
	if _, err := rf.q.GetTMCollection(context.Background(), db.GetTMCollectionParams{ID: tm.ID, TenantID: rf.tenantA.ID}); err != nil {
		t.Fatalf("the preview deleted the TM: %v", err)
	}

	if isErr, text := confirmedCall(t, owner, "openloc_delete_tm", map[string]any{"tm_id": tm.ID}, nil); isErr {
		t.Fatalf("confirm: %s", text)
	}
	// Soft delete: the row and its entries stay, with deleted_at set, and reads skip it.
	var deletedAt *time.Time
	if err := rf.pool.QueryRow(context.Background(), `SELECT deleted_at FROM tm_collections WHERE id = $1`, tm.ID).Scan(&deletedAt); err != nil {
		t.Fatalf("TM row is gone — delete must be soft: %v", err)
	}
	if deletedAt == nil {
		t.Error("deleted_at is not set")
	}
	var entries int
	if err := rf.pool.QueryRow(context.Background(), `SELECT count(*) FROM translation_memory WHERE tm_id = $1`, tm.ID).Scan(&entries); err != nil || entries != 2 {
		t.Errorf("entries after soft delete = %d (%v), want 2 kept", entries, err)
	}
	if isErr, text := callTool(t, owner, "openloc_get_tm", map[string]any{"tm_id": tm.ID}, nil); !isErr {
		t.Error("get_tm still finds a deleted TM")
	} else {
		assertOpaqueNotFound(t, text)
	}
	var list struct {
		Count int `json:"count"`
	}
	callTool(t, owner, "openloc_list_tms", map[string]any{}, &list)
	if list.Count != 0 {
		t.Errorf("list_tms still lists %d TM(s)", list.Count)
	}
}

func TestIntegrationMCPDeleteTMRefusedWhileAssigned(t *testing.T) {
	rf := newResourcesFixture(t)
	owner := connect(t, rf.url, writeToken(t, rf.fixture, rf.tenantA.ID, rf.ownerA.ID))
	tm := testutil.SeedTMWithLangs(t, rf.q, rf.tenantA.ID, "In use", "en", []string{"de"})
	if _, err := rf.q.CreateSettingsTMAssignment(context.Background(), db.CreateSettingsTMAssignmentParams{
		SettingsProfileID: rf.projectA.SettingsProfileID, TmID: tm.ID, Mode: "read_write",
	}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if isErr, text := callTool(t, owner, "openloc_delete_tm", map[string]any{"tm_id": tm.ID}, nil); !isErr || !strings.Contains(text, "1 assignment") {
		t.Errorf("preview of an assigned TM should be refused naming the assignment, got isErr=%v %q", isErr, text)
	}
	if _, err := rf.q.GetTMCollection(context.Background(), db.GetTMCollectionParams{ID: tm.ID, TenantID: rf.tenantA.ID}); err != nil {
		t.Errorf("the refused delete removed the TM: %v", err)
	}
}

// Every TM tool answers the same opaque not-found for another tenant's ids,
// and changes nothing.
func TestIntegrationMCPTMToolsCrossTenant(t *testing.T) {
	rf := newResourcesFixture(t)
	intruder := connect(t, rf.url, writeToken(t, rf.fixture, rf.tenantB.ID, rf.ownerB.ID))
	tm := testutil.SeedTMWithLangs(t, rf.q, rf.tenantA.ID, "Tenant A memory", "en", []string{"de"})
	e := seedTMEntry(t, rf.q, tm.ID, "Secret", "Geheim")
	upload := rf.stageUpload(t, rf.tenantB.ID, rf.ownerB.ID, "b.tmx", twoUnitTMX)
	profile := testutil.SeedTMProfile(t, rf.q, rf.tenantA.ID, "Tenant A profile", 0.75)
	updateProfile := map[string]any{"tm_profile_id": profile.ID, "name": "Pwned"}
	deleteProfile := map[string]any{"tm_profile_id": profile.ID}

	calls := map[string]map[string]any{
		"openloc_get_tm":            {"tm_id": tm.ID},
		"openloc_list_tm_entries":   {"tm_id": tm.ID},
		"openloc_list_tm_imports":   {"tm_id": tm.ID},
		"openloc_rename_tm":         {"tm_id": tm.ID, "name": "Pwned"},
		"openloc_update_tm_entry":   {"tm_id": tm.ID, "entry_id": e.ID, "source_text": "x", "target_text": "y"},
		"openloc_import_tmx":        {"tm_id": tm.ID, "upload_id": upload},
		"openloc_export_tmx":        {"tm_id": tm.ID},
		"openloc_delete_tm":         {"tm_id": tm.ID},
		"openloc_update_tm_profile": updateProfile,
		"openloc_delete_tm_profile": deleteProfile,
	}
	for tool, args := range calls {
		isErr, text := callTool(t, intruder, tool, args, nil)
		if !isErr {
			t.Errorf("%s on another tenant's id succeeded", tool)
			continue
		}
		assertOpaqueNotFound(t, text)
	}
	// A TM tenant B creates may not carry tenant A's customer. The service
	// words it "not found in this tenant"; the empty list_tms below shows no
	// TM was made.
	customer, err := rf.q.CreateCustomer(context.Background(), db.CreateCustomerParams{TenantID: rf.tenantA.ID, Name: "Tenant A customer"})
	if err != nil {
		t.Fatal(err)
	}
	newTM := map[string]any{"name": "Pwned", "source_language": "en", "target_languages": []string{"de"}, "customer_id": customer.ID}
	cfgMustRefuse(t, intruder, "openloc_create_tm", newTM, "customer not found in this tenant")
	got, err := rf.q.GetTMCollection(context.Background(), db.GetTMCollectionParams{ID: tm.ID, TenantID: rf.tenantA.ID})
	if err != nil || got.Name != "Tenant A memory" {
		t.Errorf("tenant A's TM changed: %+v, %v", got, err)
	}
	var list struct {
		Count int `json:"count"`
	}
	callTool(t, intruder, "openloc_list_tms", map[string]any{}, &list)
	if list.Count != 0 {
		t.Errorf("tenant B lists %d TM(s) of tenant A", list.Count)
	}
	if p, err := rf.q.GetTMProfile(context.Background(), db.GetTMProfileParams{ID: profile.ID, TenantID: rf.tenantA.ID}); err != nil || p.Name != "Tenant A profile" {
		t.Errorf("tenant A's TM profile changed or went: %+v, %v", p, err)
	}
	if isErr, text := callTool(t, intruder, "openloc_list_tm_profiles", map[string]any{}, &list); isErr || list.Count != 0 {
		t.Errorf("tenant B lists %d TM profile(s) of tenant A (isErr=%v %q)", list.Count, isErr, text)
	}

	// Tenant A gets a preview from the same calls, so the refusals above are
	// the tenant check and not a malformed call.
	owner := connect(t, rf.url, writeToken(t, rf.fixture, rf.tenantA.ID, rf.ownerA.ID))
	cfgPreviewOnly(t, owner, "openloc_update_tm_profile", updateProfile)
	cfgPreviewOnly(t, owner, "openloc_delete_tm_profile", deleteProfile)
	if isErr, text := callTool(t, owner, "openloc_create_tm", newTM, nil); isErr {
		t.Errorf("create_tm with tenant A's own customer: %s", text)
	}
}

func TestIntegrationMCPResourceWritesNeedWriteAccess(t *testing.T) {
	rf := newResourcesFixture(t)
	readOnly := connect(t, rf.url, rf.tokenA)
	tm := testutil.SeedTMWithLangs(t, rf.q, rf.tenantA.ID, "Read only", "en", []string{"de"})
	base := testutil.SeedTermBase(t, rf.q, rf.tenantA.ID, "Glossary", "en", "de")
	for tool, args := range map[string]map[string]any{
		"openloc_create_tm":         {"name": "X", "source_language": "en", "target_languages": []string{"de"}},
		"openloc_rename_tm":         {"tm_id": tm.ID, "name": "Y"},
		"openloc_delete_tm":         {"tm_id": tm.ID},
		"openloc_create_tm_profile": {"name": "P"},
		"openloc_rename_term_base":  {"term_base_id": base.ID, "name": "Z"},
		"openloc_delete_term_base":  {"term_base_id": base.ID},
	} {
		if isErr, text := callTool(t, readOnly, tool, args, nil); !isErr || !strings.Contains(text, "write access") {
			t.Errorf("%s with a read-only token: isErr=%v %q", tool, isErr, text)
		}
	}
	got, _ := rf.q.GetTMCollection(context.Background(), db.GetTMCollectionParams{ID: tm.ID, TenantID: rf.tenantA.ID})
	if got.Name != "Read only" {
		t.Errorf("a read-only token renamed the TM to %q", got.Name)
	}
}
