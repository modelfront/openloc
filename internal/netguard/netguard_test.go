package netguard

import (
	"net"
	"strings"
	"testing"
	"time"
)

func TestIsPublicIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "::1", // loopback
		"10.0.0.5", "172.16.0.1", "192.168.1.1", // RFC 1918
		"169.254.169.254",    // link-local (cloud metadata)
		"fd00::1",            // IPv6 ULA
		"100.64.0.1",         // CGNAT
		"0.0.0.0", "0.1.2.3", // this-network / unspecified
		"224.0.0.1", // multicast
	}
	for _, s := range blocked {
		if IsPublicIP(net.ParseIP(s)) {
			t.Errorf("%s classified public — the guard would allow it", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:2800:220:1::1"} {
		if !IsPublicIP(net.ParseIP(s)) {
			t.Errorf("%s classified non-public — a legitimate host would be blocked", s)
		}
	}
	if IsPublicIP(nil) {
		t.Error("nil IP must not be public")
	}
}

// The dialer refuses a reachable loopback listener before connecting to it.
func TestDialerBlocksPrivate(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			accepted <- struct{}{}
			c.Close()
		}
	}()

	conn, err := Dialer(time.Second).Dial("tcp", ln.Addr().String())
	if err == nil {
		conn.Close()
		t.Fatal("dialer connected to a loopback address")
	}
	if !strings.Contains(err.Error(), "non-public address") {
		t.Fatalf("err = %v, want the non-public refusal", err)
	}
	select {
	case <-accepted:
		t.Fatal("listener accepted a connection")
	case <-time.After(200 * time.Millisecond):
	}
}

// The list must stay net/smtp's: the SMTP sender's startup check and its
// login check both use it, and PlainAuth applies the same one.
func TestIsLocalhost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "127.0.0.1": true, "::1": true,
		"127.0.0.2": false, "LOCALHOST": false, "localhost.example.com": false, "": false,
	} {
		if got := IsLocalhost(host); got != want {
			t.Errorf("IsLocalhost(%q) = %v, want %v", host, got, want)
		}
	}
}
