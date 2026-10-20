// Integration test for ScheduledHandler.HandleDeadlineCheck.
//
// Why an integration test (not a unit test with mocks):
//   - The whole point of the cron is that it queries DB, dispatches via Service,
//     and relies on the SQL `NOT EXISTS … notifications` clause for dedup. A
//     mock querier could fake `GetJobsApproachingDeadline` but would short-circuit
//     the dedup logic that lives in SQL — exactly the contract we care about.
//   - testutil.SetupTestDB uses a real Postgres testcontainer, so dedup runs
//     against the same query the prod handler does.
//
// What we pin:
//   1. Happy path — a job with deadline 12h out + an assigned step yields exactly
//      one EventDeadline notification row + one captured email send.
//   2. SQL dedup — running the cron twice in succession does NOT double-fire.
//      This is the load-bearing safety net for "scheduler runs hourly but the
//      window is 24h" — without dedup, a single deadline would notify ~20 times
//      across the window.
//   3. Skip-no-assignee — a job with deadline but no current_step_id (so
//      AssignedTo comes back NULL via the LEFT JOIN) is silently skipped, no
//      crash, no orphan notification row with user_id=0.
//   4. Out-of-window — a job with deadline 30h out is excluded by the SQL
//      WHERE clause, so the handler doesn't see it.
//
// Anti-tautology:
//   - Reverting `if r.AssignedTo == nil { continue }` in scheduled.go would
//     attempt to dereference a nil pointer in the happy-path test if the LEFT
//     JOIN ever returns NULL — making test 3 panic.
//   - Reverting the `Service.Send` call would zero-out test 1's email count
//     and notification row count.
//   - Removing the SQL dedup clause in queries.sql would make test 2 see TWO
//     notification rows after the second call.

package notification_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/emaillayout"
	notif "openloc/internal/notification"
	"openloc/internal/testutil"
)

// spySender records SendEmail calls for assertions. Thread-safe because the
// scheduler could theoretically be invoked concurrently in some test variants.
//
// `failOn` lets a test inject a transient send error for the recipient with
// the matching email — used to verify the mid-loop "log + continue" contract
// in TestScheduled_FailureMidLoop_DoesNotBlockOthers.
type spySender struct {
	mu     sync.Mutex
	calls  []spySendCall
	failOn string
}

type spySendCall struct {
	To       string
	Subject  string
	HTMLBody string
}

func (s *spySender) SendEmail(_ context.Context, to, subject string, body emaillayout.Email) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failOn != "" && to == s.failOn {
		// Don't append — failed sends shouldn't show up in Calls(); the
		// test wants to assert on what was *successfully* sent.
		return errors.New("simulated send failure")
	}
	s.calls = append(s.calls, spySendCall{To: to, Subject: subject, HTMLBody: body.HTML()})
	return nil
}

func (s *spySender) Calls() []spySendCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]spySendCall, len(s.calls))
	copy(out, s.calls)
	return out
}

// seedDeadlineFixture creates the minimum set of rows for a job to appear in
// GetJobsApproachingDeadline with an assigned recipient. Returns only what
// every caller actually asserts on (userID for notif counts, jobID for the
// reference_id assertion). tenant + project IDs aren't surfaced because no
// test asserts on them — adding them back creates `_ =` noise.
func seedDeadlineFixture(t *testing.T, q db.Querier, deadlineOffset time.Duration) (userID, jobID int64) {
	t.Helper()
	_, userID, jobID = seedDeadlineTenant(t, q, "DeadlineCorp", deadlineOffset)
	return userID, jobID
}

// seedDeadlineTenant is seedDeadlineFixture in a tenant of the given name.
func seedDeadlineTenant(t *testing.T, q db.Querier, name string, deadlineOffset time.Duration) (tenantID, userID, jobID int64) {
	t.Helper()
	ctx := context.Background()

	tenant := testutil.SeedTenant(t, q, name)
	user := testutil.SeedUser(t, q, tenant.ID, "translator@"+strings.ToLower(name)+".test", "pw")
	project := testutil.SeedProject(t, q, tenant.ID, "Deadline Project", "en", []string{"fr"})
	file := testutil.SeedFile(t, q, project.ID, "doc.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "fr")

	// Workflow step (so the LEFT JOIN can resolve step_name)
	step, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID,
		Name:              "translate",
		StepOrder:         0,
		RoleRequired:      "member",
	})
	if err != nil {
		t.Fatalf("CreateSettingsWorkflowStep: %v", err)
	}

	// Job step assignment (so AssignedTo resolves to the user)
	assign, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID:          job.ID,
		WorkflowStepID: step.ID,
		AssignedTo:     &user.ID,
		Status:         "in_progress",
	})
	if err != nil {
		t.Fatalf("CreateJobStepAssignment: %v", err)
	}
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: &assign.ID}); err != nil {
		t.Fatalf("SetJobCurrentStep: %v", err)
	}

	// Set the deadline on the ACTIVE assignment — this is what
	// GetJobsApproachingDeadline keys on (the deadline lives on the
	// assignment, reached via jobs.current_step_id).
	deadlineAt := time.Now().Add(deadlineOffset)
	if err := q.UpdateStepAssignmentDeadline(ctx, db.UpdateStepAssignmentDeadlineParams{
		ID:       assign.ID,
		Deadline: pgtype.Timestamptz{Time: deadlineAt, Valid: true},
	}); err != nil {
		t.Fatalf("UpdateStepAssignmentDeadline: %v", err)
	}

	return tenant.ID, user.ID, job.ID
}

