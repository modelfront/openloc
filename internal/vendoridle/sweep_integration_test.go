package vendoridle_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/emaillayout"
	"openloc/internal/notification"
	"openloc/internal/testutil"
	"openloc/internal/vendoridle"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type failingSender struct{}

func (failingSender) SendEmail(context.Context, string, string, emaillayout.Email) error {
	return errors.New("smtp down")
}

// age sets how long ago the member last used the org and, when warnedDaysAgo
// is not negative, when the warning went out.
func age(t *testing.T, pool *pgxpool.Pool, tenantID, userID int64, unusedDays, warnedDaysAgo int) {
	t.Helper()
	var warned any
	if warnedDaysAgo >= 0 {
		warned = time.Now().AddDate(0, 0, -warnedDaysAgo)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE memberships SET last_active_at = $3, inactivity_warned_at = $4 WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID, time.Now().AddDate(0, 0, -unusedDays), warned); err != nil {
		t.Fatalf("age: %v", err)
	}
}

type state struct {
	warned, disabled, claimed bool
}

func stateOf(t *testing.T, pool *pgxpool.Pool, tenantID, userID int64) state {
	t.Helper()
	var s state
	if err := pool.QueryRow(context.Background(),
		`SELECT inactivity_warned_at IS NOT NULL, disabled_at IS NOT NULL, inactivity_warning_claimed_at IS NOT NULL
		 FROM memberships WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID).Scan(&s.warned, &s.disabled, &s.claimed); err != nil {
		t.Fatalf("state: %v", err)
	}
	return s
}

// TestIntegrationSweep: a vendor org member unused for 83 days gets one
// warning email; one unused for 90 days is disabled, but only once a warning
// went out 7 days before. Client org members are never touched.
func TestIntegrationSweep(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	client := testutil.SeedTenant(t, q, "Client Corp")
	clientOwner := testutil.SeedUser(t, q, client.ID, "owner@client.test", "pw")
	vendor := testutil.SeedVendorTenant(t, q, client.ID, "Acme Vendors")
	seed := func(email, role string) db.User { return testutil.SeedUserWithRole(t, q, vendor.ID, email, "pw", role) }
	fresh := seed("fresh@acme.test", "linguist")
	recent := seed("recent@acme.test", "linguist")
	due := seed("due@acme.test", "project_manager")
	lateNoWarning := seed("late@acme.test", "admin")
	warnedLong := seed("warned-long@acme.test", "linguist")
	warnedRecently := seed("warned-recently@acme.test", "admin")

	age(t, pool, vendor.ID, recent.ID, 80, -1)
	age(t, pool, vendor.ID, due.ID, 84, -1)
	age(t, pool, vendor.ID, lateNoWarning.ID, 120, -1)
	age(t, pool, vendor.ID, warnedLong.ID, 95, 8)
	age(t, pool, vendor.ID, warnedRecently.ID, 95, 3)
	age(t, pool, client.ID, clientOwner.ID, 400, -1)

	outbox := notification.NewOutbox(100)
	sweeper := vendoridle.New(q, &notification.RecordingSender{Next: &notification.ConsoleSender{}, Outbox: outbox}, "https://app.example.com")
	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	want := map[string]state{
		fresh.Email:          {},
		recent.Email:         {},
		due.Email:            {warned: true},
		lateNoWarning.Email:  {warned: true}, // warned now, disabled 7 days later
		warnedLong.Email:     {warned: true, disabled: true},
		warnedRecently.Email: {warned: true},
	}
	for _, u := range []db.User{fresh, recent, due, lateNoWarning, warnedLong, warnedRecently} {
		if got := stateOf(t, pool, vendor.ID, u.ID); got != want[u.Email] {
			t.Errorf("%s: %+v, want %+v", u.Email, got, want[u.Email])
		}
	}
	if got := stateOf(t, pool, client.ID, clientOwner.ID); got != (state{}) {
		t.Errorf("client owner: %+v, want untouched", got)
	}

	for _, email := range []string{due.Email, lateNoWarning.Email} {
		mails := outbox.For(email)
		if len(mails) != 1 {
			t.Fatalf("%s: want 1 warning email, got %d", email, len(mails))
		}
		if !strings.Contains(mails[0].Subject, "Acme Vendors") || !strings.Contains(mails[0].Body, "7 days") {
			t.Errorf("%s: warning = %q / %q, want the vendor name and the 7 days", email, mails[0].Subject, mails[0].Body)
		}
	}
	for _, email := range []string{fresh.Email, recent.Email, warnedLong.Email, warnedRecently.Email, clientOwner.Email} {
		if n := len(outbox.For(email)); n != 0 {
			t.Errorf("%s got %d emails", email, n)
		}
	}

	// A second sweep sends no second warning.
	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	if n := len(outbox.For(due.Email)); n != 1 {
		t.Errorf("second sweep: %s has %d warnings, want 1", due.Email, n)
	}
}

// A warning whose email fails is not recorded and its claim is given back, so
// the next sweep tries again at once and nobody is disabled on a warning they
// never got.
func TestIntegrationSweep_FailedWarningRetries(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	client := testutil.SeedTenant(t, q, "Client Corp")
	vendor := testutil.SeedVendorTenant(t, q, client.ID, "Acme Vendors")
	ling := testutil.SeedUserWithRole(t, q, vendor.ID, "ling@acme.test", "pw", "linguist")
	age(t, pool, vendor.ID, ling.ID, 100, -1)

	if err := vendoridle.New(q, failingSender{}, "https://app.example.com").Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := stateOf(t, pool, vendor.ID, ling.ID); got != (state{}) {
		t.Errorf("after a failed email: %+v, want not warned, not disabled, not claimed", got)
	}

	outbox := notification.NewOutbox(10)
	if err := vendoridle.New(q, &notification.RecordingSender{Next: &notification.ConsoleSender{}, Outbox: outbox}, "https://app.example.com").Sweep(ctx); err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	if n := len(outbox.For(ling.Email)); n != 1 {
		t.Errorf("the next sweep sent %d warnings, want 1", n)
	}
	if got := stateOf(t, pool, vendor.ID, ling.ID); got != (state{warned: true}) {
		t.Errorf("after the next sweep: %+v, want warned", got)
	}
}

// countingSender fails every email and counts the attempts.
type countingSender struct{ calls int }

func (c *countingSender) SendEmail(context.Context, string, string, emaillayout.Email) error {
	c.calls++
	return errors.New("smtp down")
}

// A mail outage with a full batch due: the sweep tries each person once and
// stops, rather than claiming the same rows again and again. Nobody is left
// marked warned.
func TestIntegrationSweep_FailingBatchStops(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := testutil.SeedTenant(t, q, "Client Corp")
	vendor := testutil.SeedVendorTenant(t, q, client.ID, "Acme Vendors")
	var people []db.User
	for _, email := range []string{"a@acme.test", "b@acme.test", "c@acme.test"} {
		u := testutil.SeedUserWithRole(t, q, vendor.ID, email, "pw", "linguist")
		age(t, pool, vendor.ID, u.ID, 100, -1)
		people = append(people, u)
	}

	sender := &countingSender{}
	sweeper := vendoridle.New(q, sender, "https://app.example.com")
	sweeper.SetBatch(2)
	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if sender.calls != 2 {
		t.Errorf("tried %d emails, want 2: one batch, then wait for the next sweep", sender.calls)
	}
	for _, u := range people {
		if got := stateOf(t, pool, vendor.ID, u.ID); got != (state{}) {
			t.Errorf("%s: %+v, want not warned and not disabled", u.Email, got)
		}
	}
}

// cancellingSender sends the first email, then the sweep's context ends (a
// deploy, the task deadline) before the next one.
type cancellingSender struct {
	cancel context.CancelFunc
	sent   []string
}

func (c *cancellingSender) SendEmail(ctx context.Context, to, _ string, _ emaillayout.Email) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.sent = append(c.sent, to)
	c.cancel()
	return nil
}

// A sweep cut off mid-batch leaves marked warned only the people it emailed:
// the others were never told, so they must not be disabled a week later.
func TestIntegrationSweep_CancelledMidBatch(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := testutil.SeedTenant(t, q, "Client Corp")
	vendor := testutil.SeedVendorTenant(t, q, client.ID, "Acme Vendors")
	var people []db.User
	for i, email := range []string{"a@acme.test", "b@acme.test", "c@acme.test"} {
		u := testutil.SeedUserWithRole(t, q, vendor.ID, email, "pw", "linguist")
		age(t, pool, vendor.ID, u.ID, 100+i, -1) // a is claimed last, c first
		people = append(people, u)
	}

	sender := &cancellingSender{cancel: cancel}
	if err := vendoridle.New(q, sender, "https://app.example.com").Sweep(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sweep = %v, want the cancellation", err)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent %v, want exactly one email before the cut", sender.sent)
	}
	for _, u := range people {
		want := state{warned: u.Email == sender.sent[0]}
		if got := stateOf(t, pool, vendor.ID, u.ID); got != want {
			t.Errorf("%s: %+v, want %+v", u.Email, got, want)
		}
	}
}

// While a vendor org's client is not approved its people cannot use it, so
// they are neither warned nor disabled for disuse.
func TestIntegrationSweep_SkipsInactiveClient(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	client := testutil.SeedTenant(t, q, "Client Corp")
	vendor := testutil.SeedVendorTenant(t, q, client.ID, "Acme Vendors")
	due := testutil.SeedUserWithRole(t, q, vendor.ID, "due@acme.test", "pw", "linguist")
	late := testutil.SeedUserWithRole(t, q, vendor.ID, "late@acme.test", "pw", "linguist")
	age(t, pool, vendor.ID, due.ID, 85, -1)
	age(t, pool, vendor.ID, late.ID, 95, 8)
	if _, err := q.SetTenantStatus(ctx, db.SetTenantStatusParams{ID: client.ID, Status: "suspended"}); err != nil {
		t.Fatalf("suspend client: %v", err)
	}

	outbox := notification.NewOutbox(10)
	sweeper := vendoridle.New(q, &notification.RecordingSender{Next: &notification.ConsoleSender{}, Outbox: outbox}, "https://app.example.com")
	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := stateOf(t, pool, vendor.ID, due.ID); got != (state{}) {
		t.Errorf("due: %+v, want not warned", got)
	}
	// A warning sent before the suspension is withdrawn: its 7 days ran while
	// the person could not sign in.
	if got := stateOf(t, pool, vendor.ID, late.ID); got != (state{}) {
		t.Errorf("late: %+v, want the warning withdrawn, not disabled", got)
	}
	if n := len(outbox.For(due.Email)); n != 0 {
		t.Errorf("sent %d warnings to the people of an inactive client's vendor", n)
	}

	// Once the client is approved again, everyone overdue gets a fresh warning
	// and a full 7 days, nobody is disabled at once.
	if _, err := q.SetTenantStatus(ctx, db.SetTenantStatusParams{ID: client.ID, Status: "approved"}); err != nil {
		t.Fatalf("approve client: %v", err)
	}
	if err := sweeper.Sweep(ctx); err != nil {
		t.Fatalf("Sweep after re-approval: %v", err)
	}
	for _, u := range []db.User{due, late} {
		if got := stateOf(t, pool, vendor.ID, u.ID); got != (state{warned: true}) {
			t.Errorf("%s after re-approval: %+v, want warned again, not disabled", u.Email, got)
		}
		if n := len(outbox.For(u.Email)); n != 1 {
			t.Errorf("%s after re-approval: %d warnings, want 1", u.Email, n)
		}
	}
}

// A suspended vendor org is the same: its people are neither warned nor
// disabled meanwhile, and a warning sent before is withdrawn.
func TestIntegrationSweep_SkipsSuspendedVendor(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	client := testutil.SeedTenant(t, q, "Client Corp")
	vendor := testutil.SeedVendorTenant(t, q, client.ID, "Acme Vendors")
	late := testutil.SeedUserWithRole(t, q, vendor.ID, "late@acme.test", "pw", "linguist")
	age(t, pool, vendor.ID, late.ID, 95, 8)
	if _, err := q.SetTenantStatus(ctx, db.SetTenantStatusParams{ID: vendor.ID, Status: "suspended"}); err != nil {
		t.Fatalf("suspend vendor: %v", err)
	}
	if err := vendoridle.New(q, &notification.ConsoleSender{}, "https://app.example.com").Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := stateOf(t, pool, vendor.ID, late.ID); got != (state{}) {
		t.Errorf("late: %+v, want the warning withdrawn, not disabled", got)
	}
}

// A claim whose email never went out (the sweep died between claiming and
// sending, or could not give the claim back) is no warning: the person is
// never disabled on it, and once the claim's lease ran out a later sweep
// warns them. A claim still within its lease is left to the sweep that holds
// it.
func TestIntegrationSweep_UnsentClaimExpires(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	client := testutil.SeedTenant(t, q, "Client Corp")
	vendor := testutil.SeedVendorTenant(t, q, client.ID, "Acme Vendors")
	stale := testutil.SeedUserWithRole(t, q, vendor.ID, "stale@acme.test", "pw", "linguist")
	fresh := testutil.SeedUserWithRole(t, q, vendor.ID, "fresh@acme.test", "pw", "linguist")
	age(t, pool, vendor.ID, stale.ID, 100, -1)
	age(t, pool, vendor.ID, fresh.ID, 100, -1)
	claim := func(u db.User, ago time.Duration) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE memberships SET inactivity_warning_claimed_at = $3 WHERE tenant_id = $1 AND user_id = $2`,
			vendor.ID, u.ID, time.Now().Add(-ago)); err != nil {
			t.Fatalf("claim: %v", err)
		}
	}
	claim(stale, 8*24*time.Hour) // claimed a week ago, never sent
	claim(fresh, 5*time.Minute)  // another sweep is sending it right now

	outbox := notification.NewOutbox(10)
	if err := vendoridle.New(q, &notification.RecordingSender{Next: &notification.ConsoleSender{}, Outbox: outbox}, "https://app.example.com").Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := stateOf(t, pool, vendor.ID, stale.ID); got != (state{warned: true}) {
		t.Errorf("stale claim: %+v, want warned now, not disabled", got)
	}
	if n := len(outbox.For(stale.Email)); n != 1 {
		t.Errorf("stale claim: %d warnings, want 1", n)
	}
	if got := stateOf(t, pool, vendor.ID, fresh.ID); got != (state{claimed: true}) {
		t.Errorf("fresh claim: %+v, want left alone", got)
	}
	if n := len(outbox.For(fresh.Email)); n != 0 {
		t.Errorf("fresh claim: %d warnings, want none from this sweep", n)
	}
}

