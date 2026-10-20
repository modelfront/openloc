package testutil

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openloc/internal/pglisten"
)

// ListenerConfig is cfg with an application name, which finds the listen
// connection in pg_stat_activity.
func ListenerConfig(cfg *pgx.ConnConfig, name string) *pgx.ConnConfig {
	cfg = cfg.Copy()
	cfg.RuntimeParams["application_name"] = name
	return cfg
}

// RunListener runs l on cfg until the test ends, and waits for it to stop.
func RunListener(t testing.TB, l *pglisten.Listener, cfg *pgx.ConnConfig) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.Run(ctx, cfg)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// TerminateConnection ends, from the server side, the one connection with the
// application name name.
func TerminateConnection(t testing.TB, pool *pgxpool.Pool, name string) {
	t.Helper()
	var killed int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity
		 WHERE datname = current_database() AND application_name = $1`, name).Scan(&killed); err != nil {
		t.Fatal(err)
	}
	if killed != 1 {
		t.Fatalf("terminated %d connections named %q, want 1", killed, name)
	}
}
