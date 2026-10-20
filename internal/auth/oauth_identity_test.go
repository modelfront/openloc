package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// fakeIDToken builds an unsigned JWT carrying claims.
func fakeIDToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return enc(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." + enc(claims) + ".sig"
}

func TestDecodeMicrosoftIDToken_VerifiedEmailAccepted(t *testing.T) {
	raw := fakeIDToken(t, map[string]any{"email": "john@example.com", "name": "John P", "xms_edov": true})
	u, err := decodeMicrosoftIDToken(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if u.Email != "john@example.com" || u.Name != "John P" {
		t.Errorf("got %+v", u)
	}
}

func TestDecodeMicrosoftIDToken_UnverifiedEmailRefused(t *testing.T) {
	cases := map[string]map[string]any{
		"xms_edov false":    {"email": "victim@example.com", "xms_edov": false},
		"xms_edov missing":  {"email": "victim@example.com"},
		"no email claim":    {"preferred_username": "victim@example.com", "xms_edov": true},
		"edov as string no": {"email": "victim@example.com", "xms_edov": "0"},
	}
	for name, claims := range cases {
		t.Run(name, func(t *testing.T) {
			if u, err := decodeMicrosoftIDToken(fakeIDToken(t, claims)); err == nil {
				t.Errorf("expected refusal, got %+v", u)
			}
		})
	}
}

func TestDecodeMicrosoftIDToken_Malformed(t *testing.T) {
	for _, raw := range []string{"", "not-a-jwt", "a.!!!.c"} {
		if _, err := decodeMicrosoftIDToken(raw); err == nil {
			t.Errorf("expected error for %q", raw)
		}
	}
}

func TestDecodeGoogleUser_UnverifiedEmailRefused(t *testing.T) {
	if u, err := decodeGoogleUser(strings.NewReader(`{"email":"victim@example.com","verified_email":false,"name":"X"}`)); err == nil {
		t.Errorf("expected refusal, got %+v", u)
	}
	if u, err := decodeGoogleUser(strings.NewReader(`{"email":"victim@example.com","name":"X"}`)); err == nil {
		t.Errorf("expected refusal when verified_email is missing, got %+v", u)
	}
}

func TestDecodeGoogleUser_VerifiedEmailAccepted(t *testing.T) {
	u, err := decodeGoogleUser(strings.NewReader(`{"email":"jane@example.com","verified_email":true,"name":"Jane"}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if u.Email != "jane@example.com" || u.Name != "Jane" {
		t.Errorf("got %+v", u)
	}
}