// usingSender stands for the member using the org while their warning email
// is on its way: the gate's touch lands between the claim and the mark.
type usingSender struct {
	q                db.Querier
	tenantID, userID int64
}

func (u usingSender) SendEmail(ctx context.Context, _, _ string, _ emaillayout.Email) error {
	_, err := u.q.TouchVendorMembership(ctx, db.TouchVendorMembershipParams{TenantID: u.tenantID, UserID: u.userID})
	return err
}

// A member who uses the org while their warning is being sent has no pending
// warning afterwards: the use withdrew the claim, so a late mark must not
// record a warning the next 90 days would count on.
func TestIntegrationSweep_UseDuringSendWithdraws(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	client := testutil.SeedTenant(t, q, "Client Corp")
	vendor := testutil.SeedVendorTenant(t, q, client.ID, "Acme Vendors")
	ling := testutil.SeedUserWithRole(t, q, vendor.ID, "ling@acme.test", "pw", "linguist")
	age(t, pool, vendor.ID, ling.ID, 85, -1)

	sender := usingSender{q: q, tenantID: vendor.ID, userID: ling.ID}
	if err := vendoridle.New(q, sender, "https://app.example.com").Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := stateOf(t, pool, vendor.ID, ling.ID); got != (state{}) {
		t.Errorf("after use during the send: %+v, want no warning", got)
	}
}

