package auth

import (
	"net/http/httptest"
	"testing"
)

func TestPATRequestAllowed(t *testing.T) {
	read := &PATInfo{Capability: CapabilityRead}
	write := &PATInfo{Capability: CapabilityWrite}
	unknown := &PATInfo{Capability: "admin"}
	cases := []struct {
		method, path string
		info         *PATInfo
		want         bool
	}{
		{"GET", "/api/projects", read, true},
		{"HEAD", "/api/projects", read, true},
		{"OPTIONS", "/api/projects", read, true},
		{"POST", "/api/projects", read, false},
		{"PUT", "/api/members/1/role", read, false},
		{"PATCH", "/api/projects/1", read, false},
		{"DELETE", "/api/projects/1", read, false},
		{"POST", "/api/chat", read, false},
		{"POST", "/mcp", read, true},
		{"DELETE", "/api/projects/1", write, true},
		{"DELETE", "/api/projects/1", unknown, false},
		{"DELETE", "/api/projects/1", nil, false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		if got := patRequestAllowed(r, c.info); got != c.want {
			t.Errorf("%s %s cap=%v: got %v, want %v", c.method, c.path, c.info, got, c.want)
		}
	}
}
