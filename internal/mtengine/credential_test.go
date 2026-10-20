package mtengine

import (
	"errors"
	"strings"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
)

type stubDecryptor struct {
	plain string
	err   error
}

func (s stubDecryptor) Decrypt(string) (string, error) { return s.plain, s.err }

// Credential decides where every catalog read gets its key, and each failing
// branch fails OPEN: no key means the static table, which refuses nothing. The
// branches are worth pinning individually because two of them are ordinary
// deployment states rather than corruption — a key rotated out from under a
// row, and an installation with no AI_SECRETS_KEY.
func TestCredential(t *testing.T) {
	id := int64(7)

	for _, tc := range []struct {
		name      string
		row       db.GetMTEngineCredentialRow
		dec       Decryptor
		wantKey   string
		wantURL   string
		wantErrIs string
	}{
		{
			name:    "no token reads the columns",
			row:     db.GetMTEngineCredentialRow{ApiKey: ptr.To("col-key"), ApiUrl: ptr.To("https://lt.test")},
			dec:     stubDecryptor{plain: "unused"},
			wantKey: "col-key",
			wantURL: "https://lt.test",
		},
		{
			name: "no token and no columns is the mock's shape, not an error",
			row:  db.GetMTEngineCredentialRow{},
		},
		{
			name:    "a token is decrypted and brings its own base URL",
			row:     db.GetMTEngineCredentialRow{TokenID: &id, TokenKeyEncrypted: ptr.To("sealed"), TokenBaseUrl: ptr.To("https://gw.test")},
			dec:     stubDecryptor{plain: "real-key"},
			wantKey: "real-key",
			wantURL: "https://gw.test",
		},
		{
			// The key row was deleted while the engine still names it.
			name:      "a token whose key is gone",
			row:       db.GetMTEngineCredentialRow{TokenID: &id},
			dec:       stubDecryptor{plain: "unused"},
			wantErrIs: "no longer exists",
		},
		{
			// A deployment with no AI_SECRETS_KEY: api.go leaves the decryptor
			// nil, so this is a running state rather than dead code.
			name:      "no decryptor wired",
			row:       db.GetMTEngineCredentialRow{TokenID: &id, TokenKeyEncrypted: ptr.To("sealed")},
			dec:       nil,
			wantErrIs: "no decryptor",
		},
		{
			name:      "the key cannot be decrypted",
			row:       db.GetMTEngineCredentialRow{TokenID: &id, TokenKeyEncrypted: ptr.To("sealed")},
			dec:       stubDecryptor{err: errors.New("cipher: message authentication failed")},
			wantErrIs: "decrypt key",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key, url, err := Credential(tc.row, tc.dec)

			if tc.wantErrIs != "" {
				if err == nil {
					t.Fatalf("expected an error naming %q, got key=%q url=%q", tc.wantErrIs, key, url)
				}
				if !strings.Contains(err.Error(), tc.wantErrIs) {
					t.Errorf("error %q does not name %q", err, tc.wantErrIs)
				}
				// Nothing usable escapes a failed resolve, or a caller would
				// authenticate with the empty string against a real endpoint.
				if key != "" || url != "" {
					t.Errorf("a failed resolve returned key=%q url=%q, want both empty", key, url)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if key != tc.wantKey || url != tc.wantURL {
				t.Errorf("got key=%q url=%q, want key=%q url=%q", key, url, tc.wantKey, tc.wantURL)
			}
		})
	}
}

// The decrypted key must come from the token, never from the column: a row can
// carry both, and reading the wrong one authenticates with a stale secret.
func TestCredential_TokenWinsOverTheColumn(t *testing.T) {
	id := int64(3)
	key, url, err := Credential(db.GetMTEngineCredentialRow{
		ApiKey: ptr.To("stale-column-key"), ApiUrl: ptr.To("https://stale.test"),
		TokenID: &id, TokenKeyEncrypted: ptr.To("sealed"), TokenBaseUrl: ptr.To("https://live.test"),
	}, stubDecryptor{plain: "live-key"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "live-key" || url != "https://live.test" {
		t.Errorf("got key=%q url=%q, want the token's", key, url)
	}
}
