package oauthserver

import "net/http"

// Test seams. This file ends in _test.go so it is compiled ONLY into the test
// binary — the exported helpers below never ship in production, yet the
// external oauthserver_test package can still reach them.

// SetHTTPClientForTest swaps the CIMD fetcher (bypassing the SSRF-guarded
// default dialer) so tests can point it at an httptest server.
func (s *Server) SetHTTPClientForTest(c *http.Client) { s.httpClient = c }

// ConsentNonceAtBucketForTest mints a consent CSRF nonce for a PAST time
// bucket, so the suite can prove a stale nonce is rejected. The nonce is
// time-bucketed and unexported in production.
func (s *Server) ConsentNonceAtBucketForTest(userID int64, clientID string, bucketsAgo int64) string {
	return s.consentNonceAtBucket(userID, clientID, currentBucket()-bucketsAgo)
}
