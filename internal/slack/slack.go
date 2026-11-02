// Package slack posts and edits messages in a Slack channel with a bot token
// (the Web API's chat.postMessage and chat.update), and escapes user text for
// mrkdwn.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultAPIURL = "https://slack.com/api"
	// requestTimeout bounds one call, connect to last byte.
	requestTimeout = 15 * time.Second
	// maxResponse caps the reply read: chat.postMessage answers with a small
	// JSON object.
	maxResponse = 1 << 20
)

// Client posts messages as one bot. Safe for concurrent use.
type Client struct {
	token  string
	apiURL string
	http   *http.Client
}

// New returns a client for the bot token (xoxb-…).
func New(token string) *Client {
	return &Client{token: token, apiURL: defaultAPIURL, http: &http.Client{Timeout: requestTimeout}}
}

// WithAPIURL points the client at another Web API root, such as a test server.
func (c *Client) WithAPIURL(url string) *Client {
	c.apiURL = strings.TrimRight(url, "/")
	return c
}

// Message is one post. Text is Slack mrkdwn; text from users goes through
// Escape. ThreadTS, when set, posts the message as a reply in that thread.
type Message struct {
	Channel  string
	Text     string
	ThreadTS string
}

// APIError is Slack's refusal of a call: its error code, such as
// "channel_not_found".
type APIError struct {
	Code string
}

func (e *APIError) Error() string { return "slack: " + e.Code }

// Slack's refusals, each code in one list.
var (
	// authCodes refuse the bot itself: every call fails until the token or
	// its scopes change.
	authCodes = map[string]bool{
		"invalid_auth": true, "not_authed": true, "account_inactive": true, "token_revoked": true,
		"token_expired": true, "missing_scope": true,
	}
	// messageGoneCodes: the message can no longer be edited.
	messageGoneCodes = map[string]bool{"message_not_found": true, "cant_update_message": true, "edit_window_closed": true}
	// requestCodes refuse one message or its channel; a retry of the same
	// call cannot fix them, and calls about other messages may still work.
	requestCodes = map[string]bool{
		"channel_not_found": true, "not_in_channel": true, "is_archived": true, "is_inactive": true,
		"restricted_action": true, "ekm_access_denied": true, "external_channel_migrating": true,
		"team_access_not_granted": true, "invalid_arguments": true, "msg_too_long": true,
	}
)

// Permanent reports whether err is a refusal that a retry cannot fix.
func Permanent(err error) bool {
	return hasCode(err, authCodes) || hasCode(err, messageGoneCodes) || hasCode(err, requestCodes)
}

// MessageGone reports whether err says the message itself can no longer be
// edited, whatever the token or channel.
func MessageGone(err error) bool { return hasCode(err, messageGoneCodes) }

// CardRefused reports whether err is a refusal of this one message or its
// channel, which says nothing about calls for other messages. Any other
// failure may stop them all.
func CardRefused(err error) bool { return hasCode(err, requestCodes) }

func hasCode(err error, codes map[string]bool) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && codes[apiErr.Code]
}

type postRequest struct {
	Channel     string `json:"channel"`
	Text        string `json:"text"`
	ThreadTS    string `json:"thread_ts,omitempty"`
	Parse       string `json:"parse"`
	UnfurlLinks bool   `json:"unfurl_links"`
	UnfurlMedia bool   `json:"unfurl_media"`
}

type updateRequest struct {
	Channel string `json:"channel"`
	TS      string `json:"ts"`
	Text    string `json:"text"`
	Parse   string `json:"parse"`
}

type response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	TS    string `json:"ts"`
}

// PostMessage posts m and returns the message's ts, Slack's id for it and
// for the thread it starts. Links and mentions in the text are not expanded,
// and links are not unfurled.
func (c *Client) PostMessage(ctx context.Context, m Message) (string, error) {
	return c.call(ctx, "chat.postMessage",
		postRequest{Channel: m.Channel, Text: m.Text, ThreadTS: m.ThreadTS, Parse: "none"})
}

// UpdateMessage replaces the text of the bot's message ts in channel.
func (c *Client) UpdateMessage(ctx context.Context, channel, ts, text string) error {
	_, err := c.call(ctx, "chat.update", updateRequest{Channel: channel, TS: ts, Text: text, Parse: "none"})
	return err
}

// call runs one Web API method and returns the ts it answers with.
func (c *Client) call(ctx context.Context, method string, payload any) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("slack: encode %s: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+"/"+method, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("slack: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("slack: %s: %w", method, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return "", fmt.Errorf("slack: read response: %w", err)
	}
	var out response
	decodeErr := json.Unmarshal(data, &out)
	switch {
	case decodeErr == nil && !out.OK && out.Error != "":
		return "", &APIError{Code: out.Error}
	case resp.StatusCode != http.StatusOK:
		// 429 (rate limited) and 5xx: the caller's retry tries again later.
		return "", fmt.Errorf("slack: %s: HTTP %d", method, resp.StatusCode)
	case decodeErr != nil:
		return "", fmt.Errorf("slack: decode response: %w", decodeErr)
	case !out.OK || out.TS == "":
		return "", fmt.Errorf("slack: %s: no ts in the response", method)
	}
	return out.TS, nil
}

var escaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// Escape makes s safe inside mrkdwn: it can no longer form a link, a mention
// or a channel ping.
func Escape(s string) string { return escaper.Replace(s) }

// EscapeLine is Escape for text that must stay on one line, such as a name or
// a title: every run of whitespace, line breaks included, becomes one space.
func EscapeLine(s string) string { return Escape(strings.Join(strings.Fields(s), " ")) }

// Link is a link to url labelled with label, which is escaped onto one line.
func Link(url, label string) string {
	return "<" + url + "|" + EscapeLine(label) + ">"
}

// Quote is s as a block quote, every line of it escaped.
func Quote(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, line := range lines {
		lines[i] = "> " + Escape(line)
	}
	return strings.Join(lines, "\n")
}
