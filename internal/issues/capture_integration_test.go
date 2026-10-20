package issues_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"openloc/internal/testutil"
)

const (
	trailMarker     = "Export settings"
	recordingMarker = "typed while recording"
)

// The form sends the action trail and the recording with a report: the
// support team reads them on the issue; the reporter's org never gets them, in
// the issue or the list.
func TestIntegrationIssueTrail_ReachesTheSupportTeamOnly(t *testing.T) {
	f := newFixture(t)
	const title = "Export does nothing"
	fields := bugFields(title)
	fields["trail"] = `[{"at":"2026-10-16T10:00:00Z","kind":"page","path":"/projects/12"},` +
		`{"at":"2026-10-16T10:00:01Z","kind":"click","target":"` + trailMarker + `"},` +
		`{"at":"2026-10-16T10:00:02Z","kind":"api","method":"POST","path":"/api/projects/12/export","status":500}]`
	fields["recording"] = `{"started_at":"2026-10-16T10:01:00Z","ended_at":"2026-10-16T10:02:00Z","events":[` +
		`{"at":"2026-10-16T10:01:05Z","kind":"input","target":"Name","value":"` + recordingMarker + `"}]}`
	created := f.reportWith(t, fields)

	detail := f.platformGet(t, created.ID)
	var trail []map[string]any
	var recording struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(detail.Trail, &trail); err != nil {
		t.Fatalf("support team's trail: %v (%s)", err, detail.Trail)
	}
	if err := json.Unmarshal(detail.Recording, &recording); err != nil {
		t.Fatalf("support team's recording: %v (%s)", err, detail.Recording)
	}
	if len(trail) != 3 || trail[1]["target"] != trailMarker || trail[2]["status"] != float64(500) {
		t.Errorf("support team's trail = %v", trail)
	}
	if ev := recording.Events; len(ev) != 1 || ev[0]["value"] != recordingMarker {
		t.Errorf("support team's recording = %v", recording)
	}

	support := f.asOperator(t, http.MethodGet, "/api/platform/issues", nil)
	if support.Code != http.StatusOK || !strings.Contains(support.Body.String(), title) {
		t.Fatalf("support list: %d %s", support.Code, support.Body.String())
	}
	for name, body := range map[string]string{
		"issue":           f.orgRead(t, issuePath(created.ID, ""), f.reporter.ID, title),
		"manager's issue": f.orgRead(t, issuePath(created.ID, ""), f.colleague.ID, title),
		"list":            f.orgRead(t, "/api/issues", f.colleague.ID, title),
		"support list":    support.Body.String(),
	} {
		if strings.Contains(body, trailMarker) || strings.Contains(body, `"trail"`) ||
			strings.Contains(body, recordingMarker) || strings.Contains(body, `"recording"`) {
			t.Errorf("the %s carries the trail or the recording: %s", name, body)
		}
	}
}

// A report without a trail, or with nothing usable in it, stores none.
func TestIntegrationIssueTrail_NoneWithoutEvents(t *testing.T) {
	f := newFixture(t)
	fields := bugFields("No trail")
	fields["trail"] = `[{"kind":"keystroke","message":"typed"}]`
	created := f.reportWith(t, fields)
	var n int
	if err := f.pool.QueryRow(testutil.Ctx(), `SELECT count(*) FROM issue_captures`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d captures stored", n)
	}
	if d := f.platformGet(t, created.ID); len(d.Trail) != 0 || len(d.Recording) != 0 {
		t.Errorf("the support team's issue carries %s / %s", d.Trail, d.Recording)
	}
}