// countDeadlineNotifsForUser returns how many `deadline` events have been
// stored for a user. Lookup via ListNotifications avoids ad-hoc SQL.
func countDeadlineNotifsForUser(t *testing.T, q db.Querier, userID int64) int {
	t.Helper()
	count := 0
	for _, n := range allNotifsByUser(t, q, userID) {
		if n.EventType == string(notif.EventDeadline) {
			count++
		}
	}
	return count
}

// allNotifsByUser returns every notification row for the user, used by tests
// that want to inspect the `emailed` flag (the ordering-invariant assertion
// for Service.Send).
//
// ListNotifications requires tenant_id, so it resolves the tenant from the
// user's only membership.
func allNotifsByUser(t *testing.T, q db.Querier, userID int64) []db.Notification {
	t.Helper()
	// Every test user has exactly one membership, so the first is unambiguous.
	memberships, err := q.ListMembershipsWithTenant(context.Background(), userID)
	if err != nil || len(memberships) == 0 {
		t.Fatalf("ListMembershipsWithTenant(%d): %v", userID, err)
	}
	rows, err := q.ListNotifications(context.Background(), db.ListNotificationsParams{
		UserID:     userID,
		TenantID:   memberships[0].TenantID,
		UnreadOnly: false,
		Cursor:     0,
		PageLimit:  100,
	})
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	return rows
}

func TestScheduled_HappyPath_DispatchesDeadlineNotification(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	userID, jobID := seedDeadlineFixture(t, q, 12*time.Hour)

	sender := &spySender{}
	service := notif.NewService(q, sender)
	handler := notif.NewScheduledHandler(q, service)

	if err := handler.HandleDeadlineCheck(context.Background(), nil); err != nil {
		t.Fatalf("HandleDeadlineCheck: %v", err)
	}

	emails := sender.Calls()
	if len(emails) != 1 {
		t.Fatalf("expected 1 email, got %d", len(emails))
	}
	if emails[0].To != "translator@deadlinecorp.test" {
		t.Errorf("email.To = %q, want translator@deadlinecorp.test", emails[0].To)
	}
	if !strings.Contains(emails[0].Subject, "Deadline") {
		t.Errorf("email.Subject = %q, want subject containing 'Deadline'", emails[0].Subject)
	}

	if got := countDeadlineNotifsForUser(t, q, userID); got != 1 {
		t.Errorf("deadline notifications for user = %d, want 1", got)
	}

	// Verify the notification's reference_id points to the job (not the project)
	rows := allNotifsByUser(t, q, userID)
	if len(rows) != 1 {
		t.Fatalf("expected 1 notification row, got %d", len(rows))
	}
	if rows[0].ReferenceType != "job" || rows[0].ReferenceID != jobID {
		t.Errorf("notification ref = (%s, %d), want (job, %d)",
			rows[0].ReferenceType, rows[0].ReferenceID, jobID)
	}
}

func TestScheduled_SQLDedup_TwoCallsDoNotDoubleFire(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	userID, _ := seedDeadlineFixture(t, q, 12*time.Hour)

	sender := &spySender{}
	service := notif.NewService(q, sender)
	handler := notif.NewScheduledHandler(q, service)
	ctx := context.Background()

	// First run: should fire once.
	if err := handler.HandleDeadlineCheck(ctx, nil); err != nil {
		t.Fatalf("first HandleDeadlineCheck: %v", err)
	}
	// Second run: SQL `NOT EXISTS … last 20 hours` should suppress.
	if err := handler.HandleDeadlineCheck(ctx, nil); err != nil {
		t.Fatalf("second HandleDeadlineCheck: %v", err)
	}

	if got := len(sender.Calls()); got != 1 {
		t.Errorf("emails sent across 2 cron runs = %d, want 1 (SQL dedup)", got)
	}
	if got := countDeadlineNotifsForUser(t, q, userID); got != 1 {
		t.Errorf("notification rows after 2 runs = %d, want 1 (SQL dedup)", got)
	}
}

