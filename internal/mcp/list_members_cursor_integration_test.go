package mcp_test

import (
	"encoding/base64"
	"fmt"
	"testing"

	"openloc/internal/testutil"
)

// The page after a cursor depends only on the (name, id) the cursor carries:
// naming another organization's user gives the same page as naming an unknown
// id, so the list cannot be used to probe users of other organizations. A
// cursor that is not a next_cursor is rejected.
func TestIntegrationMCPListMembers_CursorRevealsNothingOutside(t *testing.T) {
	f := newFixture(t)
	s := connect(t, f.url, f.tokenA)
	foreign := testutil.SeedUserWithRole(t, f.q, f.tenantB.ID, "foreign@test.com", "pass", "linguist")
	after := func(name string, id int64) string {
		return base64.RawURLEncoding.EncodeToString([]byte(name)) + "." + fmt.Sprint(id)
	}
	pages := map[string]memberPage{}
	for label, cursor := range map[string]string{
		"foreign user": after("foreign@test.com", foreign.ID),
		"unknown id":   after("foreign@test.com", 999999999),
	} {
		var out memberPage
		if isErr, text := callTool(t, s, "openloc_list_members", map[string]any{"cursor": cursor}, &out); isErr {
			t.Fatalf("%s: list_members: %s", label, text)
		}
		pages[label] = out
	}
	if a, b := pages["foreign user"], pages["unknown id"]; fmt.Sprint(a.Members) != fmt.Sprint(b.Members) {
		t.Errorf("a cursor naming a foreign user gave %v, an unknown id %v: want the same page", a.Members, b.Members)
	}
	for _, bad := range []string{"42", "!!.1", after("x", 0)} {
		var out memberPage
		if isErr, _ := callTool(t, s, "openloc_list_members", map[string]any{"cursor": bad}, &out); !isErr {
			t.Errorf("cursor %q was accepted, want an invalid-cursor error", bad)
		}
	}
}
