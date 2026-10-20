package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// keySlots bounds the provider requests in flight per vendor key across the
// process: every batch of every run with one key waits for one of
// llmConcurrency slots, so concurrent steps and workers cannot multiply the
// load a key sees. Keys are held only as hashes.
var keySlots = &keyLimiter{slots: map[string]chan struct{}{}, size: llmConcurrency}

type keyLimiter struct {
	mu    sync.Mutex
	slots map[string]chan struct{}
	size  int
}

// acquire waits for a slot of cc's key and returns its release; an error
// when ctx ends first.
func (l *keyLimiter) acquire(ctx context.Context, cc callConfig) (func(), error) {
	sum := sha256.Sum256([]byte(cc.provider + "\x00" + cc.baseURL + "\x00" + cc.apiKey))
	key := hex.EncodeToString(sum[:])
	l.mu.Lock()
	ch, ok := l.slots[key]
	if !ok {
		ch = make(chan struct{}, l.size)
		l.slots[key] = ch
	}
	l.mu.Unlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
