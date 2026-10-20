package hold

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"

	"openloc/internal/logging"
	"openloc/internal/pglisten"
)

// NotifyChannel is the Postgres channel the translation_holds trigger
// notifies, with the job id as the payload, when a change other editors can
// see commits: a hold taken, passed to another user, or ended while live.
const NotifyChannel = "translation_holds"

// Feed wakes the open editors of a job, on every server process, when the
// job's holds change. The process's pglisten.Listener receives the trigger's
// notices; each wakes that job's subscribers, who refetch the holds. A
// wake-up carries no data, so nothing in it needs a tenant check.
type Feed struct {
	mu   sync.Mutex
	subs map[int64]map[chan struct{}]struct{}
	// down is closed when the current listen connection ends; nil while
	// none is up.
	down chan struct{}
}

// NewFeed returns a Feed that is not receiving yet. ListenOn makes it
// receive once the listener runs.
func NewFeed() *Feed {
	return &Feed{subs: map[int64]map[chan struct{}]struct{}{}}
}

// ListenOn makes l deliver the trigger's notices to the feed. Call it before
// l.Run.
func (f *Feed) ListenOn(l *pglisten.Listener) {
	l.Handle(NotifyChannel, receiver{f})
}

// Subscribe returns a channel that receives a value when the job's holds may
// have changed, and one that closes when the feed stops receiving; no change
// reaches the first after that. Wake-ups coalesce: one not yet read absorbs
// the next. ok is false while the feed is not receiving. The first channel
// closes when ctx ends.
func (f *Feed) Subscribe(ctx context.Context, jobID int64) (changes, down <-chan struct{}, ok bool) {
	f.mu.Lock()
	if f.down == nil {
		f.mu.Unlock()
		return nil, nil, false
	}
	ch := make(chan struct{}, 1)
	if f.subs[jobID] == nil {
		f.subs[jobID] = map[chan struct{}]struct{}{}
	}
	f.subs[jobID][ch] = struct{}{}
	down = f.down
	f.mu.Unlock()

	go func() {
		<-ctx.Done()
		f.mu.Lock()
		delete(f.subs[jobID], ch)
		if len(f.subs[jobID]) == 0 {
			delete(f.subs, jobID)
		}
		f.mu.Unlock()
		close(ch)
	}()
	return ch, down, true
}

func (f *Feed) wake(jobID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ch := range f.subs[jobID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// receiver is the feed's pglisten.Handler.
type receiver struct{ f *Feed }

// Up marks the feed as receiving.
func (r receiver) Up() {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	r.f.down = make(chan struct{})
}

// Down marks the feed as not receiving and tells every subscriber.
func (r receiver) Down() {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	if r.f.down != nil {
		close(r.f.down)
		r.f.down = nil
	}
}

// Notify wakes the subscribers of the job a notice names.
func (r receiver) Notify(ctx context.Context, payload string) {
	jobID, err := strconv.ParseInt(payload, 10, 64)
	if err != nil {
		logging.Warn(ctx, errors.New("not a job id"), "segment hold notice ignored", "segment-hold-feed-bad-payload",
			slog.String("payload", payload))
		return
	}
	r.f.wake(jobID)
}
