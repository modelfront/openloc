// Package pglisten receives Postgres notices on one LISTEN connection per
// server process and hands each to the handler of its channel, so a change
// committed by any process reaches every process.
package pglisten

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"openloc/internal/logging"
)

const (
	retryMin = time.Second
	retryMax = 30 * time.Second
	// closeTimeout bounds closing a connection whose network is gone.
	closeTimeout = 5 * time.Second
	// connectTimeout bounds one connect and its LISTENs: a database that
	// accepts the connection and never answers would otherwise hold it for
	// minutes.
	connectTimeout = 10 * time.Second
	// A connection quiet for pingEvery is pinged, and dropped when the ping
	// gets no answer within pingTimeout.
	pingEvery   = 30 * time.Second
	pingTimeout = 5 * time.Second
)

// Handler receives one channel's notices. Its methods run on the listener's
// goroutine, one at a time, and must not block: a slow handler holds up every
// channel's notices.
type Handler interface {
	// Up is called once LISTEN holds on a new connection. Notices committed
	// from then on arrive.
	Up()
	// Notify is called with each notice's payload.
	Notify(ctx context.Context, payload string)
	// Down is called when that connection ends. Notices committed before the
	// next Up are lost.
	Down()
}

// Listener holds the process's listen connection.
type Listener struct {
	handlers map[string]Handler
	started  atomic.Bool

	pingEvery, pingTimeout time.Duration
	connectTimeout         time.Duration
	retryMin, retryMax     time.Duration
}

func New() *Listener {
	return &Listener{
		handlers:  map[string]Handler{},
		pingEvery: pingEvery, pingTimeout: pingTimeout, connectTimeout: connectTimeout,
		retryMin: retryMin, retryMax: retryMax,
	}
}

// Handle makes h receive the notices of channel. Call it before Run.
func (l *Listener) Handle(channel string, h Handler) {
	if l.started.Load() {
		panic("pglisten: Handle called after Run")
	}
	if _, dup := l.handlers[channel]; dup {
		panic("pglisten: two handlers for channel " + channel)
	}
	l.handlers[channel] = h
}

// Run receives notices until ctx ends, on its own connection (cfg, the
// pool's), and reconnects after a failure with a pause that doubles up to
// retryMax. The pause starts over only after a connection that stayed up for
// a ping period: one that drops right after LISTEN, as during a failover,
// keeps backing off, since every reconnect ends the streams the handlers
// serve.
func (l *Listener) Run(ctx context.Context, cfg *pgx.ConnConfig) {
	l.started.Store(true)
	channels := l.channels()
	delay := l.retryMin
	for {
		upFor, err := l.listenOnce(ctx, cfg, channels)
		if ctx.Err() != nil {
			return
		}
		if upFor >= l.pingEvery {
			delay = l.retryMin
		}
		logging.Warn(ctx, err, "database listen connection lost", "pg-listen-disconnected",
			slog.String("channels", strings.Join(channels, ",")), slog.Duration("retry_in", delay))
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, l.retryMax)
	}
}

func (l *Listener) channels() []string {
	channels := make([]string, 0, len(l.handlers))
	for ch := range l.handlers {
		channels = append(channels, ch)
	}
	slices.Sort(channels)
	return channels
}

// listenOnce runs one connection until it fails or ctx ends. upFor is how
// long it was listening, zero when a LISTEN never succeeded.
func (l *Listener) listenOnce(ctx context.Context, cfg *pgx.ConnConfig, channels []string) (upFor time.Duration, err error) {
	setupCtx, cancelSetup := context.WithTimeout(ctx, l.connectTimeout)
	defer cancelSetup()
	conn, err := pgx.ConnectConfig(setupCtx, cfg.Copy())
	if err != nil {
		return 0, fmt.Errorf("connect: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		if cerr := conn.Close(closeCtx); cerr != nil && err == nil {
			err = fmt.Errorf("close: %w", cerr)
		}
	}()
	for _, ch := range channels {
		if _, err := conn.Exec(setupCtx, "LISTEN "+pgx.Identifier{ch}.Sanitize()); err != nil {
			return 0, fmt.Errorf("listen %s: %w", ch, err)
		}
	}
	since := time.Now()
	for _, ch := range channels {
		l.handlers[ch].Up()
	}
	defer func() {
		for _, ch := range channels {
			l.handlers[ch].Down()
		}
	}()
	for {
		n, err := l.next(ctx, conn)
		if err != nil {
			return time.Since(since), err
		}
		if h, ok := l.handlers[n.Channel]; ok {
			h.Notify(ctx, n.Payload)
		}
	}
}

// next waits for a notice. A wait alone never notices a peer that vanished
// without closing the socket (a lost database node): the OS gives up on it
// only many minutes later. So a connection quiet for pingEvery is pinged, and
// one that does not answer is given up.
func (l *Listener) next(ctx context.Context, conn *pgx.Conn) (*pgconn.Notification, error) {
	for {
		waitCtx, cancel := context.WithTimeout(ctx, l.pingEvery)
		n, err := conn.WaitForNotification(waitCtx)
		quiet := waitCtx.Err() != nil
		cancel()
		if err == nil {
			return n, nil
		}
		if ctx.Err() != nil || !quiet {
			return nil, fmt.Errorf("wait for notification: %w", err)
		}
		pingCtx, cancel := context.WithTimeout(ctx, l.pingTimeout)
		err = conn.Ping(pingCtx)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("ping a quiet connection: %w", err)
		}
	}
}
