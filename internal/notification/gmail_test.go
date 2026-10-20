package notification

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestNormalizePrivateKey(t *testing.T) {
	pem := "-----BEGIN PRIVATE KEY-----\nMIIabc\n-----END PRIVATE KEY-----\n" // gitleaks:allow

	// Raw PEM passes through.
	if got, err := normalizePrivateKey(pem); err != nil || string(got) != strings.TrimSpace(pem) {
		t.Errorf("raw PEM: got %q err %v", got, err)
	}
	// Literal "\n" escapes are converted to real newlines.
	escaped := strings.ReplaceAll(strings.TrimSpace(pem), "\n", "\\n")
	if got, err := normalizePrivateKey(escaped); err != nil || !strings.Contains(string(got), "\nMIIabc\n") {
		t.Errorf("escaped PEM not unescaped: got %q err %v", got, err)
	}
	// Base64-encoded PEM is decoded.
	b64 := base64.StdEncoding.EncodeToString([]byte(pem))
	if got, err := normalizePrivateKey(b64); err != nil || !strings.Contains(string(got), "BEGIN PRIVATE KEY") {
		t.Errorf("base64 PEM: got %q err %v", got, err)
	}
	// Empty / non-PEM errors.
	if _, err := normalizePrivateKey(""); err == nil {
		t.Error("expected error for empty key")
	}
	if _, err := normalizePrivateKey("Zm9vYmFy"); err == nil { // base64 of "foobar", no PEM
		t.Error("expected error for base64 that isn't a PEM block")
	}
}

func TestNewGmailSender_RequiresFields(t *testing.T) {
	// Missing impersonated mailbox (NOTIFICATION_EMAIL).
	if _, err := NewGmailSender(nil, "sa@proj.iam.gserviceaccount.com", testPEM, ""); err == nil {
		t.Error("expected error when NOTIFICATION_EMAIL is empty")
	}
	// Missing serviceClient (NOTIFICATION_SERVICE_CLIENT_ID).
	if _, err := NewGmailSender(nil, "", testPEM, "ops@example.com"); err == nil {
		t.Error("expected error when NOTIFICATION_SERVICE_CLIENT_ID is empty")
	}
}

func TestXOAUTH2Start(t *testing.T) {
	mech, resp, err := xoauth2Auth{user: "ops@example.com", token: "tok123"}.Start(nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if mech != "XOAUTH2" {
		t.Errorf("mechanism = %q, want XOAUTH2", mech)
	}
	// SASL XOAUTH2: "user=<email>^Aauth=Bearer <token>^A^A" (^A = 0x01).
	want := "user=ops@example.com\x01auth=Bearer tok123\x01\x01"
	if string(resp) != want {
		t.Errorf("response = %q, want %q", resp, want)
	}
}
