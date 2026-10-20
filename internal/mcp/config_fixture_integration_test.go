package mcp_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// Configuration tools (register_config.go) over the real protocol: MT engines,
// AI routers and engines, MT routers, the step and workflow library, and the
// prompt preview. Previews change nothing and name their impact, confirms
// execute, and the owner/admin and write-access gates refuse. Another tenant's
// id is checked only where a test makes that tool call from asOwnerB.

// configFixture is the standard two-tenant setup plus the pool (to read
// deleted_at, which no live query returns) and sessions per role.
type configFixture struct {
	*fixture
	pool       *pgxpool.Pool
	pm         db.User
	asOwner    *sdk.ClientSession
	asManager  *sdk.ClientSession
	asOwnerB   *sdk.ClientSession
	asReadOnly *sdk.ClientSession
}

func newConfigFixture(t *testing.T) *configFixture {
	t.Helper()
	pool, q, cleanup := testutil.SetupTestDB(t)
	t.Cleanup(cleanup)
	srv := httptest.NewServer(testutil.NewTestRouter(pool))
	t.Cleanup(srv.Close)

	f := &fixture{q: q, url: srv.URL}
	f.tenantA = testutil.SeedTenant(t, q, "CfgTenantA")
	f.tenantB = testutil.SeedTenant(t, q, "CfgTenantB")
	f.ownerA = testutil.SeedUserWithRole(t, q, f.tenantA.ID, "cfg-a@test.com", "pass", "owner")
	f.linguistA = testutil.SeedUserWithRole(t, q, f.tenantA.ID, "cfg-l@test.com", "pass", "linguist")
	f.ownerB = testutil.SeedUserWithRole(t, q, f.tenantB.ID, "cfg-b@test.com", "pass", "owner")
	f.projectA = testutil.SeedProject(t, q, f.tenantA.ID, "Cfg Project", "en", []string{"de"})

	c := &configFixture{fixture: f, pool: pool}
	c.pm = testutil.SeedUserWithRole(t, q, f.tenantA.ID, "cfg-pm@test.com", "pass", "project_manager")
	c.asOwner = connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	c.asManager = connect(t, f.url, writeToken(t, f, f.tenantA.ID, c.pm.ID))
	c.asOwnerB = connect(t, f.url, writeToken(t, f, f.tenantB.ID, f.ownerB.ID))
	c.asReadOnly = connect(t, f.url, testutil.SeedAPIToken(t, q, f.tenantA.ID, f.ownerA.ID, auth.CapabilityRead, time.Hour))
	return c
}

// refresh gives each role a new token. The MCP endpoint rate-limits per token
// (burst 20), so a long scenario starts a new phase with fresh sessions.
func (c *configFixture) refresh(t *testing.T) {
	t.Helper()
	c.asOwner = connect(t, c.url, writeToken(t, c.fixture, c.tenantA.ID, c.ownerA.ID))
	c.asManager = connect(t, c.url, writeToken(t, c.fixture, c.tenantA.ID, c.pm.ID))
	c.asOwnerB = connect(t, c.url, writeToken(t, c.fixture, c.tenantB.ID, c.ownerB.ID))
}

// softDeleted reports whether a row still exists and carries deleted_at.
func (c *configFixture) softDeleted(t *testing.T, table string, id int64) bool {
	t.Helper()
	var deleted bool
	err := c.pool.QueryRow(context.Background(),
		"SELECT deleted_at IS NOT NULL FROM "+table+" WHERE id = $1", id).Scan(&deleted)
	if err != nil {
		t.Fatalf("read %s %d: %v", table, id, err)
	}
	return deleted
}

// cfgPreviewOnly runs the first call of a guarded tool and returns its preview.
func cfgPreviewOnly(t *testing.T, s *sdk.ClientSession, tool string, args map[string]any) guardedOut {
	t.Helper()
	var g guardedOut
	if isErr, text := callTool(t, s, tool, args, &g); isErr {
		t.Fatalf("%s preview: %s", tool, text)
	}
	if g.Status != "needs_confirmation" || g.Preview == nil || g.ConfirmToken == "" {
		t.Fatalf("%s: want a preview with a token, got %+v", tool, g)
	}
	return g
}

func cfgEffectsContain(g guardedOut, sub string) bool {
	for _, e := range g.Preview.Effects {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}

// cfgMustRefuse asserts a call is a tool error whose text contains want.
func cfgMustRefuse(t *testing.T, s *sdk.ClientSession, tool string, args map[string]any, want string) {
	t.Helper()
	isErr, text := callTool(t, s, tool, args, nil)
	if !isErr {
		t.Fatalf("%s was accepted, want a refusal containing %q", tool, want)
	}
	if !strings.Contains(text, want) {
		t.Errorf("%s refusal = %q, want it to contain %q", tool, text, want)
	}
}

// cfgMustNotFind asserts another tenant's id reads as not found, for the preview
// of a guarded tool as much as for a plain read.
func cfgMustNotFind(t *testing.T, s *sdk.ClientSession, tool string, args map[string]any) {
	t.Helper()
	isErr, text := callTool(t, s, tool, args, nil)
	if !isErr {
		t.Fatalf("%s reached another tenant's data", tool)
	}
	assertOpaqueNotFound(t, text)
}
