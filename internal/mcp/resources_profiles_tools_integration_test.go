package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type tmProfileOut struct {
	TMProfileID    int64   `json:"tm_profile_id"`
	Name           string  `json:"name"`
	FuzzyThreshold float32 `json:"fuzzy_threshold"`
	TMs            []struct {
		TMID int64  `json:"tm_id"`
		Mode string `json:"mode"`
	} `json:"tms"`
	UsageCount int32 `json:"usage_count"`
}

func TestIntegrationMCPTMProfileLifecycle(t *testing.T) {
	rf := newResourcesFixture(t)
	pm := connect(t, rf.url, writeToken(t, rf.fixture, rf.tenantA.ID, rf.pmA.ID))
	owner := connect(t, rf.url, writeToken(t, rf.fixture, rf.tenantA.ID, rf.ownerA.ID))
	tm1 := testutil.SeedTMWithLangs(t, rf.q, rf.tenantA.ID, "Main", "en", []string{"de"})
	tm2 := testutil.SeedTMWithLangs(t, rf.q, rf.tenantA.ID, "Legacy", "en", []string{"de"})
	foreignTM := testutil.SeedTMWithLangs(t, rf.q, rf.tenantB.ID, "Foreign", "en", []string{"de"})

	var created tmProfileOut
	if isErr, text := callTool(t, pm, "openloc_create_tm_profile", map[string]any{
		"name": "Legal bundle", "fuzzy_threshold": 0.8,
		"tms": []map[string]any{{"tm_id": tm1.ID, "mode": "read_write"}},
	}, &created); isErr {
		t.Fatalf("create_tm_profile: %s", text)
	}
	if created.Name != "Legal bundle" || created.FuzzyThreshold < 0.79 || len(created.TMs) != 1 || created.TMs[0].TMID != tm1.ID {
		t.Fatalf("create_tm_profile = %+v", created)
	}
	// A TM of another tenant cannot be bundled.
	if isErr, text := callTool(t, pm, "openloc_create_tm_profile", map[string]any{
		"name": "Sneaky", "tms": []map[string]any{{"tm_id": foreignTM.ID}},
	}, nil); !isErr || !strings.Contains(text, "not found") {
		t.Errorf("a foreign TM in a profile should be refused, got isErr=%v %q", isErr, text)
	}

	// A project follows the profile; the edit preview names it and changes nothing.
	testutil.LinkTMProfile(t, rf.q, rf.projectA.SettingsProfileID, created.TMProfileID)
	args := map[string]any{
		"tm_profile_id": created.TMProfileID, "name": "Legal bundle v2",
		"tms": []map[string]any{{"tm_id": tm1.ID, "mode": "read"}, {"tm_id": tm2.ID}},
	}
	var g guardedOut
	if isErr, text := callTool(t, pm, "openloc_update_tm_profile", args, &g); isErr {
		t.Fatalf("update preview: %s", text)
	}
	if g.Status != "needs_confirmation" || len(g.Preview.Effects) != 1 || !strings.Contains(g.Preview.Effects[0], "1 project(s)") {
		t.Fatalf("preview should name the following project, got %+v", g.Preview)
	}
	var list struct {
		Profiles []tmProfileOut `json:"profiles"`
	}
	callTool(t, pm, "openloc_list_tm_profiles", map[string]any{}, &list)
	if len(list.Profiles) != 1 || list.Profiles[0].Name != "Legal bundle" || len(list.Profiles[0].TMs) != 1 || list.Profiles[0].UsageCount != 1 {
		t.Fatalf("the preview changed the profile: %+v", list.Profiles)
	}
	// The preview validates like the change: a duplicate name is refused up front.
	testutil.SeedTMProfile(t, rf.q, rf.tenantA.ID, "Taken", 0.75)
	if isErr, text := callTool(t, pm, "openloc_update_tm_profile", map[string]any{
		"tm_profile_id": created.TMProfileID, "name": "Taken",
	}, nil); !isErr || !strings.Contains(text, "already taken") {
		t.Errorf("duplicate name should be refused at preview, got isErr=%v %q", isErr, text)
	}

	var updated tmProfileOut
	if isErr, text := confirmedCall(t, pm, "openloc_update_tm_profile", args, &updated); isErr {
		t.Fatalf("update confirm: %s", text)
	}
	if updated.Name != "Legal bundle v2" || len(updated.TMs) != 2 {
		t.Fatalf("after update = %+v", updated)
	}
	rows, err := rf.q.ListSettingsTMAssignments(context.Background(), mustProfile(t, rf, created.TMProfileID).SettingsProfileID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("TM rows after update = %+v, %v", rows, err)
	}

	// Delete: owner/admin only, and refused while a project follows it.
	if isErr, text := callTool(t, pm, "openloc_delete_tm_profile", map[string]any{"tm_profile_id": created.TMProfileID}, nil); !isErr || !strings.Contains(text, "owner or admin") {
		t.Errorf("PM delete_tm_profile should be refused, got isErr=%v %q", isErr, text)
	}
	if isErr, text := callTool(t, owner, "openloc_delete_tm_profile", map[string]any{"tm_profile_id": created.TMProfileID}, nil); !isErr || !strings.Contains(text, "used by 1 project") {
		t.Errorf("deleting a followed profile should be refused, got isErr=%v %q", isErr, text)
	}
	if _, err := rf.q.SetSettingsProfileTMProfile(context.Background(), db.SetSettingsProfileTMProfileParams{ID: rf.projectA.SettingsProfileID}); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if isErr, text := confirmedCall(t, owner, "openloc_delete_tm_profile", map[string]any{"tm_profile_id": created.TMProfileID}, nil); isErr {
		t.Fatalf("delete confirm: %s", text)
	}
	var deletedAt *time.Time
	if err := rf.pool.QueryRow(context.Background(), `SELECT deleted_at FROM tm_profiles WHERE id = $1`, created.TMProfileID).Scan(&deletedAt); err != nil || deletedAt == nil {
		t.Errorf("profile should be soft-deleted, got deleted_at=%v err=%v", deletedAt, err)
	}
	if _, err := rf.q.GetTMCollection(context.Background(), db.GetTMCollectionParams{ID: tm1.ID, TenantID: rf.tenantA.ID}); err != nil {
		t.Errorf("deleting the profile removed its TM: %v", err)
	}
}

func mustProfile(t *testing.T, rf *resourcesFixture, id int64) db.TmProfile {
	t.Helper()
	p, err := rf.q.GetTMProfile(context.Background(), db.GetTMProfileParams{ID: id, TenantID: rf.tenantA.ID})
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	return p
}
