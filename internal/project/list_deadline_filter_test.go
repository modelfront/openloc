package project_test

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"openloc/internal/testutil"
)

// The project list's ?deadline= filter keeps the projects in any of the named
// deadline chip bands. A completed project is never overdue or due, so it
// shows up only under "none", and only without a deadline.
func TestIntegrationProjectListDeadlineFilter(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	router := testutil.NewTestRouter(pool)
	tenant := testutil.SeedTenant(t, q, "TestCorp")
	user := testutil.SeedUserWithRole(t, q, tenant.ID, "owner@test.com", "pass", "owner")
	other := testutil.SeedTenant(t, q, "OtherCorp")
	otherUser := testutil.SeedUserWithRole(t, q, other.ID, "other@test.com", "pass", "owner")

	// newProject creates a project and moves its deadline relative to now();
	// an empty offset leaves it without one. The deadline is set from the
	// DB's own clock, the one the bands are measured against.
	newProject := func(name, status, offset string, userID, tenantID int64) {
		t.Helper()
		id, _ := createProject(t, q, router, map[string]any{
			"name": name, "source_language": "en", "target_languages": []string{"de"}, "status": status,
		}, userID, tenantID)
		if offset == "" {
			return
		}
		if _, err := pool.Exec(context.Background(),
			`UPDATE projects SET deadline = now() + $1::interval WHERE id = $2`, offset, id); err != nil {
			t.Fatalf("set deadline of %s: %v", name, err)
		}
	}
	newProject("Overdue", "in_progress", "-2 hours", user.ID, tenant.ID)
	newProject("Overdue on hold", "on_hold", "-3 days", user.ID, tenant.ID)
	newProject("Done late", "completed", "-2 hours", user.ID, tenant.ID)
	newProject("Due in 3h", "in_progress", "3 hours", user.ID, tenant.ID)
	newProject("Due in 3d", "in_progress", "3 days", user.ID, tenant.ID)
	newProject("Due in 30d", "in_progress", "30 days", user.ID, tenant.ID)
	newProject("No deadline", "in_progress", "", user.ID, tenant.ID)
	newProject("Done, no deadline", "completed", "", user.ID, tenant.ID)
	newProject("Other tenant overdue", "in_progress", "-2 hours", otherUser.ID, other.ID)

	names := func(query string) []string {
		t.Helper()
		rec := testutil.DoRequest(t, router, "GET", "/api/projects?deadline="+query, nil, user.ID, tenant.ID)
		testutil.AssertStatus(t, rec, http.StatusOK)
		var page listResp
		testutil.DecodeJSON(t, rec, &page)
		out := make([]string, 0, len(page.Data))
		for _, p := range page.Data {
			out = append(out, p.Name)
		}
		slices.Sort(out)
		return out
	}

	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"overdue", []string{"Overdue", "Overdue on hold"}},
		{"approaching", []string{"Due in 3h"}},
		{"soon", []string{"Due in 3d"}},
		{"far", []string{"Due in 30d"}},
		{"none", []string{"Done, no deadline", "No deadline"}},
		// Several bands: the PM's daily "overdue or due today" view.
		{"overdue,approaching", []string{"Due in 3h", "Overdue", "Overdue on hold"}},
		{"overdue,%20none,overdue", []string{"Done, no deadline", "No deadline", "Overdue", "Overdue on hold"}},
	} {
		if got := names(tc.query); !slices.Equal(got, tc.want) {
			t.Errorf("deadline=%s: got %v, want %v", tc.query, got, tc.want)
		}
	}

	// The band combines with the other filters.
	rec := testutil.DoRequest(t, router, "GET", "/api/projects?deadline=overdue&status=on_hold", nil, user.ID, tenant.ID)
	testutil.AssertStatus(t, rec, http.StatusOK)
	var page listResp
	testutil.DecodeJSON(t, rec, &page)
	if len(page.Data) != 1 || page.Data[0].Name != "Overdue on hold" {
		t.Errorf("deadline=overdue&status=on_hold: got %+v", page.Data)
	}

	for _, bad := range []string{"garbage", "overdue,garbage", "done", "overdue,"} {
		rec := testutil.DoRequest(t, router, "GET", "/api/projects?deadline="+bad, nil, user.ID, tenant.ID)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("deadline=%s: got %d, want 400", bad, rec.Code)
		}
	}
}