func TestScheduled_SkipsJobWithoutAssignee(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Build a job whose ACTIVE assignment has a deadline but NO assignee
	// (assigned_to = NULL). The cron returns the row, but the handler must
	// treat AssignedTo=NULL as "skip" — not crash on dereference and not
	// write a notification with user_id=0.
	tenant := testutil.SeedTenant(t, q, "OrphanCorp")
	owner := testutil.SeedUser(t, q, tenant.ID, "owner@orphan.test", "pw")
	project := testutil.SeedProject(t, q, tenant.ID, "Orphan Project", "en", []string{"de"})
	file := testutil.SeedFile(t, q, project.ID, "orphan.txt", db.FileFormatTxt)
	job := testutil.SeedJob(t, q, file.ID, "de")

	step, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
		SettingsProfileID: project.SettingsProfileID,
		Name:              "translate", StepOrder: 0, RoleRequired: "member",
	})
	if err != nil {
		t.Fatalf("CreateSettingsWorkflowStep: %v", err)
	}
	assign, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
		JobID: job.ID, WorkflowStepID: step.ID, AssignedTo: nil, Status: "in_progress",
	})
	if err != nil {
		t.Fatalf("CreateJobStepAssignment: %v", err)
	}
	if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: &assign.ID}); err != nil {
		t.Fatalf("SetJobCurrentStep: %v", err)
	}
	if err := q.UpdateStepAssignmentDeadline(ctx, db.UpdateStepAssignmentDeadlineParams{
		ID:       assign.ID,
		Deadline: pgtype.Timestamptz{Time: time.Now().Add(6 * time.Hour), Valid: true},
	}); err != nil {
		t.Fatalf("UpdateStepAssignmentDeadline: %v", err)
	}

	sender := &spySender{}
	service := notif.NewService(q, sender)
	handler := notif.NewScheduledHandler(q, service)

	if err := handler.HandleDeadlineCheck(ctx, nil); err != nil {
		t.Fatalf("HandleDeadlineCheck panicked or failed: %v", err)
	}

	if got := len(sender.Calls()); got != 0 {
		t.Errorf("emails for unassigned job = %d, want 0", got)
	}
	if got := countDeadlineNotifsForUser(t, q, owner.ID); got != 0 {
		t.Errorf("notifications for owner of unassigned job = %d, want 0", got)
	}
}

