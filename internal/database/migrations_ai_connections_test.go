package database_test

import (
	"testing"

	"github.com/pressly/goose/v3"
)

const (
	verBeforeDedupeConnections = int64(20261006143123)
	verDedupeConnections       = int64(20261007150511)
)

// TestMigrationDedupeEmptyAIConnections verifies migration 20261007150511: an
// empty connection (no keys) that is an exact copy of another connection on the
// same vendor — same name, same endpoint — is deleted, keeping the copy that
// has keys, else the oldest. Connections that differ in name or endpoint, that
// hold keys, or that are alone on their vendor are untouched, and nothing
// crosses vendors or tenants.
func TestMigrationDedupeEmptyAIConnections(t *testing.T) {
	sqldb := setupDB(t)
	dir := migrationsDir()
	f := &routerFixture{t: t, db: sqldb}

	if err := goose.UpTo(sqldb, dir, verBeforeDedupeConnections); err != nil {
		t.Fatalf("up to pre-dedupe migration: %v", err)
	}

	tenantA := f.scanID("insert tenant A", `INSERT INTO tenants (name, slug) VALUES ('Alpha','alpha') RETURNING id`)
	tenantB := f.scanID("insert tenant B", `INSERT INTO tenants (name, slug) VALUES ('Beta','beta') RETURNING id`)

	vendor := func(tenantID int64, provider string) int64 {
		return f.scanID("insert vendor "+provider,
			`INSERT INTO ai_vendors (tenant_id, provider) VALUES ($1,$2) RETURNING id`, tenantID, provider)
	}
	conn := func(vendorID int64, name, baseURL string) int64 {
		return f.scanID("insert connection "+name,
			`INSERT INTO ai_connections (vendor_id, name, base_url) VALUES ($1,$2,$3) RETURNING id`,
			vendorID, name, baseURL)
	}
	token := func(vendorID, connID int64, name string) {
		f.exec("insert token "+name,
			`INSERT INTO ai_vendor_tokens (vendor_id, connection_id, name, api_key_encrypted) VALUES ($1,$2,$3,'x')`,
			vendorID, connID, name)
	}

	// Three identical empty connections: the oldest survives.
	googleA := vendor(tenantA, "google")
	g1 := conn(googleA, "Google Translate", "")
	g2 := conn(googleA, "Google Translate", "")
	g3 := conn(googleA, "Google Translate", "")

	// An empty copy of a connection that has a key: the keyed one survives,
	// even though the empty one is older.
	claudeA := vendor(tenantA, "claude")
	cEmpty := conn(claudeA, "Claude", "")
	cKeyed := conn(claudeA, "Claude", "")
	token(claudeA, cKeyed, "Default")

	// Two endpoints on one vendor are two real connections.
	openaiA := vendor(tenantA, "openai")
	oPublic := conn(openaiA, "OpenAI", "")
	oAzure := conn(openaiA, "OpenAI", "https://acme.openai.azure.com/openai/v1")

	// Two user-chosen names are two real connections.
	deeplA := vendor(tenantA, "deepl")
	dFirst := conn(deeplA, "DeepL", "")
	dSecond := conn(deeplA, "DeepL Pro", "")

	// Identical connections that both hold keys are never deleted.
	extA := vendor(tenantA, "ext:acme")
	m1 := conn(extA, "Acme", "")
	m2 := conn(extA, "Acme", "")
	token(extA, m1, "Key one")
	token(extA, m2, "Key two")

	// Another tenant's lone empty connection is not a copy of tenant A's.
	googleB := vendor(tenantB, "google")
	gB := conn(googleB, "Google Translate", "")

	if err := goose.UpTo(sqldb, dir, verDedupeConnections); err != nil {
		t.Fatalf("run dedupe migration: %v", err)
	}

	exists := func(id int64) bool {
		t.Helper()
		var n int
		if err := sqldb.QueryRow(`SELECT count(*) FROM ai_connections WHERE id = $1`, id).Scan(&n); err != nil {
			t.Fatalf("lookup connection %d: %v", id, err)
		}
		return n == 1
	}
	for _, c := range []struct {
		name string
		id   int64
		want bool
	}{
		{"oldest of three empty copies", g1, true},
		{"second empty copy", g2, false},
		{"third empty copy", g3, false},
		{"empty copy of a keyed connection", cEmpty, false},
		{"keyed connection", cKeyed, true},
		{"public endpoint", oPublic, true},
		{"custom endpoint", oAzure, true},
		{"first name", dFirst, true},
		{"second name", dSecond, true},
		{"first keyed copy", m1, true},
		{"second keyed copy", m2, true},
		{"other tenant's lone connection", gB, true},
	} {
		if got := exists(c.id); got != c.want {
			t.Errorf("%s (id %d): exists = %v, want %v", c.name, c.id, got, c.want)
		}
	}
	f.count(3, `SELECT count(*) FROM ai_vendor_tokens`)

	if err := goose.DownTo(sqldb, dir, verBeforeDedupeConnections); err != nil {
		t.Fatalf("roll back dedupe migration: %v", err)
	}
}
