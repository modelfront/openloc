package signedtoken

import (
	"strings"
	"testing"
	"time"
)

func TestSignVerify(t *testing.T) {
	key := DeriveKey([]byte("secret"), "test purpose v1")
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	exp := now.Add(time.Minute)
	token := Sign(key, exp, "a|1|2")
	if !Verify(key, now, token, "a|1|2") {
		t.Fatal("a fresh token for the same payload did not verify")
	}
	if !Verify(key, exp, token, "a|1|2") {
		t.Error("a token did not verify at its expiry second")
	}
	exp1, mac, _ := strings.Cut(token, ".")
	for name, c := range map[string]struct {
		key     []byte
		now     time.Time
		token   string
		payload string
	}{
		"another payload": {key, now, token, "a|1|3"},
		"expired":         {key, exp.Add(time.Second), token, "a|1|2"},
		"another key":     {DeriveKey([]byte("secret"), "other purpose v1"), now, token, "a|1|2"},
		"later expiry":    {key, now, "9999999999." + mac, "a|1|2"},
		"no dot":          {key, now, exp1 + mac, "a|1|2"},
		"bad expiry":      {key, now, "x." + mac, "a|1|2"},
		"bad signature":   {key, now, exp1 + ".!!", "a|1|2"},
		"empty":           {key, now, "", "a|1|2"},
	} {
		t.Run(name, func(t *testing.T) {
			if Verify(c.key, c.now, c.token, c.payload) {
				t.Errorf("verified %q for %q", c.token, c.payload)
			}
		})
	}
}

func TestDeriveKeySeparatesPurposes(t *testing.T) {
	a := DeriveKey([]byte("secret"), "one v1")
	b := DeriveKey([]byte("secret"), "two v1")
	if string(a) == string(b) {
		t.Error("two purposes got the same key")
	}
	if string(a) != string(DeriveKey([]byte("secret"), "one v1")) {
		t.Error("the same purpose got two keys")
	}
	if len(RandomKey()) != 32 || string(RandomKey()) == string(RandomKey()) {
		t.Error("RandomKey is not 32 fresh bytes")
	}
}
