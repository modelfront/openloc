package secrets

import "testing"

func TestEncryptDecryptRoundTrip(t *testing.T) {
	e, err := NewEncryptor("some-key")
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	plaintext := "sk-super-secret-api-key-123"
	ct, err := e.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if ct == plaintext {
		t.Fatal("ciphertext must not equal plaintext")
	}
	got, err := e.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != plaintext {
		t.Errorf("round-trip mismatch: got %q, want %q", got, plaintext)
	}
}

func TestEncryptIsNonDeterministic(t *testing.T) {
	e, _ := NewEncryptor("k")
	a, _ := e.Encrypt("same")
	b, _ := e.Encrypt("same")
	if a == b {
		t.Error("two encryptions of the same plaintext must differ (random nonce)")
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	e1, _ := NewEncryptor("key-one")
	e2, _ := NewEncryptor("key-two")
	ct, _ := e1.Encrypt("secret")
	if _, err := e2.Decrypt(ct); err == nil {
		t.Error("decrypting with the wrong key must fail (auth tag mismatch)")
	}
}

func TestDecryptTamperedFails(t *testing.T) {
	e, _ := NewEncryptor("k")
	ct, _ := e.Encrypt("secret")
	// Flip a character in the base64 ciphertext.
	tampered := []byte(ct)
	if tampered[len(tampered)-2] == 'A' {
		tampered[len(tampered)-2] = 'B'
	} else {
		tampered[len(tampered)-2] = 'A'
	}
	if _, err := e.Decrypt(string(tampered)); err == nil {
		t.Error("decrypting tampered ciphertext must fail")
	}
}

func TestEmptyKeyRejected(t *testing.T) {
	if _, err := NewEncryptor(""); err == nil {
		t.Error("empty key must be rejected")
	}
}
