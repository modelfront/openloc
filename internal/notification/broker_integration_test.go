package notification_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	notif "openloc/internal/notification"
	"openloc/internal/pglisten"
	"openloc/internal/testutil"
)

// process is one server process: its own pool, listen connection and broker.
type process struct {
	pool   *pgxpool.Pool
	broker *notif.PGBroker
	name   string
}

func newProcess(t *testing.T, shared *pgxpool.Pool, name string) *process {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, shared.Config())
	if err != nil {
		t.Fatal(err)
	}
	p := &process{pool: pool, broker: notif.NewPGBroker(pool), name: name}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(ctx, testutil.Within)
		defer cancel()
		p.broker.Close(closeCtx)
		pool.Close()
	})
	return p
}

// listenerName finds the process's listen connection in pg_stat_activity.
func (p *process) listenerName() string { return "notif-listen-" + p.name }

// listen starts the process's listener without waiting for it.
func (p *process) listen(t *testing.T) {
	t.Helper()
	l := pglisten.New()
	p.broker.ListenOn(l)
	testutil.RunListener(t, l, testutil.ListenerConfig(p.pool.Config().ConnConfig, p.listenerName()))
}

// waitLive waits until the process's listen connection is up.
func (p *process) waitLive(t *testing.T) {
	t.Helper()
	testutil.WaitFor(t, func() bool { return notif.LiveForTest(p.broker) }, "process "+p.name+" listening")
}

func startProcess(t *testing.T, shared *pgxpool.Pool, name string) *process {
	t.Helper()
	p := newProcess(t, shared, name)
	p.listen(t)
	p.waitLive(t)
	return p
}

// terminateListener ends the process's listen connection from the server side.
func (p *process) terminateListener(t *testing.T) {
	t.Helper()
	testutil.TerminateConnection(t, p.pool, p.listenerName())
}

func subscribe(t *testing.T, p *process, userID int64) <-chan notif.Event {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return p.broker.Subscribe(ctx, userID)
}

// Two server processes on one database: an event published on one reaches a
// subscriber connected to the other.
func TestIntegrationBroker_PublishReachesTheOtherProcess(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	a, b := startProcess(t, pool, "a"), startProcess(t, pool, "b")
	events := subscribe(t, b, 42)

	if err := a.broker.Publish(context.Background(), notif.Event{
		Type: notif.EventAssignment, TenantID: 1, RecipientID: 42, JobID: 7, ProjectID: 3,
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	ev := testutil.Receive(t, events, "a subscriber on the other process")
	if ev.Type != notif.EventAssignment || ev.TenantID != 1 || ev.RecipientID != 42 || ev.JobID != 7 || ev.ProjectID != 3 {
		t.Fatalf("got %+v", ev)
	}
}

// Each process's own subscribers get its events too, and only the
// recipient's.
func TestIntegrationBroker_EventsReachOnlyTheirRecipient(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	a, b := startProcess(t, pool, "a"), startProcess(t, pool, "b")
	onA, onB, other := subscribe(t, a, 42), subscribe(t, b, 42), subscribe(t, b, 99)

	ctx := context.Background()
	if err := a.broker.Publish(ctx, notif.Event{Type: notif.EventRejection, TenantID: 1, RecipientID: 42, JobID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := a.broker.Publish(ctx, notif.Event{Type: notif.EventRejection, TenantID: 1, RecipientID: 99, JobID: 2}); err != nil {
		t.Fatal(err)
	}

	if ev := testutil.Receive(t, onA, "the publishing process's own subscriber"); ev.JobID != 1 {
		t.Fatalf("user 42 on a got job %d", ev.JobID)
	}
	if ev := testutil.Receive(t, onB, "user 42 on the other process"); ev.JobID != 1 {
		t.Fatalf("user 42 on b got job %d", ev.JobID)
	}
	// Notices keep their order, so user 99's first event is its own unless
	// routing leaked user 42's to it.
	if ev := testutil.Receive(t, other, "user 99 on the other process"); ev.JobID != 2 || ev.RecipientID != 99 {
		t.Fatalf("user 99 got %+v", ev)
	}
}

// The notice between processes carries ids and the event type, never a
// project, file or person's name, nor what someone wrote.
func TestIntegrationBroker_NoticeCarriesNoText(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	a := newProcess(t, pool, "a")

	conn, err := pgx.ConnectConfig(ctx, pool.Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "LISTEN "+notif.NotifyChannel); err != nil {
		t.Fatal(err)
	}

	texts := []string{"Secret Project", "secret-file.docx", "Ana Actor", "the reason given", "Issue title", "Translation", "https://example.com/link"}
	if err := a.broker.Publish(ctx, notif.Event{
		Type: notif.EventAssignment, TenantID: 5, RecipientID: 6, ActorID: 7, JobID: 8, ProjectID: 9, IssueID: 10,
		ActorName: texts[2], ProjectName: texts[0], FileName: texts[1], TargetLang: "de", StepName: texts[5],
		Reason: texts[3], IssueTitle: texts[4], IssueStatus: "Resolved", Link: texts[6],
	}); err != nil {
		t.Fatal(err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, testutil.Within)
	defer cancel()
	n, err := conn.WaitForNotification(waitCtx)
	if err != nil {
		t.Fatalf("no notice: %v", err)
	}
	for _, text := range append(texts, "de", "Resolved") {
		if strings.Contains(n.Payload, text) {
			t.Errorf("notice carries %q: %s", text, n.Payload)
		}
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(n.Payload), &got); err != nil {
		t.Fatalf("payload %q: %v", n.Payload, err)
	}
	want := map[string]any{"tenant_id": 5.0, "user_id": 6.0, "type": "assignment", "job_id": 8.0, "project_id": 9.0}
	if len(got) != len(want) {
		t.Fatalf("payload %v, want exactly %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("payload %s = %v, want %v", k, got[k], v)
		}
	}
}

// A lost listen connection ends the subscriptions it was serving; once the
// process listens again, new ones receive.
func TestIntegrationBroker_ALostListenerEndsSubscriptions(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	a, b := startProcess(t, pool, "a"), startProcess(t, pool, "b")
	events := subscribe(t, b, 42)

	b.terminateListener(t)
	testutil.Closed(t, events, "subscriber on the process that lost its listener")

	b.waitLive(t)
	again := subscribe(t, b, 42)
	if err := a.broker.Publish(context.Background(), notif.Event{Type: notif.EventCompletion, TenantID: 1, RecipientID: 42, JobID: 5}); err != nil {
		t.Fatal(err)
	}
	if ev := testutil.Receive(t, again, "a subscriber after the reconnect"); ev.JobID != 5 {
		t.Fatalf("got job %d", ev.JobID)
	}
}

// A subscription made while the process does not listen yet stays open,
// quietly, so the client does not reconnect in a loop during an outage. It
// missed whatever was published meanwhile, so it ends once the listener is
// up, and the client reconnects and rereads the DB.
func TestIntegrationBroker_ASubscriptionFromBeforeTheListenerEndsWhenItComesUp(t *testing.T) {
	pool, _, cleanup := testutil.SetupTestDB(t)
	defer cleanup()
	b := newProcess(t, pool, "b")
	early := subscribe(t, b, 42)
	testutil.ReceiveNone(t, early, "a subscription while the process does not listen")

	b.listen(t)
	testutil.Closed(t, early, "a subscription from before the listener")
	b.waitLive(t)
}
