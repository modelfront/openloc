package notification

import (
	"bytes"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"

	"openloc/internal/emaillayout"
)

func mustBuild(t *testing.T, subject, body string) (*mail.Message, string) {
	t.Helper()
	raw, err := buildMIME("ops@example.com", "john@example.com", subject, body, emaillayout.Thread{})
	if err != nil {
		t.Fatalf("buildMIME: %v", err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, raw)
	}
	return msg, string(raw)
}

func TestBuildMIME(t *testing.T) {
	msg, raw := mustBuild(t, "Welcome", "<p>Hi</p>")
	for _, want := range []string{
		"From: OpenLoc <ops@example.com>\r\n",
		"To: john@example.com\r\n",
		"Subject: Welcome\r\n",
		"MIME-Version: 1.0\r\n",
		"Content-Type: text/html; charset=\"UTF-8\"\r\n",
		"Content-Transfer-Encoding: quoted-printable\r\n",
		"\r\n\r\n<p>Hi</p>",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("MIME missing %q\n---\n%s", want, raw)
		}
	}
	if _, err := msg.Header.Date(); err != nil {
		t.Errorf("Date header: %v", err)
	}
	if id := msg.Header.Get("Message-ID"); !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, "@example.com>") {
		t.Errorf("Message-ID = %q, want <…@example.com>", id)
	}
	msg2, _ := mustBuild(t, "Welcome", "<p>Hi</p>")
	if id2 := msg2.Header.Get("Message-ID"); id2 == msg.Header.Get("Message-ID") {
		t.Errorf("two messages share Message-ID %q", id2)
	}
}

func TestBuildMIME_EncodesNonASCIISubject(t *testing.T) {
	const subject = "Projekt „Übersetzung“ fällig"
	msg, raw := mustBuild(t, subject, "<p>x</p>")
	if strings.Contains(raw, "Übersetzung") {
		t.Errorf("raw headers carry unencoded UTF-8:\n%s", raw)
	}
	got, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || got != subject {
		t.Errorf("decoded Subject = %q (err %v), want %q", got, err, subject)
	}
}

func TestBuildMIME_SubjectCannotAddHeaders(t *testing.T) {
	msg, _ := mustBuild(t, "Hello\r\nBcc: attacker@example.com", "<p>x</p>")
	if bcc := msg.Header.Get("Bcc"); bcc != "" {
		t.Errorf("subject injected a Bcc header: %q", bcc)
	}
}

// Encoding a plain subject only to fold it would make it unreadable in raw
// mail for nothing; only a run too long for one line needs it.
func TestBuildMIME_KeepsFoldableASCIISubjectPlain(t *testing.T) {
	subject := "Workflow completed: " + strings.Repeat("x", 200) + ".docx (de)"
	_, raw := mustBuild(t, subject, "<p>x</p>")
	if strings.Contains(raw, "=?utf-8?") {
		t.Errorf("a foldable ASCII subject was encoded:\n%s", raw)
	}
}

func TestBuildMIME_WrapsLongLines(t *testing.T) {
	body := "<p>" + strings.Repeat("x", 2000) + "</p>"
	msg, raw := mustBuild(t, "Long", body)
	for _, line := range strings.Split(raw, "\r\n") {
		if len(line) > 998 {
			t.Fatalf("line of %d bytes exceeds the 998-byte limit", len(line))
		}
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil || string(decoded) != body {
		t.Errorf("decoded body differs (err %v)", err)
	}
}

// A file name has no length limit, and every CJK character grows to about
// nine bytes when encoded, so a long subject must fold to stay under the
// 998-byte line limit. Unfolded, every subject here passes it.
func TestBuildMIME_FoldsLongSubject(t *testing.T) {
	for name, subject := range map[string]string{
		"non-ASCII":           "You've been assigned to 翻訳 — " + strings.Repeat("翻訳ファイル", 40) + ".docx (ja)",
		"ASCII":               strings.TrimSpace(strings.Repeat("assigned review ", 80)),
		"ASCII without space": "Workflow completed: " + strings.Repeat("x", 1500) + ".docx (de)",
	} {
		t.Run(name, func(t *testing.T) {
			msg, raw := mustBuild(t, subject, "<p>x</p>")
			head, _, _ := strings.Cut(raw, "\r\n\r\n")
			for _, line := range strings.Split(head, "\r\n") {
				if len(line) > 998 {
					t.Errorf("header line of %d bytes, want at most 998: %.60q…", len(line), line)
				}
			}
			got, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
			if err != nil || got != subject {
				t.Errorf("decoded Subject = %q (err %v), want %q", got, err, subject)
			}
		})
	}
}

func threadedHeaders(t *testing.T, thread emaillayout.Thread) mail.Header {
	t.Helper()
	raw, err := buildMIME("ops@example.com", "john@example.com", "Issue #42: Export fails", "<p>x</p>", thread)
	if err != nil {
		t.Fatalf("buildMIME: %v", err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, raw)
	}
	return msg.Header
}

func TestBuildMIME_Thread(t *testing.T) {
	const want = "<issue-42.app.example.com@example.com>"
	root := threadedHeaders(t, emaillayout.Thread{ID: "issue-42.app.example.com", Root: true})
	if got := root.Get("Message-ID"); got != want {
		t.Errorf("root Message-ID = %q, want %q", got, want)
	}
	if got := root.Get("In-Reply-To") + root.Get("References"); got != "" {
		t.Errorf("root refers to %q, want nothing", got)
	}
	reply := threadedHeaders(t, emaillayout.Thread{ID: "issue-42.app.example.com"})
	if got := reply.Get("Message-ID"); got == want || !strings.HasSuffix(got, "@example.com>") {
		t.Errorf("reply Message-ID = %q, want its own id", got)
	}
	if got := reply.Get("In-Reply-To"); got != want {
		t.Errorf("reply In-Reply-To = %q, want %q", got, want)
	}
	if got := reply.Get("References"); got != want {
		t.Errorf("reply References = %q, want %q", got, want)
	}
	none := threadedHeaders(t, emaillayout.Thread{})
	if got := none.Get("In-Reply-To") + none.Get("References"); got != "" {
		t.Errorf("unthreaded email refers to %q", got)
	}
}

func TestBuildMIME_ThreadIDCannotBreakHeaders(t *testing.T) {
	h := threadedHeaders(t, emaillayout.Thread{ID: "issue-1>\r\nBcc: x@evil.test <y"})
	if got := h.Get("Bcc"); got != "" {
		t.Fatalf("thread id injected a Bcc header: %q", got)
	}
	if got := h.Get("References"); got != "<issue-1---Bcc--x-evil.test--y@example.com>" {
		t.Errorf("References = %q", got)
	}
}
