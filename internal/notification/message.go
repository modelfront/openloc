package notification

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"strings"
	"time"

	"openloc/internal/emaillayout"
)

// fromName is the display name shown as the email sender, so clients render
// "OpenLoc" rather than the bare mailbox (e.g. noreply@…).
const fromName = "OpenLoc"

// buildMIME assembles an RFC 5322 HTML message.
//
// Date and Message-ID are set here because only some servers add them, and
// mail without them scores as spam. The subject is RFC 2047-encoded, so a
// non-ASCII file name survives and a CR/LF in it cannot start a new header,
// and folded, so a long one stays under the 998-byte line limit strict
// servers enforce. The body is quoted-printable for the same limit.
//
// An email of a thread refers to the thread's id in In-Reply-To and
// References, and the thread's root carries it as its Message-ID, so mail
// apps such as Gmail group the thread's emails (they also need one subject).
func buildMIME(from, to, subject, htmlBody string, thread emaillayout.Thread) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("From: " + fromName + " <" + from + ">\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString(foldHeader("Subject", encodeSubject(subject)))
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	threadID := threadMessageID(thread.ID, from)
	if thread.Root && threadID != "" {
		b.WriteString("Message-ID: " + threadID + "\r\n")
	} else {
		b.WriteString("Message-ID: <" + rand.Text() + "@" + domainOf(from) + ">\r\n")
	}
	if threadID != "" && !thread.Root {
		b.WriteString("In-Reply-To: " + threadID + "\r\n")
		b.WriteString("References: " + threadID + "\r\n")
	}
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("\r\n")

	qp := quotedprintable.NewWriter(&b)
	if _, err := qp.Write([]byte(htmlBody)); err != nil {
		return nil, fmt.Errorf("encode body: %w", err)
	}
	if err := qp.Close(); err != nil {
		return nil, fmt.Errorf("encode body: %w", err)
	}
	return b.Bytes(), nil
}

// buildEmail is the message every sender delivers: body's markup in its
// thread.
func buildEmail(from, to, subject string, body emaillayout.Email) ([]byte, error) {
	return buildMIME(from, to, subject, body.HTML(), body.Thread())
}

// maxHeaderLine is RFC 5322's hard limit on a line, CRLF excluded.
const maxHeaderLine = 998

// encodeSubject RFC 2047-encodes the subject when it needs it: for non-ASCII
// or control characters, and for a run without spaces too long for one line,
// which folding cannot break. mime leaves plain ASCII as it is, so that second
// case is B-encoded here, in chunks that each fit one encoded word.
func encodeSubject(subject string) string {
	enc := mime.QEncoding.Encode("utf-8", subject)
	if enc != subject || !hasUnfoldableRun(subject) {
		return enc
	}
	var words []string
	for s := subject; s != ""; {
		n := min(len(s), 45) // 60 base64 characters, inside the 75 of a word
		words = append(words, "=?utf-8?b?"+base64.StdEncoding.EncodeToString([]byte(s[:n]))+"?=")
		s = s[n:]
	}
	return strings.Join(words, " ")
}

// hasUnfoldableRun reports a run without spaces longer than fits after
// "Subject: " on one line.
func hasUnfoldableRun(subject string) bool {
	for _, run := range strings.Split(subject, " ") {
		if len(run) > maxHeaderLine-len("Subject: ") {
			return true
		}
	}
	return false
}

// foldHeader writes "name: value" with CRLF + space before any word that
// would take the line past 78 characters (RFC 5322 section 2.2.3). It breaks
// only at spaces; encoded words are short and separated by one, so an
// encoded subject always folds.
func foldHeader(name, value string) string {
	const lineLimit = 78
	var b strings.Builder
	b.WriteString(name + ":")
	lineLen, onLine := len(name)+1, 0 // onLine: words on the current line
	for _, word := range strings.Split(value, " ") {
		if onLine > 0 && lineLen+1+len(word) > lineLimit {
			b.WriteString("\r\n")
			lineLen, onLine = 0, 0
		}
		b.WriteString(" " + word)
		lineLen += 1 + len(word)
		onLine++
	}
	b.WriteString("\r\n")
	return b.String()
}

// domainOf returns the domain of an email address, for the Message-ID.
func domainOf(addr string) string {
	if i := strings.LastIndex(addr, "@"); i >= 0 && i < len(addr)-1 {
		return addr[i+1:]
	}
	return "localhost"
}

// threadMessageID is the message id of a thread, "<id@domain>", empty for no
// thread. Characters a message id may not hold become '-', so the id never
// breaks the header.
func threadMessageID(id, from string) string {
	clean := strings.Map(func(r rune) rune {
		if r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-') {
			return r
		}
		return '-'
	}, strings.Trim(id, "."))
	if clean == "" {
		return ""
	}
	return "<" + clean + "@" + domainOf(from) + ">"
}