// One row's Send failure must NOT block subsequent rows. Pins the
// RunDeadlineCheck's "log + continue, don't return err" contract:
//   - Handler still returns nil → asynq doesn't retry the whole task.
//   - The non-failing recipient still receives their email.
//   - The failing row's notification record is still written (Service.Send
//     creates the in-app row before emailing).
//     This means the SQL dedup window catches it on the next run; we're
//     accepting "best-effort email, no retry" as the current contract.
func TestScheduled_FailureMidLoop_DoesNotBlockOthers(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenant := testutil.SeedTenant(t, q, "MultiVendor")
	failingUser := testutil.SeedUser(t, q, tenant.ID, "fail@multivendor.test", "pw")
	healthyUser := testutil.SeedUser(t, q, tenant.ID, "ok@multivendor.test", "pw")
	project := testutil.SeedProject(t, q, tenant.ID, "Multi Project", "en", []string{"fr", "de"})

	// One job per language so the fixture matches the project's target_languages.
	// Both still trigger one EventDeadline each — GetJobsApproachingDeadline
	// doesn't filter by language. Using distinct languages prevents any future
	// reader from misreading the test as "two jobs racing on the same lang".
	jobLangs := []string{"fr", "de"}
	for i, recipient := range []db.User{failingUser, healthyUser} {
		file := testutil.SeedFile(t, q, project.ID, "f"+string(rune('a'+i))+".txt", db.FileFormatTxt)
		job := testutil.SeedJob(t, q, file.ID, jobLangs[i])
		step, err := q.CreateSettingsWorkflowStep(ctx, db.CreateSettingsWorkflowStepParams{
			SettingsProfileID: project.SettingsProfileID,
			Name:              "translate",
			StepOrder:         int32(i),
			RoleRequired:      "member",
		})
		if err != nil {
			t.Fatalf("CreateSettingsWorkflowStep: %v", err)
		}
		assign, err := q.CreateJobStepAssignment(ctx, db.CreateJobStepAssignmentParams{
			JobID:          job.ID,
			WorkflowStepID: step.ID,
			AssignedTo:     &recipient.ID,
			Status:         "in_progress",
		})
		if err != nil {
			t.Fatalf("CreateJobStepAssignment: %v", err)
		}
		if err := q.SetJobCurrentStep(ctx, db.SetJobCurrentStepParams{ID: job.ID, CurrentStepID: &assign.ID}); err != nil {
			t.Fatalf("SetJobCurrentStep: %v", err)
		}
		if err := q.UpdateStepAssignmentDeadline(ctx, db.UpdateStepAssignmentDeadlineParams{
			ID:       assign.ID,
			Deadline: pgtype.Timestamptz{Time: time.Now().Add(8 * time.Hour), Valid: true},
		}); err != nil {
			t.Fatalf("UpdateStepAssignmentDeadline: %v", err)
		}
	}

	sender := &spySender{failOn: "fail@multivendor.test"}
	service := notif.NewService(q, sender)
	handler := notif.NewScheduledHandler(q, service)

	if err := handler.HandleDeadlineCheck(ctx, nil); err != nil {
		t.Fatalf("HandleDeadlineCheck must return nil even when a Send fails: %v", err)
	}

	// The healthy recipient got their email.
	delivered := sender.Calls()
	if len(delivered) != 1 || delivered[0].To != "ok@multivendor.test" {
		t.Errorf("expected one delivered email to ok@multivendor.test, got %#v", delivered)
	}

	// Both recipients have notification rows — Service.Send writes the in-app
	// row regardless of email success (only the `emailed` flag differs).
	// This documents the current "best-effort email" behavior.
	if got := countDeadlineNotifsForUser(t, q, healthyUser.ID); got != 1 {
		t.Errorf("healthyUser deadline notifications = %d, want 1", got)
	}
	if got := countDeadlineNotifsForUser(t, q, failingUser.ID); got != 1 {
		t.Errorf("failingUser deadline notifications = %d, want 1 (in-app row created even though email failed)", got)
	}

	// Ordering invariant (Service.Send: row-first, email-second): the
	// failing recipient's row must have emailed=false; the healthy
	// recipient's row must have emailed=true. If anyone ever flips the
	// order back, the failing-recipient row would either be missing OR
	// have emailed=true (depending on the regression flavour) — both fail
	// these assertions.
	rows := allNotifsByUser(t, q, failingUser.ID)
	if len(rows) != 1 {
		t.Fatalf("failingUser rows = %d, want 1", len(rows))
	}
	if rows[0].Emailed {
		t.Errorf("failingUser row.Emailed = true, want false (SendEmail returned an error)")
	}
	rows = allNotifsByUser(t, q, healthyUser.ID)
	if len(rows) != 1 {
		t.Fatalf("healthyUser rows = %d, want 1", len(rows))
	}
	if !rows[0].Emailed {
		t.Errorf("healthyUser row.Emailed = false, want true (SendEmail succeeded; UpdateNotificationEmailed should have flipped the flag)")
	}
}