// failingMark records no warning: the database refuses the mark.
type failingMark struct{ db.Querier }

func (failingMark) MarkInactivityWarned(context.Context, int64) error {
	return errors.New("db blip")
}

// When recording a sent warning fails, the sweep stops and gives back the
// claims it did not send yet, so the next sweep warns those people at once
// instead of after the lease.
func TestIntegrationSweep_FailedMarkReleasesRest(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	client := testutil.SeedTenant(t, q, "Client Corp")
	vendor := testutil.SeedVendorTenant(t, q, client.ID, "Acme Vendors")
	var people []db.User
	for i, email := range []string{"a@acme.test", "b@acme.test", "c@acme.test"} {
		u := testutil.SeedUserWithRole(t, q, vendor.ID, email, "pw", "linguist")
		age(t, pool, vendor.ID, u.ID, 100+i, -1) // c is claimed first
		people = append(people, u)
	}

	outbox := notification.NewOutbox(10)
	sender := &notification.RecordingSender{Next: &notification.ConsoleSender{}, Outbox: outbox}
	if err := vendoridle.New(failingMark{q}, sender, "https://app.example.com").Sweep(ctx); err == nil {
		t.Fatal("Sweep = nil, want the mark failure")
	}
	emailed := 0
	for _, u := range people {
		emailed += len(outbox.For(u.Email))
	}
	if emailed != 1 {
		t.Errorf("sent %d warnings, want 1: the sweep stops at the first failed mark", emailed)
	}
	for _, u := range people {
		sent := len(outbox.For(u.Email)) == 1
		// The one emailed keeps its claim until the lease ends; the rest are free.
		if got := stateOf(t, pool, vendor.ID, u.ID); got != (state{claimed: sent}) {
			t.Errorf("%s (emailed %v): %+v, want claimed only if emailed", u.Email, sent, got)
		}
	}
}

