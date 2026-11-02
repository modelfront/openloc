package mcp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"openloc/internal/file"
	"openloc/internal/mcp"
	"openloc/internal/testutil"
)

type attachedFilesOut struct {
	Files []struct {
		UploadID   string   `json:"upload_id"`
		File       *fileOut `json:"file"`
		Warnings   []string `json:"warnings"`
		Error      string   `json:"error"`
		NotStarted bool     `json:"not_started"`
	} `json:"files"`
	Added int  `json:"added"`
	Draft bool `json:"draft"`
}

func filesArg(ids ...string) []map[string]any {
	out := make([]map[string]any, len(ids))
	for i, id := range ids {
		out[i] = map[string]any{"upload_id": id}
	}
	return out
}

func projectFileNames(t *testing.T, f *fixture, projectID int64) []string {
	t.Helper()
	files, err := f.q.ListFilesByProject(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(files))
	for i, pf := range files {
		names[i] = pf.Name
	}
	return names
}

func TestIntegrationMCPAttachUploadedFiles(t *testing.T) {
	store := file.NewMemoryStore()
	f := newFixtureWithStore(t, store)
	s := connect(t, f.url, writeToken(t, f, f.tenantA.ID, f.ownerA.ID))
	stage := func(name, body string) string {
		return stageUpload(t, f, store, f.tenantA.ID, f.ownerA.ID, name, []byte(body))
	}

	t.Run("adds every file in one call", func(t *testing.T) {
		one, two := stage("one.txt", "First\n"), stage("two.txt", "Second\nThird\n")
		var out attachedFilesOut
		if isErr, text := callTool(t, s, "openloc_attach_uploaded_files", map[string]any{
			"project_id": f.projectA.ID, "files": filesArg(one, two),
		}, &out); isErr {
			t.Fatalf("attach_uploaded_files: %s", text)
		}
		if out.Added != 2 || out.Draft || len(out.Files) != 2 {
			t.Fatalf("result: %+v", out)
		}
		for i, want := range []struct {
			id, name string
			segs     int32
		}{{one, "one.txt", 1}, {two, "two.txt", 2}} {
			row := out.Files[i]
			if row.UploadID != want.id || row.Error != "" || row.File == nil || row.File.Name != want.name || row.File.SegmentCount != want.segs || row.File.ProjectID != f.projectA.ID {
				t.Errorf("row %d = %+v, want %s with %d segments", i, row, want.name, want.segs)
			}
		}
		names := strings.Join(projectFileNames(t, f, f.projectA.ID), ",")
		if !strings.Contains(names, "one.txt") || !strings.Contains(names, "two.txt") {
			t.Fatalf("project files = %s", names)
		}
	})

	t.Run("a failing file leaves the others added", func(t *testing.T) {
		good := stage("good.txt", "Fine\n")
		png := stage("picture.png", "\x89PNG")
		colleague := testutil.SeedUserWithRole(t, f.q, f.tenantA.ID, "batch-colleague@test.com", "pass", "owner")
		theirs := stageUpload(t, f, store, f.tenantA.ID, colleague.ID, "theirs.txt", []byte("Nope\n"))
		otherTenant := stageUpload(t, f, store, f.tenantB.ID, f.ownerB.ID, "other-tenant.txt", []byte("Nope\n"))
		madeUp := uuid.NewString()

		var out attachedFilesOut
		if isErr, text := callTool(t, s, "openloc_attach_uploaded_files", map[string]any{
			"project_id": f.projectA.ID, "files": filesArg(theirs, good, png, otherTenant, madeUp),
		}, &out); isErr {
			t.Fatalf("attach_uploaded_files: %s", text)
		}
		if out.Added != 1 || len(out.Files) != 5 {
			t.Fatalf("result: %+v", out)
		}
		if row := out.Files[1]; row.File == nil || row.File.Name != "good.txt" || row.Error != "" {
			t.Errorf("good row = %+v", row)
		}
		if row := out.Files[2]; row.File != nil || !strings.Contains(row.Error, "unsupported file format") {
			t.Errorf("png row = %+v", row)
		}
		for _, i := range []int{0, 3, 4} {
			row := out.Files[i]
			if row.File != nil {
				t.Errorf("row %d was added: %+v", i, row)
			}
			assertOpaqueNotFound(t, row.Error)
		}
		names := projectFileNames(t, f, f.projectA.ID)
		for _, n := range names {
			if n == "theirs.txt" || n == "other-tenant.txt" || n == "picture.png" {
				t.Errorf("%s reached the project", n)
			}
		}
	})

	t.Run("refuses the call when no file is added", func(t *testing.T) {
		isErr, text := callTool(t, s, "openloc_attach_uploaded_files", map[string]any{
			"project_id": f.projectA.ID, "files": filesArg(uuid.NewString(), stage("bad.png", "\x89PNG")),
		}, nil)
		if !isErr || !strings.Contains(text, "no file was added") || !strings.Contains(text, "unsupported file format") {
			t.Errorf("all-failed call: %v %s", isErr, text)
		}
	})

	t.Run("refuses bad arguments before adding anything", func(t *testing.T) {
		before := len(projectFileNames(t, f, f.projectA.ID))
		eleven := make([]string, 11)
		for i := range eleven {
			eleven[i] = stage("many.txt", "Many\n")
		}
		dup := stage("dup.txt", "Dup\n")
		for name, files := range map[string][]map[string]any{
			"none":      {},
			"eleven":    filesArg(eleven...),
			"duplicate": filesArg(dup, dup),
		} {
			isErr, text := callTool(t, s, "openloc_attach_uploaded_files", map[string]any{"project_id": f.projectA.ID, "files": files}, nil)
			if !isErr || !(strings.Contains(text, "1 to 10") || strings.Contains(text, "listed twice")) {
				t.Errorf("%s: %v %s", name, isErr, text)
			}
		}
		if after := len(projectFileNames(t, f, f.projectA.ID)); after != before {
			t.Errorf("a refused call added files: %d -> %d", before, after)
		}
	})

	t.Run("refuses another tenant's project and a read-only token", func(t *testing.T) {
		up := stage("guarded.txt", "Guarded\n")
		isErr, text := callTool(t, s, "openloc_attach_uploaded_files", map[string]any{"project_id": f.projectB.ID, "files": filesArg(up)}, nil)
		if !isErr {
			t.Fatal("attached to another tenant's project")
		}
		assertOpaqueNotFound(t, text)
		if isErr, _ := callTool(t, connect(t, f.url, f.tokenA), "openloc_attach_uploaded_files", map[string]any{"project_id": f.projectA.ID, "files": filesArg(up)}, nil); !isErr {
			t.Error("a read-only token attached files")
		}
		for _, n := range projectFileNames(t, f, f.projectB.ID) {
			if n == "guarded.txt" {
				t.Error("the file reached tenant B's project")
			}
		}
	})

	t.Run("a draft gets its files and no jobs", func(t *testing.T) {
		draft := seedDraftProject(t, f.q, f.tenantA.ID, "Draft with files")
		var out attachedFilesOut
		if isErr, text := callTool(t, s, "openloc_attach_uploaded_files", map[string]any{
			"project_id": draft.ID, "files": filesArg(stage("d1.txt", "One\n"), stage("d2.txt", "Two\n")),
		}, &out); isErr || !out.Draft || out.Added != 2 {
			t.Fatalf("attach to draft: %v %s %+v", isErr, text, out)
		}
		jobs, err := f.q.ListJobsByProject(context.Background(), draft.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(jobs) != 0 {
			t.Fatalf("a draft got jobs before publishing: %+v", jobs)
		}
	})

	t.Run("stops starting files when the time budget is spent", func(t *testing.T) {
		defer mcp.SetAttachBatchBudgetForTest(-1)()
		first, second, third := stage("first.txt", "One\n"), stage("second.txt", "Two\n"), stage("third.txt", "Three\n")
		var out attachedFilesOut
		if isErr, text := callTool(t, s, "openloc_attach_uploaded_files", map[string]any{
			"project_id": f.projectA.ID, "files": filesArg(first, second, third),
		}, &out); isErr {
			t.Fatalf("attach_uploaded_files: %s", text)
		}
		if out.Added != 1 || len(out.Files) != 3 || out.Files[0].File == nil || out.Files[0].NotStarted {
			t.Fatalf("result: %+v", out)
		}
		for _, row := range out.Files[1:] {
			if !row.NotStarted || row.File != nil || !strings.Contains(row.Error, "new message") {
				t.Errorf("row %s = %+v, want not started", row.UploadID, row)
			}
		}
		for _, n := range projectFileNames(t, f, f.projectA.ID) {
			if n == "second.txt" || n == "third.txt" {
				t.Errorf("%s was added after the budget ran out", n)
			}
		}

		// The first file failing leaves no file added: the error names the others too.
		isErr, text := callTool(t, s, "openloc_attach_uploaded_files", map[string]any{
			"project_id": f.projectA.ID, "files": filesArg(uuid.NewString(), second),
		}, nil)
		if !isErr || !strings.Contains(text, second+": not started") {
			t.Errorf("all-failed call with a row not started: %v %s", isErr, text)
		}
	})
}
