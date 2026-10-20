// Package signedtoken makes short-lived tokens that prove the server issued
// them for one exact payload: "<unix expiry>.<base64url HMAC-SHA256>". The
// payload is not in the token; whoever checks it builds the payload again.
// The key is the same on every replica, so any replica can check a token.
package signedtoken

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DeriveKey turns an application secret into the key for one purpose,
// domain-separated from every other key made from the same secret.
func DeriveKey(secret []byte, purpose string) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(purpose))
	return mac.Sum(nil)
}

// RandomKey is the key of a process that has none configured (tests, a
// single dev process): its tokens only verify on that process.
func RandomKey() []byte {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		panic("signedtoken: no randomness for a key: " + err.Error())
	}
	return k
}

// Sign returns a token for payload that expires at exp.
func Sign(key []byte, exp time.Time, payload string) string {
	e := exp.Unix()
	return strconv.FormatInt(e, 10) + "." + base64.RawURLEncoding.EncodeToString(sum(key, e, payload))
}

// Verify reports whether token was signed with key for payload and has not
// expired at now.
func Verify(key []byte, now time.Time, token, payload string) bool {
	expStr, macStr, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || now.Unix() > exp {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(macStr)
	if err != nil {
		return false
	}
	return hmac.Equal(got, sum(key, exp, payload))
}

func sum(key []byte, exp int64, payload string) []byte {
	mac := hmac.New(sha256.New, key)
	fmt.Fprintf(mac, "%d|%s", exp, payload)
	return mac.Sum(nil)
}
