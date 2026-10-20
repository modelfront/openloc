package pglisten

import "time"

// SetPingForTest shortens when a quiet connection is pinged and how long the
// ping may take. Call it before Run.
func SetPingForTest(l *Listener, every, timeout time.Duration) {
	l.pingEvery, l.pingTimeout = every, timeout
}

// SetConnectTimeoutForTest shortens how long one connect and its LISTENs may
// take. Call it before Run.
func SetConnectTimeoutForTest(l *Listener, d time.Duration) { l.connectTimeout = d }

// SetRetryForTest shortens the pause between reconnects. Call it before Run.
func SetRetryForTest(l *Listener, min, max time.Duration) { l.retryMin, l.retryMax = min, max }
