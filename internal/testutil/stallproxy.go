package testutil

import (
	"bytes"
	"net"
	"sync"
	"sync/atomic"
	"testing"
)

// StallProxy forwards TCP to a target until Stall: then the connections open
// at that moment stay open but pass no more bytes, like a peer that vanished
// without closing the socket. Later connections forward normally, unless
// StallNew or StallListen is on.
type StallProxy struct {
	ln    net.Listener
	mu    sync.Mutex
	conns []*stallConn
	// stallNew makes connections accepted from now on start stalled;
	// stallListen makes them stall at the client's LISTEN.
	stallNew, stallListen atomic.Bool
}

type stallConn struct {
	client, upstream net.Conn
	stalled          atomic.Bool
	stallAtListen    bool
}

// NewStallProxy listens on a free local port and forwards to target
// ("host:port"); everything closes when the test ends.
func NewStallProxy(t testing.TB, target string) *StallProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &StallProxy{ln: ln}
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			upstream, err := net.Dial("tcp", target)
			if err != nil {
				_ = client.Close()
				continue
			}
			c := &stallConn{client: client, upstream: upstream, stallAtListen: p.stallListen.Load()}
			c.stalled.Store(p.stallNew.Load())
			p.mu.Lock()
			p.conns = append(p.conns, c)
			p.mu.Unlock()
			go c.pipe(upstream, client)
			go c.pipe(client, upstream)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		p.mu.Lock()
		defer p.mu.Unlock()
		for _, c := range p.conns {
			_ = c.client.Close()
			_ = c.upstream.Close()
		}
	})
	return p
}

// Port is the local port the proxy listens on.
func (p *StallProxy) Port() uint16 { return uint16(p.ln.Addr().(*net.TCPAddr).Port) }

// Accepted counts the connections the proxy has taken so far.
func (p *StallProxy) Accepted() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.conns)
}

// Stall stops every connection open now from passing bytes.
func (p *StallProxy) Stall() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		c.stalled.Store(true)
	}
}

// StallNew makes connections accepted from now on pass no bytes at all, or
// forward normally again.
func (p *StallProxy) StallNew(on bool) { p.stallNew.Store(on) }

// StallListen makes connections accepted from now on stop passing bytes at
// the client's LISTEN, or forward normally again.
func (p *StallProxy) StallListen(on bool) { p.stallListen.Store(on) }

// pipe copies src to dst, dropping what it reads once the connection stalls.
func (c *stallConn) pipe(dst, src net.Conn) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 && c.stallAtListen && src == c.client && bytes.Contains(buf[:n], []byte("LISTEN ")) {
			c.stalled.Store(true)
		}
		if n > 0 && !c.stalled.Load() {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}
