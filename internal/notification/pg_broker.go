package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"openloc/internal/errtrack"
	"openloc/internal/logging"
	"openloc/internal/pglisten"
)

// NotifyChannel is the Postgres channel notification notices travel on.
const NotifyChannel = "notifications"

const (
	// noticeQueue caps the notices waiting to be sent. Past it Publish drops
	// the event; the DB row still reaches the bell on its next poll.
	noticeQueue = 1024
	// noticeTimeout bounds one pg_notify.
	noticeTimeout = 5 * time.Second
)

var (
	errBrokerClosed = errors.New("notification broker closed")
	errQueueFull    = errors.New("notification notice queue full")
)

// Notifier runs pg_notify; *pgxpool.Pool satisfies it.
type Notifier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// notice is the pg_notify payload: ids and the event type, never text. The
// receiving process routes it by user, and the stream drops it unless the
// tenant is the session's.
type notice struct {
	TenantID  int64     `json:"tenant_id"`
	UserID    int64     `json:"user_id"`
	Type      EventType `json:"type"`
	JobID     int64     `json:"job_id,omitempty"`
	ProjectID int64     `json:"project_id,omitempty"`
}

// PGBroker delivers events to subscribers on every server process. Publish
// queues a notice, which one goroutine sends with pg_notify on the pool, so a
// slow or unreachable database never holds up the publisher. Every process's
// pglisten.Listener receives the notice and fans it out to that process's
// subscribers, the publisher's own included.
//
// A notice committed while a process's listen connection is down never
// reaches that process. So the broker ends every subscription when the
// connection goes down and again when it comes up: the client reconnects, and
// the stream's `connected` event makes the bell reread the DB.
type PGBroker struct {
	local *InProcessBroker
	db    Notifier

	mu     sync.Mutex
	queue  chan string
	closed bool

	sendCtx     context.Context
	stopSending context.CancelFunc
	sent        chan struct{}

	// live reports whether the listen connection is up.
	live atomic.Bool
}

// NewPGBroker starts the goroutine that sends notices on db. Close stops it.
// ListenOn connects the broker to the process's listener.
func NewPGBroker(db Notifier) *PGBroker {
	sendCtx, stop := context.WithCancel(context.Background())
	b := &PGBroker{
		local:       NewInProcessBroker(),
		db:          db,
		queue:       make(chan string, noticeQueue),
		sendCtx:     sendCtx,
		stopSending: stop,
		sent:        make(chan struct{}),
	}
	go b.send()
	return b
}

// ListenOn makes l deliver the broker's notices. Call it before l.Run.
func (b *PGBroker) ListenOn(l *pglisten.Listener) {
	l.Handle(NotifyChannel, receiver{b})
}

func (b *PGBroker) Subscribe(ctx context.Context, userID int64) <-chan Event {
	return b.local.Subscribe(ctx, userID)
}

// Publish queues the event's notice and returns at once. It fails when the
// queue is full or the broker is closed; the event is then dropped.
func (b *PGBroker) Publish(_ context.Context, e Event) error {
	payload, err := json.Marshal(notice{
		TenantID: e.TenantID, UserID: e.RecipientID, Type: e.Type, JobID: e.JobID, ProjectID: e.ProjectID,
	})
	if err != nil {
		return fmt.Errorf("encode notification notice: %w", err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errBrokerClosed
	}
	select {
	case b.queue <- string(payload):
		return nil
	default:
		return errQueueFull
	}
}

// Close stops Publish and waits until the queued notices are sent, or ctx
// ends; the rest are dropped then.
func (b *PGBroker) Close(ctx context.Context) {
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		close(b.queue)
	}
	b.mu.Unlock()
	select {
	case <-b.sent:
	case <-ctx.Done():
		b.stopSending()
		<-b.sent
	}
	b.stopSending()
}

func (b *PGBroker) send() {
	defer close(b.sent)
	defer errtrack.Monitor(b.sendCtx, "notification", "broker-send")
	dropped := 0
	defer func() {
		if dropped > 0 {
			logging.Warn(context.Background(), nil, "notification notices dropped when the broker closed", "notification-notices-dropped",
				logging.Count(dropped))
		}
	}()
	for payload := range b.queue {
		if b.sendCtx.Err() != nil {
			dropped++
			continue
		}
		ctx, cancel := context.WithTimeout(b.sendCtx, noticeTimeout)
		_, err := b.db.Exec(ctx, "SELECT pg_notify($1, $2)", NotifyChannel, payload)
		cancel()
		if err != nil {
			logging.Warn(context.Background(), err, "notification notice not sent", "notification-notice-send-failed")
		}
	}
}

// receiver is the broker's pglisten.Handler.
type receiver struct{ b *PGBroker }

// Up ends every subscription, each of which missed what was committed while
// the connection was down. live turns true after endAll, so a subscription
// made once live reads true is not ended by it.
func (r receiver) Up() {
	r.b.local.endAll()
	r.b.live.Store(true)
}

// Down clears live and ends every subscription: none gets what is committed
// until the connection is back.
func (r receiver) Down() {
	r.b.live.Store(false)
	r.b.local.endAll()
}

func (r receiver) Notify(ctx context.Context, payload string) {
	var n notice
	if err := json.Unmarshal([]byte(payload), &n); err != nil || n.TenantID <= 0 || n.UserID <= 0 || n.Type == "" {
		if err == nil {
			err = errors.New("notice lacks tenant, user or type")
		}
		logging.Warn(ctx, err, "notification notice ignored", "notification-notice-bad-payload")
		return
	}
	r.b.local.deliver(Event{
		Type: n.Type, TenantID: n.TenantID, RecipientID: n.UserID, JobID: n.JobID, ProjectID: n.ProjectID,
	})
}
