package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"

	db "openloc/internal/database/sqlcgen"
)

// Password auth. bcrypt over argon2id: it's already a transitive
// precedent in this repo (testutil.SeedUser), CompareHashAndPassword is
// constant-time by construction, and there are no memory-tuning knobs to get
// wrong. DefaultCost = 10.

// bcrypt silently truncates input at 72 bytes — longer passwords must be
// rejected, not truncated, or "correct horse battery staple…"+junk would all
// verify as the same password.
const (
	minPasswordLen = 8
	maxPasswordLen = 72
)

// HashPassword bcrypt-hashes a plaintext password for storage.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword reports whether the plaintext matches the stored bcrypt hash.
// Constant-time (bcrypt.CompareHashAndPassword).
func VerifyPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

// ValidatePassword enforces the password policy. The message never echoes the
// value.
func ValidatePassword(password string) error {
	if len(password) < minPasswordLen {
		return fmt.Errorf("password must be at least %d characters", minPasswordLen)
	}
	if len(password) > maxPasswordLen {
		return fmt.Errorf("password must be at most %d characters", maxPasswordLen)
	}
	return nil
}

// userHasPassword reports whether the identity can password-login. OAuth-only
// accounts have a nil (or vestigial empty) hash.
func userHasPassword(u db.User) bool {
	return u.PasswordHash != nil && *u.PasswordHash != ""
}

// dummyPasswordHash is compared against when the account doesn't exist or has
// no password, so /login response timing doesn't reveal account existence.
// The plaintext is a fixed random literal nobody can present: VerifyPassword
// against it always fails, but costs one full bcrypt verify like a real miss.
var dummyPasswordHash = func() string {
	h, err := HashPassword("dummy-timing-equalizer-3c92f1b7a4e85d06")
	if err != nil {
		panic(err) // bcrypt on a constant can only fail if the world is broken
	}
	return h
}()

// equalizeTiming burns one bcrypt verify for code paths with nothing real to
// compare (unknown email, OAuth-only account).
func equalizeTiming(password string) {
	_ = VerifyPassword(dummyPasswordHash, password)
}

// errTokenGen is returned by newAuthToken on entropy failure.
var errTokenGen = errors.New("generate token")

// newAuthToken returns a 32-byte crypto-random URL-safe token — the shared
// shape for password-reset tokens and platform owner invites (same as
// invite.generateToken and the OAuth state nonce). Reset tokens are stored as
// HashPAT(raw) (SHA-256 hex — the PAT pattern applies to any 256-bit secret).
func newAuthToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", errTokenGen
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b), nil
}