// The disable step checks on its own that the vendor org and its client are
// approved, whatever ran before it: a member past the warning of a suspended
// vendor org, or of a suspended client's vendor, is not disabled.
func TestIntegrationDisableSkipsUnapproved(t *testing.T) {
	pool, q, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	for _, suspend := range []string{"vendor", "client"} {
		t.Run(suspend, func(t *testing.T) {
			client := testutil.SeedTenant(t, q, "Client of "+suspend)
			vendor := testutil.SeedVendorTenant(t, q, client.ID, "Vendors of "+suspend)
			late := testutil.SeedUserWithRole(t, q, vendor.ID, "late-"+suspend+"@acme.test", "pw", "linguist")
			age(t, pool, vendor.ID, late.ID, 95, 8)
			target := vendor.ID
			if suspend == "client" {
				target = client.ID
			}
			if _, err := q.SetTenantStatus(ctx, db.SetTenantStatusParams{ID: target, Status: "suspended"}); err != nil {
				t.Fatalf("suspend: %v", err)
			}
			n, err := q.DisableInactiveVendorMemberships(ctx, db.DisableInactiveVendorMembershipsParams{
				UnusedSince:  pgtype.Timestamptz{Time: time.Now().Add(-vendoridle.InactiveAfter), Valid: true},
				WarnedBefore: pgtype.Timestamptz{Time: time.Now().Add(-vendoridle.WarnAhead), Valid: true},
				Lim:          10,
			})
			if err != nil {
				t.Fatalf("DisableInactiveVendorMemberships: %v", err)
			}
			if n != 0 {
				t.Errorf("disabled %d memberships, want none", n)
			}
			if got := stateOf(t, pool, vendor.ID, late.ID); got != (state{warned: true}) {
				t.Errorf("late: %+v, want still warned, not disabled", got)
			}
		})
	}
}
