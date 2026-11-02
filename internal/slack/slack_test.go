package slack

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// slackServer answers chat.postMessage with reply and records the request.
func slackServer(t *testing.T, status int, reply string) (*httptest.Server, *http.Request, map[string]any) {
	t.Helper()
	var got http.Request
	body := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, &got, body
}

func TestPostMessageSendsTheMessageAndReturnsItsTS(t *testing.T) {
	srv, req, body := slackServer(t, http.StatusOK, `{"ok":true,"ts":"1700000000.000100"}`)
	ts, err := New("xoxb-test").WithAPIURL(srv.URL).PostMessage(context.Background(),
		Message{Channel: "C123", Text: "hello", ThreadTS: "1699999999.000001"})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if ts != "1700000000.000100" {
		t.Errorf("ts = %q", ts)
	}
	if req.URL.Path != "/chat.postMessage" || req.Method != http.MethodPost {
		t.Errorf("request = %s %s", req.Method, req.URL.Path)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer xoxb-test" {
		t.Errorf("Authorization = %q", got)
	}
	want := map[string]any{"channel": "C123", "text": "hello", "thread_ts": "1699999999.000001",
		"parse": "none", "unfurl_links": false, "unfurl_media": false}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("body[%s] = %v, want %v", k, body[k], v)
		}
	}
}

// A top-level post sends no thread_ts: an empty one is not "no thread" to Slack.
func TestPostMessageWithoutThreadOmitsThreadTS(t *testing.T) {
	srv, _, body := slackServer(t, http.StatusOK, `{"ok":true,"ts":"1.2"}`)
	if _, err := New("xoxb-test").WithAPIURL(srv.URL).PostMessage(context.Background(), Message{Channel: "C1", Text: "x"}); err != nil {
		t.Fatalf("post: %v", err)
	}
	if _, ok := body["thread_ts"]; ok {
		t.Errorf("body has thread_ts: %v", body)
	}
}

// Slack answers a refusal with HTTP 200 and ok:false.
func TestPostMessageRefusalIsAnAPIError(t *testing.T) {
	srv, _, _ := slackServer(t, http.StatusOK, `{"ok":false,"error":"not_in_channel"}`)
	_, err := New("xoxb-test").WithAPIURL(srv.URL).PostMessage(context.Background(), Message{Channel: "C1", Text: "x"})
	if err == nil || err.Error() != "slack: not_in_channel" {
		t.Fatalf("err = %v, want slack: not_in_channel", err)
	}
	if !Permanent(err) {
		t.Error("not_in_channel is not permanent; a retry cannot add the bot to the channel")
	}
}

func TestPermanentLeavesPassingFailuresRetryable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		reply  string
	}{
		{"rate limited", http.StatusTooManyRequests, `{"ok":false,"error":"ratelimited"}`},
		{"server error", http.StatusInternalServerError, ``},
		{"internal error", http.StatusOK, `{"ok":false,"error":"internal_error"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _, _ := slackServer(t, tc.status, tc.reply)
			_, err := New("xoxb-test").WithAPIURL(srv.URL).PostMessage(context.Background(), Message{Channel: "C1", Text: "x"})
			if err == nil {
				t.Fatal("err = nil")
			}
			if Permanent(err) {
				t.Errorf("%v is permanent, want retryable", err)
			}
		})
	}
}

// No error carries the token: errors are logged and reported.
func TestPostMessageErrorsLeaveTheTokenOut(t *testing.T) {
	srv, _, _ := slackServer(t, http.StatusUnauthorized, `{"ok":false,"error":"invalid_auth"}`)
	_, err := New("xoxb-secret-token").WithAPIURL(srv.URL).PostMessage(context.Background(), Message{Channel: "C1", Text: "x"})
	if err == nil || strings.Contains(err.Error(), "xoxb-secret-token") {
		t.Errorf("err = %v", err)
	}
	// The code is kept whatever the HTTP status: a bad token stays permanent.
	if !Permanent(err) {
		t.Errorf("%v is retryable; a retry cannot fix the token", err)
	}
}

func TestEscapeDisarmsLinksAndPings(t *testing.T) {
	if got := Escape("<!channel> a&b <https://evil.example|click>"); got != "&lt;!channel&gt; a&amp;b &lt;https://evil.example|click&gt;" {
		t.Errorf("Escape = %q", got)
	}
	if got := Link("https://app.example.com/x", "<!here> Title"); got != "<https://app.example.com/x|&lt;!here&gt; Title>" {
		t.Errorf("Link = %q", got)
	}
	if got := Quote("one\n<@U1> two\n"); got != "> one\n> &lt;@U1&gt; two" {
		t.Errorf("Quote = %q", got)
	}
	if got := EscapeLine("  Broken\n:white_check_mark: *Resolved*\t<x> "); got != "Broken :white_check_mark: *Resolved* &lt;x&gt;" {
		t.Errorf("EscapeLine = %q, want one line", got)
	}
	if got := Link("https://app.example.com/x", "Title\n*Fake line*"); got != "<https://app.example.com/x|Title *Fake line*>" {
		t.Errorf("Link = %q, want the label on one line", got)
	}
}

func TestUpdateMessageRewritesTheMessage(t *testing.T) {
	srv, req, body := slackServer(t, http.StatusOK, `{"ok":true,"ts":"1700000000.000100"}`)
	if err := New("xoxb-test").WithAPIURL(srv.URL).UpdateMessage(context.Background(), "C123", "1700000000.000100", "new text"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if req.URL.Path != "/chat.update" {
		t.Errorf("path = %s", req.URL.Path)
	}
	want := map[string]any{"channel": "C123", "ts": "1700000000.000100", "text": "new text", "parse": "none"}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("body[%s] = %v, want %v", k, body[k], v)
		}
	}
}

// A message the bot can no longer edit stays that way: no retry.
func TestUpdateMessageOfAGoneMessageIsPermanent(t *testing.T) {
	srv, _, _ := slackServer(t, http.StatusOK, `{"ok":false,"error":"message_not_found"}`)
	err := New("xoxb-test").WithAPIURL(srv.URL).UpdateMessage(context.Background(), "C1", "1.2", "x")
	if !Permanent(err) {
		t.Errorf("err = %v, want a permanent refusal", err)
	}
}

// CardRefused tells a refusal of one message from a failure that may stop
// every call; a code it does not know is the latter.
func TestCardRefusedKnowsOnlyCardAndChannelCodes(t *testing.T) {
	for err, want := range map[error]bool{
		&APIError{Code: "not_in_channel"}:             true,
		&APIError{Code: "is_inactive"}:                true,
		&APIError{Code: "invalid_arguments"}:          true,
		&APIError{Code: "restricted_action"}:          true,
		&APIError{Code: "ekm_access_denied"}:          true,
		&APIError{Code: "external_channel_migrating"}: true,
		&APIError{Code: "team_access_not_granted"}:    true,
		errors.New("connection reset"):                false,
		&APIError{Code: "ratelimited"}:                false,
		&APIError{Code: "invalid_auth"}:               false,
		&APIError{Code: "internal_error"}:             false,
		&APIError{Code: "an_unknown_message_code"}:    false,
	} {
		if got := CardRefused(err); got != want {
			t.Errorf("CardRefused(%v) = %v, want %v", err, got, want)
		}
	}
}
