package extension_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"openloc/pkg/extsdk"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/extension"
	"openloc/internal/extension/exttest"
	"openloc/internal/file"
	"openloc/internal/secrets"
	"openloc/internal/testutil"
)

type extensionResp struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

func listExtensions(t *testing.T, router http.Handler, userID, tenantID int64, role string) []extensionResp {
	t.Helper()
	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/ai/extensions", nil, userID, tenantID, role)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var out struct {
		Data []extensionResp `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &out)
	return out.Data
}

type vendorResp struct {
	Provider    string `json:"provider"`
	DisplayName string `json:"display_name"`
	Connected   bool   `json:"connected"`
	Extension   *struct {
		ID string `json:"id"`
	} `json:"extension"`
}

func listVendors(t *testing.T, router http.Handler, userID, tenantID int64) map[string]vendorResp {
	t.Helper()
	rec := testutil.DoRequestWithRole(t, router, "GET", "/api/ai/vendors", nil, userID, tenantID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var out struct {
		Data []vendorResp `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &out)
	byProvider := map[string]vendorResp{}
	for _, v := range out.Data {
		byProvider[v.Provider] = v
	}
	return byProvider
}

// The whole life of an extension through the API: upload → listed → offered
// as an AI vendor → a key is checked by the extension and saved → an engine
// runs on it → delete hides it, keeps its row and deletes its keys.
func TestIntegrationExtensionLifecycle(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	store := file.NewMemoryStore()
	router := testutil.NewTestRouterWithExtensions(pool, store, nil)
	tenant := testutil.SeedTenant(t, q, "ExtCo")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@ext.test", "pass", "owner")
	pm := testutil.SeedUserWithRole(t, q, tenant.ID, "pm@ext.test", "pass", "project_manager")

	// Upload.
	rec := testutil.DoFileUploadWithRole(t, router, "/api/ai/extensions", "mockqe.zip", string(exttest.Zip(t, nil)), owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var created struct {
		Data extensionResp `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &created)
	if created.Data.ID != "mockqe" || created.Data.Provider != "ext:mockqe" || created.Data.Name != "Mock QE" {
		t.Fatalf("upload response = %+v", created.Data)
	}

	// A broken zip is a field error, not a 500.
	rec = testutil.DoFileUploadWithRole(t, router, "/api/ai/extensions", "x.zip", "not a zip", owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "not a zip") {
		t.Errorf("want the package error in the body, got %s", rec.Body.String())
	}

	// Listed for managers.
	if got := listExtensions(t, router, pm.ID, tenant.ID, "project_manager"); len(got) != 1 || got[0].ID != "mockqe" {
		t.Fatalf("extensions listed to the PM = %+v", got)
	}

	// Offered as an AI vendor, not yet connected.
	v, ok := listVendors(t, router, owner.ID, tenant.ID)["ext:mockqe"]
	if !ok || v.DisplayName != "Mock QE" || v.Extension == nil || v.Extension.ID != "mockqe" || v.Connected {
		t.Fatalf("vendor list entry for ext:mockqe = %+v (present=%v)", v, ok)
	}

	// A key on an endpoint nothing listens on fails the extension's own
	// check_key, so it is not saved.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/vendors/ext:mockqe/connections",
		map[string]any{"name": "Dead endpoint", "base_url": "https://127.0.0.1:1"}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var deadConn struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &deadConn)
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/vendors/ext:mockqe/tokens",
		map[string]any{"name": "Bad", "api_key": "k-1", "connection_id": deadConn.Data.ID}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "Mock QE") {
		t.Errorf("key error should name the extension, got %s", rec.Body.String())
	}

	// With no endpoint the mock accepts the key.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/vendors/ext:mockqe/connections",
		map[string]any{"name": "Local"}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var conn struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &conn)
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/vendors/ext:mockqe/tokens",
		map[string]any{"name": "Main", "api_key": "k-2", "connection_id": conn.Data.ID}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	var tok struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &tok)
	if !listVendors(t, router, owner.ID, tenant.ID)["ext:mockqe"].Connected {
		t.Error("the extension vendor should read as connected once it has a key")
	}

	// A QE+APE engine runs on the key; its model is the extension id.
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/engines",
		map[string]any{"name": "Mock reviewer", "rows": []map[string]any{
			{"token_id": tok.Data.ID, "model": "mockqe"},
		}}, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)

	// Delete: gone from the list and the vendors, but soft-deleted.
	rec = testutil.DoRequestWithRole(t, router, "DELETE", "/api/ai/extensions/mockqe", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	if got := listExtensions(t, router, owner.ID, tenant.ID, "owner"); len(got) != 0 {
		t.Errorf("a deleted extension is still listed: %+v", got)
	}
	if _, ok := listVendors(t, router, owner.ID, tenant.ID)["ext:mockqe"]; ok {
		t.Error("a deleted extension is still offered as an AI vendor")
	}
	var deletedAt *string
	if err := pool.QueryRow(ctx,
		`SELECT deleted_at::text FROM extensions WHERE tenant_id = $1 AND ext_id = 'mockqe'`, tenant.ID,
	).Scan(&deletedAt); err != nil {
		t.Fatalf("the extension row is gone, want it soft-deleted: %v", err)
	}
	if deletedAt == nil {
		t.Error("deleted_at is NULL after delete")
	}
	// Its vendor row, keys and endpoints are deleted for real, so the engine
	// reads as having no key.
	if _, err := q.GetVendorToken(ctx, db.GetVendorTokenParams{ID: tok.Data.ID, TenantID: tenant.ID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("the extension's key survived its removal: err = %v", err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_vendors WHERE tenant_id = $1 AND provider = 'ext:mockqe'`, tenant.ID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Errorf("%d vendor rows left for the removed extension, want 0", left)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_connections WHERE id IN ($1, $2)`, conn.Data.ID, deadConn.Data.ID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Errorf("%d endpoints left for the removed extension, want 0", left)
	}
	rec = testutil.DoRequestWithRole(t, router, "GET", "/api/ai/engines", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusOK)
	var engines struct {
		Data []struct {
			Name     string `json:"name"`
			Unusable string `json:"unusable"`
		} `json:"data"`
	}
	testutil.DecodeJSON(t, rec, &engines)
	if len(engines.Data) != 1 || engines.Data[0].Unusable != "no_key" {
		t.Errorf("engines after removing their provider = %+v, want one with unusable no_key", engines.Data)
	}
	rec = testutil.DoRequestWithRole(t, router, "DELETE", "/api/ai/extensions/mockqe", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// Installing it again makes a new live row next to the deleted one.
	rec = testutil.DoFileUploadWithRole(t, router, "/api/ai/extensions", "mockqe.zip", string(exttest.Zip(t, nil)), owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	if got := listExtensions(t, router, owner.ID, tenant.ID, "owner"); len(got) != 1 {
		t.Errorf("reinstalled extension not listed: %+v", got)
	}
}

// Tenant B can neither see, delete, nor run tenant A's extension.
func TestIntegrationExtensionTenantIsolation(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	store := file.NewMemoryStore()
	router := testutil.NewTestRouterWithExtensions(pool, store, nil)
	tenantA := testutil.SeedTenant(t, q, "ExtTenantA")
	tenantB := testutil.SeedTenant(t, q, "ExtTenantB")
	ownerB := testutil.SeedUserWithRole(t, q, tenantB.ID, "ownerB@ext.test", "pass", "owner")

	testutil.InstallMockExtension(t, q, store, tenantA.ID)
	tokenA := testutil.ConnectExtension(t, q, tenantA.ID, testutil.MockExtensionID, "")

	// Tenant B's API.
	if got := listExtensions(t, router, ownerB.ID, tenantB.ID, "owner"); len(got) != 0 {
		t.Errorf("tenant B sees tenant A's extensions: %+v", got)
	}
	if _, ok := listVendors(t, router, ownerB.ID, tenantB.ID)["ext:mockqe"]; ok {
		t.Error("tenant A's extension is offered to tenant B as a vendor")
	}
	rec := testutil.DoRequestWithRole(t, router, "DELETE", "/api/ai/extensions/mockqe", nil, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)
	if _, err := q.GetVendorToken(ctx, db.GetVendorTokenParams{ID: tokenA, TenantID: tenantA.ID}); err != nil {
		t.Errorf("tenant B's remove reached tenant A's key: %v", err)
	}
	rec = testutil.DoRequestWithRole(t, router, "POST", "/api/ai/vendors/ext:mockqe/connections",
		map[string]any{"name": "Sneaky"}, ownerB.ID, tenantB.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusBadRequest)

	// The service, as the pipeline calls it.
	enc, err := secrets.NewEncryptor(testutil.TestAISecretsKey)
	if err != nil {
		t.Fatal(err)
	}
	svc := extension.NewService(q, store, enc, extension.NewRuntime(nil, nil))
	if _, err := svc.GetByProvider(ctx, tenantB.ID, "ext:mockqe"); !errors.Is(err, extension.ErrNotInstalled) {
		t.Errorf("GetByProvider from tenant B = %v, want ErrNotInstalled", err)
	}
	if _, err := svc.PredictorForToken(ctx, tenantB.ID, tokenA); err == nil {
		t.Error("tenant B ran tenant A's key")
	}
	if err := svc.Delete(ctx, tenantB.ID, "mockqe"); !errors.Is(err, extension.ErrNotInstalled) {
		t.Errorf("Delete from tenant B = %v, want ErrNotInstalled", err)
	}
	// Tenant A is unaffected and can run it.
	pred, err := svc.PredictorForToken(ctx, tenantA.ID, tokenA)
	if err != nil {
		t.Fatalf("tenant A cannot run its own extension: %v", err)
	}
	if pred.Provider() != "ext:mockqe" || !pred.APE() || pred.BatchSize() != 2 {
		t.Errorf("predictor = %s ape=%v batch=%d", pred.Provider(), pred.APE(), pred.BatchSize())
	}

	// Tenant B installs its own mockqe: each tenant's key reaches only its own.
	testutil.InstallMockExtension(t, q, store, tenantB.ID)
	if _, err := svc.PredictorForToken(ctx, tenantB.ID, tokenA); err == nil {
		t.Error("tenant B ran tenant A's key after installing the same extension id")
	}

	// Removing it in tenant A deletes only tenant A's keys.
	tokenB := testutil.ConnectExtension(t, q, tenantB.ID, testutil.MockExtensionID, "")
	if err := svc.Delete(ctx, tenantA.ID, "mockqe"); err != nil {
		t.Fatalf("Delete from tenant A: %v", err)
	}
	if _, err := q.GetVendorToken(ctx, db.GetVendorTokenParams{ID: tokenB, TenantID: tenantB.ID}); err != nil {
		t.Errorf("tenant A's remove deleted tenant B's key: %v", err)
	}
	if _, err := svc.PredictorForToken(ctx, tenantB.ID, tokenB); err != nil {
		t.Errorf("tenant B cannot run its own extension after tenant A removed theirs: %v", err)
	}
}

// Remove deletes the extension's keys, so its engines read as having no key.
// A key that still points at a removed extension (a remove racing a run)
// resolves to ErrNotInstalled, which the pipeline turns into a failed step
// naming the fix.
func TestIntegrationExtensionDeletedCannotRun(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	store := file.NewMemoryStore()
	tenant := testutil.SeedTenant(t, q, "ExtDeletedCo")
	testutil.InstallMockExtension(t, q, store, tenant.ID)
	token := testutil.ConnectExtension(t, q, tenant.ID, testutil.MockExtensionID, "")

	enc, err := secrets.NewEncryptor(testutil.TestAISecretsKey)
	if err != nil {
		t.Fatal(err)
	}
	svc := extension.NewService(q, store, enc, extension.NewRuntime(nil, nil))
	if _, err := pool.Exec(ctx,
		`UPDATE extensions SET deleted_at = now() WHERE tenant_id = $1 AND ext_id = 'mockqe'`, tenant.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PredictorForToken(ctx, tenant.ID, token); !errors.Is(err, extension.ErrNotInstalled) {
		t.Fatalf("PredictorForToken on a removed extension's key = %v, want ErrNotInstalled", err)
	}
}

// The stored zip is checked against the digest recorded at install, so a
// module swapped in storage does not run.
func TestIntegrationExtensionStoredModuleDigest(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	store := file.NewMemoryStore()
	tenant := testutil.SeedTenant(t, q, "ExtDigestCo")
	inst := testutil.InstallMockExtension(t, q, store, tenant.ID)
	token := testutil.ConnectExtension(t, q, tenant.ID, testutil.MockExtensionID, "")

	// Replace the stored zip with a valid one whose module differs by a byte.
	wasm := append(append([]byte(nil), exttest.Wasm(t)...), 0)
	swapped := exttest.ZipFiles(t, map[string][]byte{"manifest.json": exttest.Manifest(t), "extension.wasm": wasm})
	if err := store.Put(ctx, inst.Row.StorageKey, bytes.NewReader(swapped)); err != nil {
		t.Fatal(err)
	}
	enc, err := secrets.NewEncryptor(testutil.TestAISecretsKey)
	if err != nil {
		t.Fatal(err)
	}
	svc := extension.NewService(q, store, enc, extension.NewRuntime(nil, nil))
	pred, err := svc.PredictorForToken(ctx, tenant.ID, token)
	if err != nil {
		t.Fatalf("PredictorForToken: %v", err)
	}
	_, err = pred.Predict(ctx, "en", "de", extsdk.PredictMeta{}, nil)
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("a swapped module ran (err=%v), want a digest refusal", err)
	}
}

// Uploading a zip whose id is already installed replaces the live extension in
// place: one live row with the new version and module, and a saved key runs
// the new module without being connected again.
func TestIntegrationExtensionReplaceInPlace(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	store := file.NewMemoryStore()
	router := testutil.NewTestRouterWithExtensions(pool, store, nil)
	tenant := testutil.SeedTenant(t, q, "ExtReplaceCo")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@extreplace.test", "pass", "owner")

	rec := testutil.DoFileUploadWithRole(t, router, "/api/ai/extensions", "mockqe.zip", string(exttest.Zip(t, nil)), owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	token := testutil.ConnectExtension(t, q, tenant.ID, testutil.MockExtensionID, "")

	// v1.1.0: a new manifest (version, batch size) and a different module.
	// A trailing custom section changes the module's digest, not its code.
	var manifest map[string]any
	if err := json.Unmarshal(exttest.Manifest(t), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["version"] = "1.1.0"
	manifest["limits"] = map[string]any{"batch_size": 3}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("v1.1.0")
	section := append([]byte{0x00, byte(1 + 4 + len(payload)), 4, 't', 'e', 's', 't'}, payload...)
	wasm := append(append([]byte{}, exttest.Wasm(t)...), section...)
	sum := sha256.Sum256(wasm)
	newSHA := hex.EncodeToString(sum[:])

	var oldSHA string
	if err := pool.QueryRow(ctx,
		`SELECT sha256 FROM extensions WHERE tenant_id = $1 AND ext_id = 'mockqe' AND deleted_at IS NULL`, tenant.ID,
	).Scan(&oldSHA); err != nil {
		t.Fatal(err)
	}
	if oldSHA == newSHA {
		t.Fatal("the replacement module must have a different digest")
	}

	zip := exttest.ZipFiles(t, map[string][]byte{"manifest.json": manifestJSON, "extension.wasm": wasm})
	rec = testutil.DoFileUploadWithRole(t, router, "/api/ai/extensions", "mockqe-1.1.0.zip", string(zip), owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)

	rows, err := pool.Query(ctx,
		`SELECT version, sha256 FROM extensions WHERE tenant_id = $1 AND ext_id = 'mockqe' AND deleted_at IS NULL`, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	type live struct{ version, sha string }
	var got []live
	for rows.Next() {
		var l live
		if err := rows.Scan(&l.version, &l.sha); err != nil {
			t.Fatal(err)
		}
		got = append(got, l)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].version != "1.1.0" || got[0].sha != newSHA {
		t.Fatalf("live rows = %+v, want one at 1.1.0 with sha %s", got, newSHA)
	}

	enc, err := secrets.NewEncryptor(testutil.TestAISecretsKey)
	if err != nil {
		t.Fatal(err)
	}
	pred, err := extension.NewService(q, store, enc, extension.NewRuntime(nil, nil)).PredictorForToken(ctx, tenant.ID, token)
	if err != nil {
		t.Fatalf("the saved key no longer resolves after the replace: %v", err)
	}
	if pred.BatchSize() != 3 {
		t.Errorf("batch size = %d, want 3 from the 1.1.0 manifest", pred.BatchSize())
	}
	out, err := pred.Predict(ctx, "en", "de", extsdk.PredictMeta{}, []extsdk.PredictRow{
		{ID: "1", Source: "a", Target: "good"},
		{ID: "2", Source: "b", Target: "BAD"},
		{ID: "3", Source: "c", Target: "FIXME"},
	})
	if err != nil {
		t.Fatalf("predict on the replaced module: %v", err)
	}
	if len(out.Rows) != 3 {
		t.Fatalf("rows = %+v, want 3", out.Rows)
	}
}

// The icon is served to every member of the owning tenant, revalidated by
// ETag, cleared by a replacement zip without one, and hidden from other
// tenants and after delete.
func TestIntegrationExtensionIcon(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouterWithExtensions(pool, file.NewMemoryStore(), nil)
	tenant := testutil.SeedTenant(t, q, "ExtIconCo")
	other := testutil.SeedTenant(t, q, "ExtIconOther")
	owner := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@exticon.test", "pass", "owner")
	linguist := testutil.SeedUserWithRole(t, q, tenant.ID, "ling@exticon.test", "pass", "linguist")
	otherOwner := testutil.SeedUserWithRole(t, q, other.ID, "owner@exticonother.test", "pass", "owner")
	const iconURL = "/api/ai/extensions/mockqe/icon"

	icon := exttest.IconPNG(t, 32, 32, false)
	rec := testutil.DoFileUploadWithRole(t, router, "/api/ai/extensions", "mockqe.zip", string(exttest.ZipWithIcon(t, icon)), owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)

	rec = testutil.DoRequestWithRole(t, router, "GET", iconURL, nil, linguist.ID, tenant.ID, "linguist")
	testutil.AssertStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	if !bytes.Equal(rec.Body.Bytes(), icon) {
		t.Errorf("served %d bytes, want the uploaded %d-byte PNG", rec.Body.Len(), len(icon))
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}

	req := httptest.NewRequest("GET", iconURL, nil)
	req.Header.Set("Authorization", testutil.AuthHeaderWithRole(linguist.ID, tenant.ID, "linguist"))
	req.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	testutil.AssertStatus(t, rec, http.StatusNotModified)

	rec = testutil.DoRequestWithRole(t, router, "GET", iconURL, nil, otherOwner.ID, other.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	// A replacement without an icon clears it.
	rec = testutil.DoFileUploadWithRole(t, router, "/api/ai/extensions", "mockqe.zip", string(exttest.Zip(t, nil)), owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	rec = testutil.DoRequestWithRole(t, router, "GET", iconURL, nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)

	rec = testutil.DoFileUploadWithRole(t, router, "/api/ai/extensions", "mockqe.zip", string(exttest.ZipWithIcon(t, icon)), owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusCreated)
	rec = testutil.DoRequestWithRole(t, router, "DELETE", "/api/ai/extensions/mockqe", nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNoContent)
	rec = testutil.DoRequestWithRole(t, router, "GET", iconURL, nil, owner.ID, tenant.ID, "owner")
	testutil.AssertStatus(t, rec, http.StatusNotFound)
}
