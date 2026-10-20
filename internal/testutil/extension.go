package testutil

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/api"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/extension"
	"openloc/internal/extension/exttest"
	"openloc/internal/file"
	"openloc/internal/secrets"
	"openloc/pkg/extsdk"
)

// MockExtensionID is the id of the mock QE+APE test extension
// (internal/extension/testdata/mockqe). With no endpoint it approves every
// target, rejects one containing "BAD" and post-edits "FIXME" to "fixed";
// with an endpoint it forwards rows to {endpoint}/predict.
const MockExtensionID = "mockqe"

// NewTestRouterWithExtensions is a router whose extension routes and
// pipeline are live: uploads go to store, and extensions reach their vendors
// with client (nil = the runtime default). Pass an httptest TLS server's
// Client() to let an extension call that server.
func NewTestRouterWithExtensions(pool *pgxpool.Pool, store file.BlobStore, client *http.Client) chi.Router {
	deps := testDeps(pool, nil, "", TestAICatalog(pool), store)
	deps.ExtensionHTTPClient = client
	return api.NewRouter(deps)
}

// InstallMockExtension installs the mock test extension for a tenant into
// store, as an upload does.
func InstallMockExtension(t testing.TB, q db.Querier, store file.BlobStore, tenantID int64) extension.Installed {
	t.Helper()
	enc, err := secrets.NewEncryptor(TestAISecretsKey)
	if err != nil {
		t.Fatalf("InstallMockExtension encryptor: %v", err)
	}
	inst, err := extension.NewService(q, store, enc, extension.NewRuntime(nil, nil)).
		Install(context.Background(), tenantID, nil, exttest.Zip(t, nil))
	if err != nil {
		t.Fatalf("InstallMockExtension: %v", err)
	}
	return inst
}

// SeedExtension records an installed extension without a stored module:
// enough for validation and vendor lists, not for running it. With no
// capabilities given it can do both QE and APE.
func SeedExtension(t testing.TB, q db.Querier, tenantID int64, extID string, capabilities ...string) db.Extension {
	t.Helper()
	if len(capabilities) == 0 {
		capabilities = []string{extsdk.CapabilityQE, extsdk.CapabilityAPE}
	}
	manifest, err := json.Marshal(extsdk.Manifest{
		ID: extID, Name: extID, Version: "1.0.0", APIVersion: extsdk.APIVersion, Capabilities: capabilities,
		Settings: []extsdk.Setting{{Key: extsdk.SettingToken, Label: "API token", Secret: true}},
		Limits:   extsdk.Limits{BatchSize: 30}, Entry: "extension.wasm",
	})
	if err != nil {
		t.Fatal(err)
	}
	row, err := q.UpsertExtension(context.Background(), db.UpsertExtensionParams{
		TenantID: tenantID, ExtID: extID, Name: extID, Version: "1.0.0", ApiVersion: extsdk.APIVersion,
		Capabilities: capabilities, Manifest: manifest,
		StorageKey: "extensions/seeded/" + extID + ".zip", Sha256: "seeded",
	})
	if err != nil {
		t.Fatalf("SeedExtension(%q): %v", extID, err)
	}
	return row
}

// ConnectExtension stores an encrypted key for an extension's vendor on a
// connection with the given endpoint ("" = none) and returns the key's id —
// the id an extension engine row carries.
func ConnectExtension(t testing.TB, q db.Querier, tenantID int64, extID, baseURL string) int64 {
	t.Helper()
	enc, err := secrets.NewEncryptor(TestAISecretsKey)
	if err != nil {
		t.Fatalf("ConnectExtension encryptor: %v", err)
	}
	encKey, err := enc.Encrypt("test-key")
	if err != nil {
		t.Fatalf("ConnectExtension encrypt: %v", err)
	}
	return SeedAIVendorTokenBase(t, q, tenantID, extension.Provider(extID), "Default", encKey, baseURL).ID
}
