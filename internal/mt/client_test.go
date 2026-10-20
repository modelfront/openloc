package mt

import (
	"testing"
	"time"
)

func TestHTTPClient_HasTimeout(t *testing.T) {
	if HTTPClient == nil {
		t.Fatal("HTTPClient is nil")
	}
	expected := 5 * time.Minute
	if HTTPClient.Timeout != expected {
		t.Errorf("expected HTTPClient.Timeout = %v, got %v", expected, HTTPClient.Timeout)
	}
}
