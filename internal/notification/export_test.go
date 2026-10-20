package notification

// LiveForTest reports whether the broker's listen connection is up.
func LiveForTest(b *PGBroker) bool { return b.live.Load() }

// isClosed reports whether Close has begun.
func (b *PGBroker) isClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}
