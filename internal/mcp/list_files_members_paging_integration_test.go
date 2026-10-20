package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

type filePage struct {
	Files []struct {
		ID       int64 `json:"id"`
		JobCount int   `json:"job_count"`
	} `json:"files"`
	Count      int   `json:"count"`
	NextCursor int64 `json:"next_cursor"`
	HasMore    bool  `json:"has_more"`
}

type memberPage struct {
	Members []struct {
		UserID int64  `json:"user_id"`
		Name   string `json:"name"`
	} `json:"members"`
	Count      int             `json:"count"`
	NextCursor json.RawMessage `json:"next_cursor"`
	HasMore    bool            `json:"has_more"`
}

// Every file of a project is reachable page by page, newest first, with its
// own job count, however many files the project has.
func TestIntegrationMCPListFiles_PagesReachEveryFile(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	const n = 250
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO files (project_id, name, format, storage_path)
		SELECT $1, 'bulk-' || g || '.txt', 'txt', 'test/bulk-' || g
		FROM generate_series(1, $2::int) g`, f.projectA.ID, n); err != nil {
		t.Fatal(err)
	}
	// One more job on the fixture's file, so a page must count per file.
	if _, err := f.q.CreateJob(ctx, db.CreateJobParams{FileID: f.jobA.FileID, TargetLanguage: "fr"}); err != nil {
		t.Fatal(err)
	}
	s := connect(t, f.url, f.tokenA)

	seen := map[int64]bool{}
	var order []int64
	cursor := int64(0)
	for page := 0; page < 10; page++ {
		args := map[string]any{"project_id": f.projectA.ID, "limit": 100}
		if cursor != 0 {
			args["cursor"] = cursor
		}
		var out filePage
		if isErr, text := callTool(t, s, "openloc_list_files", args, &out); isErr {
			t.Fatalf("list_files: %s", text)
		}
		if out.Count > 100 {
			t.Fatalf("page has %d files, want at most the limit 100", out.Count)
		}
		for _, file := range out.Files {
			if seen[file.ID] {
				t.Fatalf("file %d came back on two pages", file.ID)
			}
			seen[file.ID] = true
			order = append(order, file.ID)
			want := 0
			if file.ID == f.jobA.FileID {
				want = 2
			}
			if file.JobCount != want {
				t.Errorf("file %d job_count = %d, want %d", file.ID, file.JobCount, want)
			}
		}
		if !out.HasMore {
			break
		}
		cursor = out.NextCursor
	}
	if len(seen) != n+1 {
		t.Fatalf("reached %d files across pages, want all %d", len(seen), n+1)
	}
	for i := 1; i < len(order); i++ {
		if order[i] > order[i-1] {
			t.Fatalf("files are not newest first: %d after %d", order[i], order[i-1])
		}
	}
}

// Every active member is reachable page by page, in name order, even when
// the last member of a page leaves the organization before the next call.
func TestIntegrationMCPListMembers_PagesReachEveryMember(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	const n = 250
	for i := 0; i < n; i++ {
		testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, fmt.Sprintf("member%03d@test.com", i), "pass", "linguist")
	}
	s := connect(t, f.url, f.tokenA)

	seen := map[int64]bool{}
	var names []string
	var cursor json.RawMessage
	removed := int64(0)
	for page := 0; page < 10; page++ {
		args := map[string]any{"limit": 100}
		if cursor != nil {
			args["cursor"] = cursor
		}
		var out memberPage
		if isErr, text := callTool(t, s, "openloc_list_members", args, &out); isErr {
			t.Fatalf("list_members: %s", text)
		}
		if out.Count > 100 {
			t.Fatalf("page has %d members, want at most the limit 100", out.Count)
		}
		for _, m := range out.Members {
			if seen[m.UserID] {
				t.Fatalf("member %d came back on two pages", m.UserID)
			}
			seen[m.UserID] = true
			names = append(names, m.Name)
		}
		if !out.HasMore {
			break
		}
		if removed == 0 {
			removed = out.Members[len(out.Members)-1].UserID
			if _, err := f.pool.Exec(ctx, `DELETE FROM memberships WHERE user_id = $1 AND tenant_id = $2`, removed, f.tenantA.ID); err != nil {
				t.Fatal(err)
			}
		}
		cursor = out.NextCursor
	}
	if want := n + 2; len(seen) != want { // the bulk members, the owner and the linguist
		t.Fatalf("reached %d members across pages, want all %d", len(seen), want)
	}
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Fatalf("members are not in name order: %q after %q", names[i], names[i-1])
		}
	}
}
