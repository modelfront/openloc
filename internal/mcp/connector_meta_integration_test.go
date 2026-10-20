package mcp_test

// What the ChatGPT and Claude connector directories read off the protocol:
// per-tool security schemes, the step-up challenge a read-scoped grant gets
// from a write tool, and the profile tool that tells two accounts apart.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"openloc/internal/auth"
	"openloc/internal/mcp"
	"openloc/internal/testutil"
)

const profileMetaKey = "openai/profile"

func TestIntegrationMCPToolMetaForConnectors(t *testing.T) {
	f := newFixture(t)
	res, err := connect(t, f.url, f.tokenA).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	profileTools := 0
	for _, tool := range res.Tools {
		// The scheme's scope must be the one the write gate enforces, so a
		// client asks for exactly the access the tool needs. A write allowed
		// in read-only mode (reporting an issue) passes the gate with read.
		want := `[{"scopes":["write"],"type":"oauth2"}]`
		if mcp.AllowedInReadOnly(tool) {
			want = `[{"scopes":["read"],"type":"oauth2"}]`
		}
		got, err := json.Marshal(tool.Meta["securitySchemes"])
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s securitySchemes = %s, want %s", tool.Name, got, want)
		}
		if tool.Meta[profileMetaKey] == true {
			profileTools++
			if tool.Name != "openloc_get_me" {
				t.Errorf("%s is marked as the profile tool", tool.Name)
			}
		}
	}
	if profileTools != 1 {
		t.Fatalf("want exactly one profile tool, got %d", profileTools)
	}
}

// stepUpChallenge returns the mcp/www_authenticate entries of a tool result.
func stepUpChallenge(t *testing.T, res *sdk.CallToolResult) []string {
	t.Helper()
	raw, ok := res.Meta["mcp/www_authenticate"]
	if !ok {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("mcp/www_authenticate is not a string list: %s", b)
	}
	return out
}

func markAllRead(t *testing.T, s *sdk.ClientSession) *sdk.CallToolResult {
	t.Helper()
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "openloc_mark_notifications_read", Arguments: map[string]any{"all": true},
	})
	if err != nil {
		t.Fatalf("CallTool protocol error: %v", err)
	}
	return res
}

func TestIntegrationMCPStepUpForReadScopedOAuthGrant(t *testing.T) {
	f := newFixture(t)

	// A read-scoped OAuth grant calling a write tool: refused, with the
	// challenge that lets the client re-authorize for write.
	readGrant := testutil.SeedOAuthToken(t, f.q, f.tenantA.ID, f.ownerA.ID, auth.CapabilityRead)
	res := markAllRead(t, connect(t, f.url, readGrant))
	if !res.IsError {
		t.Fatal("read-scoped grant ran a write tool")
	}
	ch := stepUpChallenge(t, res)
	if len(ch) != 1 {
		t.Fatalf("want one step-up challenge, got %v", ch)
	}
	for _, part := range []string{
		`Bearer resource_metadata="http://localhost:5173/.well-known/oauth-protected-resource"`,
		`error="insufficient_scope"`,
		`scope="write"`,
		`error_description="`,
	} {
		if !strings.Contains(ch[0], part) {
			t.Errorf("challenge %q lacks %s", ch[0], part)
		}
	}

	// A read-only PAT can't re-authorize, so it gets the plain refusal.
	res = markAllRead(t, connect(t, f.url, f.tokenA))
	if !res.IsError {
		t.Fatal("read-only PAT ran a write tool")
	}
	if ch := stepUpChallenge(t, res); ch != nil {
		t.Fatalf("PAT refusal carries a step-up challenge: %v", ch)
	}

	// A write-scoped grant runs the tool.
	writeGrant := testutil.SeedOAuthToken(t, f.q, f.tenantA.ID, f.ownerA.ID, auth.CapabilityWrite)
	if res := markAllRead(t, connect(t, f.url, writeGrant)); res.IsError {
		t.Fatalf("write-scoped grant refused: %+v", res.Content)
	}
}

type profile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Nickname string `json:"nickname"`
}

func getProfile(t *testing.T, token, serverURL string) profile {
	t.Helper()
	var p profile
	if isErr, text := callTool(t, connect(t, serverURL, token), "openloc_get_me", map[string]any{}, &p); isErr {
		t.Fatalf("get_me: %s", text)
	}
	return p
}

func TestIntegrationMCPProfileIdentifiesAccountPerOrganization(t *testing.T) {
	f := newFixture(t)

	first := getProfile(t, f.tokenA, f.url)
	again := getProfile(t, testutil.SeedOAuthToken(t, f.q, f.tenantA.ID, f.ownerA.ID, auth.CapabilityRead), f.url)
	if !strings.HasPrefix(first.ID, "prf_") || first.ID != again.ID {
		t.Fatalf("profile id not stable across credentials: %q vs %q", first.ID, again.ID)
	}
	if first.Email != f.ownerA.Email || first.Nickname != f.ownerA.Name+" — "+f.tenantA.Name {
		t.Fatalf("profile: %+v", first)
	}

	// Another person in the same organization is another account.
	if other := getProfile(t, f.tokenLingA, f.url); other.ID == first.ID || other.ID == "" {
		t.Fatalf("two members share profile id %q", other.ID)
	}

	// The same person in a second organization is a second account: one
	// grant reaches one organization.
	testutil.SeedMembership(t, f.q, f.ownerA.ID, f.tenantB.ID, "owner")
	inB := getProfile(t, testutil.SeedAPIToken(t, f.q, f.tenantB.ID, f.ownerA.ID, auth.CapabilityRead, time.Hour), f.url)
	if inB.ID == first.ID || inB.ID == "" {
		t.Fatalf("same profile id across organizations: %q", inB.ID)
	}
}
