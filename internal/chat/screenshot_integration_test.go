package chat_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"openloc/internal/chat"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/testutil"
)

// A message names a screenshot by upload id: only the sender's own image is
// taken, the model gets it with that message, and the message keeps a marker
// with the id for later turns.
func TestIntegrationChatScreenshotGoesWithItsMessage(t *testing.T) {
	f := newChatFixture(t, []*chat.Turn{{Text: "I see the empty grid.", StopReason: "end_turn"}})
	png := testutil.PNG("grid")
	mine := f.uploadID(t, "grid.png", png)
	text := f.uploadID(t, "notes.txt", []byte("plain text"))
	rec := f.upload(t, f.ownerB, f.tenantB.ID, "theirs.png", png)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d", rec.Code)
	}
	theirs := strings.Split(strings.Split(rec.Body.String(), `"id":"`)[1], `"`)[0]

	for name, id := range map[string]string{"not an image": text, "another user's": theirs, "made up": "nope"} {
		r := f.do(t, "POST", "/api/chat", fmt.Sprintf(`{"message":"look","screenshot_upload_id":%q}`, id), f.ownerA, f.tenantA.ID)
		if r.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, r.Code, r.Body.String())
		}
	}
	if len(f.llm.seen) != 0 {
		t.Fatalf("a refused screenshot reached the model")
	}

	r := f.do(t, "POST", "/api/chat", fmt.Sprintf(`{"message":"Here is the screenshot.","screenshot_upload_id":%q}`, mine), f.ownerA, f.tenantA.ID)
	if r.Code != http.StatusOK {
		t.Fatalf("send: %d %s", r.Code, r.Body.String())
	}
	msgs := f.llm.seen[0].Messages
	last := msgs[len(msgs)-1]
	if last.Image == nil || string(last.Image.Data) != string(png) || last.Image.MediaType != "image/png" {
		t.Fatalf("the model did not get the screenshot: %+v", last.Image)
	}
	if !strings.Contains(last.Text, "upload_id: "+mine) {
		t.Errorf("the message has no marker: %q", last.Text)
	}
	var stored string
	if err := f.pool.QueryRow(context.Background(),
		`SELECT content FROM chat_messages WHERE role = 'user' ORDER BY id DESC LIMIT 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored, "Here is the screenshot.") || !strings.Contains(stored, mine) {
		t.Errorf("stored message = %q", stored)
	}
	var title string
	if err := f.pool.QueryRow(context.Background(), `SELECT title FROM chat_conversations`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Here is the screenshot." {
		t.Errorf("title = %q, want the user's words without the marker", title)
	}
}

// The report form reads a screenshot back to attach it: the sender's own
// image only.
func TestIntegrationChatUploadImageIsOwnersImageOnly(t *testing.T) {
	f := newChatFixture(t, nil)
	png := testutil.PNG("form")
	mine := f.uploadID(t, "grid.png", png)
	text := f.uploadID(t, "notes.txt", []byte("plain text"))
	get := func(user db.User, tenantID int64, id string) (int, string, http.Header) {
		r := f.do(t, "GET", "/api/chat/uploads/"+id, "", user, tenantID)
		return r.Code, r.Body.String(), r.Header()
	}
	code, body, h := get(f.ownerA, f.tenantA.ID, mine)
	if code != http.StatusOK || body != string(png) || h.Get("Content-Type") != "image/png" ||
		h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("own image: %d %s %v", code, h.Get("Content-Type"), h)
	}
	if code, _, _ := get(f.ownerA, f.tenantA.ID, text); code != http.StatusNotFound {
		t.Errorf("not an image: %d", code)
	}
	if code, _, _ := get(f.ownerB, f.tenantB.ID, mine); code != http.StatusNotFound {
		t.Errorf("another user: %d", code)
	}
}
