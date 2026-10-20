package mcp_test

import (
	"context"
	"encoding/json"
	mcppkg "openloc/internal/mcp"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"openloc/internal/auth"
	"openloc/internal/testutil"
)

// These tests pin the architectural contract the in-process connection rests on: the context
// passed to ConnectInProcess is the context tool handlers see. If an SDK
// upgrade ever stops propagating connect-context values into handler
// contexts, the chat would silently lose its identity — requireManager tools
// would deny and tenant scoping would collapse to tenant 0. That failure mode
// MUST surface here, not in production.

func inProcessServer(t *testing.T, f *fixture) *mcppkg.Server {
	t.Helper()
	s, err := mcppkg.NewServer(mcppkg.Deps{
		Backend: mcppkg.NewDirectBackend(f.q),
		Audit:   f.q,
		Version: "test",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s
}

func claimsCtx(tenantID, userID int64, role string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{
		TenantID: tenantID,
		UserID:   userID,
		Role:     role,
	})
}

// callInProcess calls one tool over an in-process session and returns
// (isError, combined text).
func callInProcess(t *testing.T, cs *sdk.ClientSession, tool string, args map[string]any) (bool, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", tool, err)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return res.IsError, sb.String()
}

func TestInProcessClaimsPropagation(t *testing.T) {
	f := newFixture(t)
	s := inProcessServer(t, f)

	// Tenant A's owner sees exactly tenant A's projects — proof that the
	// claims on the connect context reached the tool handler.
	cs, err := s.ConnectInProcess(claimsCtx(f.tenantA.ID, f.ownerA.ID, "owner"), "owner")
	if err != nil {
		t.Fatalf("ConnectInProcess: %v", err)
	}
	defer cs.Close()

	isErr, text := callInProcess(t, cs, "openloc_list_projects", map[string]any{})
	if isErr {
		t.Fatalf("list_projects errored: %s", text)
	}
	if !strings.Contains(text, "Alpha Project") {
		t.Fatalf("tenant A's project missing — claims did not propagate: %s", text)
	}
	if strings.Contains(text, "Bravo Project") {
		t.Fatalf("tenant B's project leaked into tenant A's in-process session: %s", text)
	}
}

func TestInProcessCrossTenantIsolation(t *testing.T) {
	f := newFixture(t)
	s := inProcessServer(t, f)

	// Tenant B's session pointed at tenant A's job must get the opaque
	// not-found, same as over HTTP.
	cs, err := s.ConnectInProcess(claimsCtx(f.tenantB.ID, f.ownerB.ID, "owner"), "owner")
	if err != nil {
		t.Fatalf("ConnectInProcess: %v", err)
	}
	defer cs.Close()

	isErr, text := callInProcess(t, cs, "openloc_get_job", map[string]any{"job_id": f.jobA.ID})
	if !isErr {
		t.Fatalf("in-process session leaked tenant A's job to tenant B: %s", text)
	}
	if !strings.Contains(text, "not found") {
		t.Fatalf("expected opaque not-found, got: %s", text)
	}
}

func TestInProcessRoleTier(t *testing.T) {
	f := newFixture(t)
	s := inProcessServer(t, f)

	// A linguist session must get the linguist server: enumeration tools are
	// absent from tools/list entirely, not merely denied.
	cs, err := s.ConnectInProcess(claimsCtx(f.tenantA.ID, f.linguistA.ID, "linguist"), "linguist")
	if err != nil {
		t.Fatalf("ConnectInProcess: %v", err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "openloc_list_projects" {
			t.Fatal("linguist in-process session exposes manager-only openloc_list_projects")
		}
	}
}

func TestInProcessWritesDeniedWithoutPAT(t *testing.T) {
	f := newFixture(t)
	s := inProcessServer(t, f)

	// A browser-JWT context with no PATInfo and no write grant must
	// fail requireWrite, even for an owner.
	cs, err := s.ConnectInProcess(claimsCtx(f.tenantA.ID, f.ownerA.ID, "owner"), "owner")
	if err != nil {
		t.Fatalf("ConnectInProcess: %v", err)
	}
	defer cs.Close()

	isErr, text := callInProcess(t, cs, "openloc_add_comment", map[string]any{
		"translation_id": 1, "content": "should never land",
	})
	if !isErr {
		t.Fatal("write tool executed from an in-process session without write capability")
	}
	if !strings.Contains(text, "write access") {
		t.Fatalf("expected read-only denial, got: %s", text)
	}
}

// Guards the schema contract chat relies on when translating tools for the
// LLM: every tool must have an object input schema with marshalable JSON.
func TestInProcessToolSchemasMarshal(t *testing.T) {
	f := newFixture(t)
	s := inProcessServer(t, f)

	cs, err := s.ConnectInProcess(claimsCtx(f.tenantA.ID, f.ownerA.ID, "owner"), "owner")
	if err != nil {
		t.Fatalf("ConnectInProcess: %v", err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("no tools listed in-process")
	}
	for _, tool := range tools.Tools {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("tool %s: input schema does not marshal: %v", tool.Name, err)
		}
		var schema struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("tool %s: input schema not an object: %v", tool.Name, err)
		}
		if schema.Type != "object" {
			t.Fatalf("tool %s: input schema type = %q, want object", tool.Name, schema.Type)
		}
	}
}

var _ = testutil.SetupTestDB // keep import when helpers move

// A context carrying an explicit write grant (set by the chat's
// confirm executor after a human clicked Allow) may execute write tools.
// Paired with TestInProcessWritesDeniedWithoutPAT above, this pins BOTH
// directions of the gate.
func TestInProcessWriteGrantExecutesWrites(t *testing.T) {
	f := newFixture(t)
	s := inProcessServer(t, f)

	ctx := auth.WithWriteGrant(claimsCtx(f.tenantA.ID, f.ownerA.ID, "owner"))
	cs, err := s.ConnectInProcess(ctx, "owner")
	if err != nil {
		t.Fatalf("ConnectInProcess: %v", err)
	}
	defer cs.Close()

	// Need a real translation id to comment on.
	segs := struct {
		Segments []struct {
			TranslationID int64 `json:"translation_id"`
		} `json:"segments"`
	}{}
	if isErr, text := callInProcess(t, cs, "openloc_list_segments", map[string]any{"job_id": f.jobA.ID}); isErr {
		t.Fatalf("list_segments: %s", text)
	} else if err := json.Unmarshal([]byte(text), &segs); err != nil || len(segs.Segments) == 0 {
		t.Fatalf("no segments to write to: %v %s", err, text)
	}

	isErr, text := callInProcess(t, cs, "openloc_add_comment", map[string]any{
		"translation_id": segs.Segments[0].TranslationID,
		"content":        "confirmed by a human (confirm-grant test)",
	})
	if isErr {
		t.Fatalf("granted write denied: %s", text)
	}

	// And the annotations advertise which tools need this: writes are marked
	// non-read-only, reads read-only — the chat's detection contract.
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range tools.Tools {
		if tool.Annotations == nil {
			t.Fatalf("tool %s missing annotations", tool.Name)
		}
		// Write-ness is inferred from the tool's verb because ListTools sees the
		// wire shape, not the toolSpec. Any NEW write verb must be added here —
		// otherwise this guard silently reclassifies a write as a read, which is
		// the failure it exists to catch.
		writeVerbs := []string{"add_", "lock_", "save_", "create_", "update_", "upsert_", "set_", "delete_", "run_", "ignore_", "assign_",
			"apply_", "publish_", "pretranslate_", "clear_", "rename_", "attach_", "reimport_", "import_",
			"change_", "invite_", "remove_", "revoke_", "mark_", "duplicate_",
			"advance_", "reject_", "rerun_", "find_replace_", "confirm_", "report_", "upload_"}
		isWrite := false
		for _, v := range writeVerbs {
			if strings.HasPrefix(tool.Name, "openloc_"+v) {
				isWrite = true
				break
			}
		}
		if tool.Annotations.ReadOnlyHint == isWrite {
			t.Fatalf("tool %s readOnlyHint=%v disagrees with its gate", tool.Name, tool.Annotations.ReadOnlyHint)
		}
	}
}