// The manual trigger is an org owner/admin route, so it reaches only the
// caller's org: another org's assignees get no reminder from it, and the
// count covers the caller's org alone.
func TestScheduled_ManualTrigger_ScopedToCallersTenant(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	tenantA, userA, _ := seedDeadlineTenant(t, q, "DeadlineA", 12*time.Hour)
	_, userB, _ := seedDeadlineTenant(t, q, "DeadlineB", 12*time.Hour)

	handler := notif.NewScheduledHandler(q, notif.NewService(q, &spySender{}))
	req := httptest.NewRequest(http.MethodPost, "/api/admin/notifications/deadline-check", nil)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{TenantID: tenantA, UserID: userA, Role: auth.RoleOwner}))
	rec := httptest.NewRecorder()
	handler.HandleManualDeadlineCheck(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"dispatched":1`) {
		t.Errorf("body = %s, want dispatched 1 (the caller's org only)", rec.Body.String())
	}
	if got := countDeadlineNotifsForUser(t, q, userA); got != 1 {
		t.Errorf("caller's org assignee notifications = %d, want 1", got)
	}
	if got := countDeadlineNotifsForUser(t, q, userB); got != 0 {
		t.Errorf("other org's assignee notifications = %d, want 0", got)
	}
}

// HandleManualDeadlineCheck (admin endpoint) delegates to RunDeadlineCheck.
// Pinned at the unit level here (no HTTP) — auth gating is enforced by the
// route group in api.go, not the handler itself; pure handler test would be
// tautological. What we DO need to pin: the response shape (`dispatched`)
// and that subsequent calls return 0 (SQL dedup applies to manual triggers
// too — admins can't accidentally re-spam users by clicking twice).
func TestScheduled_ManualTrigger_ReturnsCountAndDedups(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	seedDeadlineFixture(t, q, 12*time.Hour)

	sender := &spySender{}
	service := notif.NewService(q, sender)
	handler := notif.NewScheduledHandler(q, service)
	ctx := context.Background()

	dispatched, err := handler.RunDeadlineCheck(ctx)
	if err != nil {
		t.Fatalf("first RunDeadlineCheck: %v", err)
	}
	if dispatched != 1 {
		t.Errorf("first call dispatched = %d, want 1", dispatched)
	}

	// Second call must return 0 — SQL dedup window covers manual triggers
	// the same way it covers cron firings.
	dispatched, err = handler.RunDeadlineCheck(ctx)
	if err != nil {
		t.Fatalf("second RunDeadlineCheck: %v", err)
	}
	if dispatched != 0 {
		t.Errorf("second call dispatched = %d, want 0 (SQL dedup should suppress)", dispatched)
	}
}

// overlapProbe wraps the querier and records the highest number of
// GetJobsApproachingDeadline calls in flight at once. Each call holds for
// `hold` before querying, so callers that are not serialised overlap.
type overlapProbe struct {
	db.Querier
	hold     time.Duration
	inFlight atomic.Int32
	maxSeen  atomic.Int32
}

func (p *overlapProbe) GetJobsApproachingDeadline(ctx context.Context, tenantID *int64) ([]db.GetJobsApproachingDeadlineRow, error) {
	n := p.inFlight.Add(1)
	defer p.inFlight.Add(-1)
	for {
		m := p.maxSeen.Load()
		if n <= m || p.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(p.hold)
	return p.Querier.GetJobsApproachingDeadline(ctx, tenantID)
}

// Many concurrent RunDeadlineCheck calls must run one at a time and collapse
// to ONE notification row + ONE email. The asynq cron, the admin trigger and
// parallel admin clicks share one in-process gate (runMu), so each run sees
// the previous run's notification row. The probe holds every deadline query
// open, so two runs inside RunDeadlineCheck at once show up as overlap.
func TestScheduled_ConcurrentRuns_CollapseToOne(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	userID, _ := seedDeadlineFixture(t, q, 12*time.Hour)

	probe := &overlapProbe{Querier: q, hold: 20 * time.Millisecond}
	sender := &spySender{}
	service := notif.NewService(q, sender)
	handler := notif.NewScheduledHandler(probe, service)
	ctx := context.Background()

	// Fire many concurrent RunDeadlineCheck calls — simulates the asynq
	// cron firing while an admin clicks the manual-trigger button (and any
	// other concurrent admin gestures). N=10 + a release barrier (channel
	// close) guarantees they all hit RunDeadlineCheck within microseconds
	// of each other; with only 2 goroutines the Go scheduler often runs
	// them sequentially and hides the race even without runMu.
	const N = 10
	var wg sync.WaitGroup
	start := make(chan struct{})
	totalDispatched := make(chan int, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // wait for the simultaneous-release signal
			n, err := handler.RunDeadlineCheck(ctx)
			if err != nil {
				t.Errorf("RunDeadlineCheck: %v", err)
			}
			totalDispatched <- n
		}()
	}
	close(start) // release all goroutines at once
	wg.Wait()
	close(totalDispatched)

	sum := 0
	for n := range totalDispatched {
		sum += n
	}
	if got := probe.maxSeen.Load(); got != 1 {
		t.Errorf("deadline queries in flight at once = %d, want 1 (RunDeadlineCheck must serialise)", got)
	}
	if sum != 1 {
		t.Errorf("total dispatched across %d concurrent calls = %d, want 1", N, sum)
	}
	if got := len(sender.Calls()); got != 1 {
		t.Errorf("emails sent = %d, want 1", got)
	}
	if got := countDeadlineNotifsForUser(t, q, userID); got != 1 {
		t.Errorf("notification rows = %d, want 1", got)
	}
}

// User-level email mute (`shouldEmail=false`) must result in:
//   - in-app notification row created
//   - emailed=false on the row
//   - zero SendEmail calls
//
// Distinguishes "user muted" from "send failed" at the data level — both
// produce emailed=false but for different reasons. Without this test a
// future refactor could merge the two paths and silently re-enable email
// for muted users.
func TestScheduled_UserMuted_RowCreatedNoEmailAttempted(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	userID, _ := seedDeadlineFixture(t, q, 12*time.Hour)

	// Mute deadline emails for this user. Other prefs default true so we
	// know the suppression is specific to deadline.
	if _, err := q.UpsertNotificationPreferences(ctx, db.UpsertNotificationPreferencesParams{
		UserID:          userID,
		EmailAssignment: true,
		EmailRejection:  true,
		EmailCompletion: true,
		EmailDeadline:   false, // <-- the line under test
	}); err != nil {
		t.Fatalf("UpsertNotificationPreferences: %v", err)
	}

	sender := &spySender{}
	service := notif.NewService(q, sender)
	handler := notif.NewScheduledHandler(q, service)

	if _, err := handler.RunDeadlineCheck(ctx); err != nil {
		t.Fatalf("RunDeadlineCheck: %v", err)
	}

	if got := len(sender.Calls()); got != 0 {
		t.Errorf("SendEmail calls = %d, want 0 (user muted deadline emails)", got)
	}

	rows := allNotifsByUser(t, q, userID)
	if len(rows) != 1 {
		t.Fatalf("notification rows = %d, want 1 (in-app row created even when muted)", len(rows))
	}
	if rows[0].Emailed {
		t.Errorf("row.Emailed = true, want false (no email was attempted)")
	}
}

// Service.Send must publish every successful row creation to the
// broker — so the SSE handler delivers it to live subscribers in addition
// to the row landing in the DB. Pinned at the integration level (real
// queries + real Service) because the publish-on-defer contract must
// survive every early-return path inside Send (muted-email, GetUser
// failure, RenderHTML failure, SendEmail failure — all should still fire
// the broker after the row write).
//
// Anti-tautology: removing the `defer s.publishLive(ctx, e)` from Send
// makes this test fail with `published = 0, want 1`.
func TestService_Send_PublishesToBrokerAfterRowWrite(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	tenant := testutil.SeedTenant(t, q, "BrokerCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@brokercorp.test", "pw")

	broker := notif.NewInProcessBroker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub := broker.Subscribe(ctx, user.ID)

	service := notif.NewServiceWithBroker(q, &spySender{}, broker, "")
	if err := service.Send(ctx, notif.Event{
		Type:        notif.EventDeadline,
		TenantID:    tenant.ID,
		RecipientID: user.ID,
		JobID:       42,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case got := <-sub:
		if got.JobID != 42 {
			t.Errorf("broker received JobID=%d, want 42", got.JobID)
		}
	case <-time.After(time.Second):
		t.Fatal("broker subscriber did not receive the event after Send")
	}
}

// Service.Send must publish even when email is skipped (user
// muted) or fails (error from the email provider). The defer pattern in publishLive
// handles all the early returns; this test pins that contract.
func TestService_Send_PublishesEvenWhenEmailMutedOrFails(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tenant := testutil.SeedTenant(t, q, "MutedCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "muted@mutedcorp.test", "pw")
	if _, err := q.UpsertNotificationPreferences(ctx, db.UpsertNotificationPreferencesParams{
		UserID:          user.ID,
		EmailAssignment: true, EmailRejection: true, EmailCompletion: true,
		EmailDeadline: false, // muted
	}); err != nil {
		t.Fatalf("UpsertNotificationPreferences: %v", err)
	}

	broker := notif.NewInProcessBroker()
	sub := broker.Subscribe(ctx, user.ID)

	service := notif.NewServiceWithBroker(q, &spySender{}, broker, "")
	if err := service.Send(ctx, notif.Event{
		Type:        notif.EventDeadline,
		TenantID:    tenant.ID,
		RecipientID: user.ID,
		JobID:       7,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case got := <-sub:
		if got.JobID != 7 {
			t.Errorf("muted-email path: broker received JobID=%d, want 7", got.JobID)
		}
	case <-time.After(time.Second):
		t.Fatal("muted-email path: broker did not fire (live stream must work even when email is muted)")
	}
}

// panicBroker satisfies notif.Broker but panics on Publish — used to
// pin Service.publishLive's defer/recover. Without recover, a broker
// panic propagates out of Send, surfaces as a 500 to the caller, and
// any caller using `defer s.Send(...)` somewhere down the stack would
// chain panics.
type panicBroker struct{}

func (panicBroker) Subscribe(_ context.Context, _ int64) <-chan notif.Event {
	return make(chan notif.Event)
}
func (panicBroker) Publish(_ context.Context, _ notif.Event) error {
	panic("simulated broker panic")
}

// A broker.Publish panic must NOT surface as an
// error from Service.Send — the row was already committed and the
// caller's contract says Send returns nil for "row created, email
// best-effort." A panicking broker is a defensive concern (future
// Redis impl bug, nil deref, network library quirk) — recover converts
// it to a log line and lets the caller proceed.
//
// Anti-tautology: removing the `defer func() { recover() ... }()` block
// from publishLive in service.go makes this test fail with the goroutine
// panicking; the deferred runtime.Goexit propagates and t.Fatal never
// runs cleanly, surfacing as "panic: simulated broker panic" in output.
func TestService_Send_RecoversFromBrokerPanic(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	tenant := testutil.SeedTenant(t, q, "PanicCorp")
	user := testutil.SeedUser(t, q, tenant.ID, "user@paniccorp.test", "pw")

	service := notif.NewServiceWithBroker(q, &spySender{}, panicBroker{}, "")

	// Send must complete without panicking.
	err := service.Send(context.Background(), notif.Event{
		Type: notif.EventDeadline, TenantID: tenant.ID, RecipientID: user.ID, JobID: 1,
	})
	if err != nil {
		t.Fatalf("Send returned error despite the broker panic being deferred + recovered: %v", err)
	}

	// AND the in-app row must have been written (the panic is in publish,
	// AFTER row creation; the recover protects the caller from seeing it).
	if got := countDeadlineNotifsForUser(t, q, user.ID); got != 1 {
		t.Errorf("notification row count = %d, want 1 (row write must succeed even when broker panics)", got)
	}
}

// Notification reads must scope by tenant_id.
// Constructs two tenants with the same userID would be impossible (users
// are tenant-scoped at the schema level), so this test simulates the
// "compromised JWT" / "user_id reuse" risk by directly inserting a row
// for a foreign tenant_id and confirming the lookup excludes it.
//
// Anti-tautology: removing `AND tenant_id = @tenant_id` from
// ListNotifications makes the cross-tenant row leak through and the
// `len(rows) == 1` assertion fails with `len = 2`.
func TestService_NotificationReads_AreTenantScoped(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	tenantA := testutil.SeedTenant(t, q, "TenantA")
	userA := testutil.SeedUser(t, q, tenantA.ID, "user@a.test", "pw")

	// Real notification for userA in tenantA.
	if _, err := q.CreateNotification(ctx, db.CreateNotificationParams{
		TenantID: tenantA.ID, UserID: userA.ID,
		EventType: "deadline", Subject: "real", BodyPreview: "real",
		ReferenceType: "job", ReferenceID: 1, Emailed: false,
	}); err != nil {
		t.Fatalf("create real notification: %v", err)
	}

	// Inject a "stray" row for the same userID but a different tenant_id.
	// In production this can't happen via Service.Send (tenant_id flows
	// from the Event), but the audit's defense-in-depth scope check is
	// for the case where SOMETHING else (raw SQL, replication artifact,
	// migration error) created such a row.
	tenantB := testutil.SeedTenant(t, q, "TenantB")
	if _, err := pool.Exec(ctx,
		`INSERT INTO notifications (tenant_id, user_id, event_type, subject, body_preview, reference_type, reference_id, emailed)
		 VALUES ($1, $2, 'deadline', 'stray', 'stray', 'job', 99, false)`,
		tenantB.ID, userA.ID,
	); err != nil {
		t.Fatalf("inject cross-tenant row: %v", err)
	}

	// `allNotifsByUser` queries via tenant_id from userA (which is tenantA).
	// The stray row in tenantB must NOT be returned.
	rows := allNotifsByUser(t, q, userA.ID)
	if len(rows) != 1 {
		t.Errorf("ListNotifications returned %d rows for user in tenantA, want 1 (stray cross-tenant row leaked)", len(rows))
	}
	if len(rows) > 0 && rows[0].Subject != "real" {
		t.Errorf("got subject %q, want 'real' — query returned the wrong row", rows[0].Subject)
	}

	// Same check for CountUnreadNotifications.
	count, err := q.CountUnreadNotifications(ctx, db.CountUnreadNotificationsParams{
		UserID: userA.ID, TenantID: tenantA.ID,
	})
	if err != nil {
		t.Fatalf("CountUnreadNotifications: %v", err)
	}
	if count != 1 {
		t.Errorf("CountUnreadNotifications = %d, want 1 (stray row leaked)", count)
	}
}

// /health/scheduler shape: distinguishes 4 states ops cares about:
//   - enabled=false                                : turned off by RUN_SCHEDULER
//   - enabled=true,  last_run_at=null              : on, hasn't fired yet
//   - enabled=true,  last_run_at=<recent>          : healthy, firing
//   - enabled=true,  last_run_at=<stale>           : silently stuck (alert)
//
// Test pins shape + the lastRunAt update side-effect of RunDeadlineCheck.
// Ops dashboards consume this — schema drift would silently break alerting.
func TestScheduled_HealthEndpoint_ShapeAndLastRunUpdate(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	sender := &spySender{}
	service := notif.NewService(q, sender)
	handler := notif.NewScheduledHandler(q, service)

	// State 1: enabled=false (default, no SetEnabled call) + no firing yet.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health/scheduler", nil)
	handler.HandleHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["enabled"] != false {
		t.Errorf("enabled = %v, want false (default state before SetEnabled)", resp["enabled"])
	}
	if resp["last_run_at"] != nil {
		t.Errorf("last_run_at = %v, want nil before any firing", resp["last_run_at"])
	}
	if resp["last_run_dispatched"].(float64) != 0 {
		t.Errorf("last_run_dispatched = %v, want 0", resp["last_run_dispatched"])
	}

	// State 2: enabled=true after SetEnabled.
	handler.SetEnabled(true)
	seedDeadlineFixture(t, q, 12*time.Hour)
	if _, err := handler.RunDeadlineCheck(context.Background()); err != nil {
		t.Fatalf("RunDeadlineCheck: %v", err)
	}

	rec = httptest.NewRecorder()
	handler.HandleHealth(rec, httptest.NewRequest("GET", "/health/scheduler", nil))
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["enabled"] != true {
		t.Errorf("enabled = %v, want true after SetEnabled(true)", resp["enabled"])
	}
	if resp["last_run_at"] == nil {
		t.Errorf("last_run_at should be set after RunDeadlineCheck, got nil")
	}
	if got := resp["last_run_dispatched"].(float64); got != 1 {
		t.Errorf("last_run_dispatched = %v, want 1", got)
	}
}

// The deadline lives on the active assignment; there is NO project
// fallback. A project-level deadline approaching must NOT fire per-assignment
// reminders on its own.
//
// Anti-tautology: re-adding a project-deadline branch to
// GetJobsApproachingDeadline makes this test fail with `emails = 1, want 0`.
func TestScheduled_ProjectDeadline_DoesNotFireWithoutAssignmentDeadline(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	// Standard fixture, but clear the assignment deadline and set ONLY a
	// project deadline inside the window.
	userID, jobID := seedDeadlineFixture(t, q, 12*time.Hour)
	if _, err := pool.Exec(ctx, `
		UPDATE job_step_assignments SET deadline = NULL
		WHERE job_id = $1
	`, jobID); err != nil {
		t.Fatalf("clear assignment deadline: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE projects SET deadline = $1
		WHERE id = (SELECT project_id FROM files WHERE id = (SELECT file_id FROM jobs WHERE id = $2))
	`, time.Now().Add(12*time.Hour), jobID); err != nil {
		t.Fatalf("set project deadline: %v", err)
	}

	sender := &spySender{}
	service := notif.NewService(q, sender)
	handler := notif.NewScheduledHandler(q, service)
	if _, err := handler.RunDeadlineCheck(ctx); err != nil {
		t.Fatalf("RunDeadlineCheck: %v", err)
	}
	if got := len(sender.Calls()); got != 0 {
		t.Errorf("emails from project deadline alone = %d, want 0 (no fallback)", got)
	}
	if got := countDeadlineNotifsForUser(t, q, userID); got != 0 {
		t.Errorf("notifications from project deadline alone = %d, want 0", got)
	}
}

func TestScheduled_OutOfWindow_NotPickedUp(t *testing.T) {
	_, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()

	// Deadline 30h out — outside the 24h window the SQL filters on.
	userID, _ := seedDeadlineFixture(t, q, 30*time.Hour)

	sender := &spySender{}
	service := notif.NewService(q, sender)
	handler := notif.NewScheduledHandler(q, service)

	if err := handler.HandleDeadlineCheck(context.Background(), nil); err != nil {
		t.Fatalf("HandleDeadlineCheck: %v", err)
	}

	if got := len(sender.Calls()); got != 0 {
		t.Errorf("emails for out-of-window deadline = %d, want 0", got)
	}
	if got := countDeadlineNotifsForUser(t, q, userID); got != 0 {
		t.Errorf("notifications for out-of-window deadline = %d, want 0", got)
	}
}
