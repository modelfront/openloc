package mtengine

import (
	"fmt"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
)

// Decryptor decrypts a stored provider key (implemented by *secrets.Encryptor).
type Decryptor interface {
	Decrypt(encoded string) (string, error)
}

// Credential resolves the key and base URL an engine authenticates with.
//
// Two storage shapes, and reading either one alone is wrong for the other. A
// provider engine names a vendor token and leaves `mt_engines.api_key` NULL, so
// the column is empty for every DeepL and Google row. A deployment row carries
// the column and no token. Anything that reads a catalog needs this, not the
// column: an engine handed an empty key falls back to the static table, and a
// static table refuses nothing.
func Credential(eng db.GetMTEngineCredentialRow, d Decryptor) (apiKey, apiURL string, err error) {
	if eng.TokenID == nil {
		return ptr.Deref(eng.ApiKey), ptr.Deref(eng.ApiUrl), nil
	}
	if eng.TokenKeyEncrypted == nil {
		return "", "", fmt.Errorf("key %d no longer exists", *eng.TokenID)
	}
	if d == nil {
		return "", "", fmt.Errorf("no decryptor wired")
	}
	plain, decErr := d.Decrypt(*eng.TokenKeyEncrypted)
	if decErr != nil {
		return "", "", fmt.Errorf("decrypt key %d: %w", *eng.TokenID, decErr)
	}
	return plain, ptr.Deref(eng.TokenBaseUrl), nil
}
