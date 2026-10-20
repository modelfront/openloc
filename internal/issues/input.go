package issues

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"openloc/internal/httputil"
)

const (
	maxTitle = 200
	maxText  = 10000
	// MaxFiles and MaxFileSize bound what one report or one comment carries.
	MaxFiles    = 10
	MaxFileSize = 25 << 20
	// MaxScreenshots and MaxScreenshotSize bound the screenshots the
	// assistant passes on from its conversation to a report.
	MaxScreenshots    = 3
	MaxScreenshotSize = 5 << 20
)

// imageTypes are the sniffed types shown in the page. Every other file is
// served as a download, so an uploaded page or script never runs.
var imageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
	"image/gif":  true,
}

// Upload is one file of a request. Size is what the server measured. Open is
// called once, when the file is stored, so the content streams to storage
// instead of sitting in memory; storage needs a body it can seek.
type Upload struct {
	Name string
	Size int64
	Open func() (io.ReadSeekCloser, error)
}

// BytesUpload is an Upload of data held in memory: an imported file, or a
// test's.
func BytesUpload(name string, data []byte) Upload {
	return Upload{Name: name, Size: int64(len(data)), Open: func() (io.ReadSeekCloser, error) {
		return bytesFile{bytes.NewReader(data)}, nil
	}}
}

type bytesFile struct{ *bytes.Reader }

func (bytesFile) Close() error { return nil }

// checkUploads enforces the count and size limits before anything is stored.
func checkUploads(files []Upload) error {
	if len(files) > MaxFiles {
		return httputil.NewClientError(fmt.Sprintf("Attach at most %d files.", MaxFiles))
	}
	for _, f := range files {
		if f.Size > MaxFileSize {
			return httputil.NewClientError(fmt.Sprintf("%s is too large (max 25 MB).", cleanFilename(f.Name)))
		}
	}
	return nil
}

// cleanFilename keeps the base name only, without path separators or control
// characters, capped in length.
func cleanFilename(raw string) string {
	name := filepath.Base(strings.ReplaceAll(raw, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	if r := []rune(name); len(r) > 255 {
		name = string(r[:255])
	}
	if name == "" || name == "." || name == "/" {
		return "file"
	}
	return name
}

// SniffImage tells the type of data by its bytes, and whether it is an image
// the app shows: a screenshot can be one of these only.
func SniffImage(data []byte) (string, bool) {
	t := http.DetectContentType(data)
	return t, imageTypes[t]
}

// sniff reads the start of a file to tell its type, then rewinds it.
func sniff(r io.ReadSeeker) (string, error) {
	head := make([]byte, 512)
	n, err := io.ReadFull(r, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", err
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return http.DetectContentType(head[:n]), nil
}

// cleanText trims s, turns CRLF line breaks (a browser's form upload sends
// them) into plain ones, and checks it is between min and max characters.
func cleanText(s, field string, minLen, maxLen int) (string, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	n := utf8.RuneCountInString(s)
	if n < minLen {
		return "", httputil.NewClientError(field + " is required.")
	}
	if n > maxLen {
		return "", httputil.NewClientError(fmt.Sprintf("%s is too long (max %d characters).", field, maxLen))
	}
	return s, nil
}

// triage checks the severity and area for an issue of this kind: a bug needs
// a severity, nothing else may have one, and no area means Other.
func triage(kind, severity, area string) (*string, string, error) {
	if area == "" {
		area = AreaOther
	}
	if areaLabels[area] == "" {
		return nil, "", httputil.NewClientError("Pick an area from the list.")
	}
	if kind != KindBug {
		if severity != "" {
			return nil, "", httputil.NewClientError("Only bugs have a severity.")
		}
		return nil, area, nil
	}
	if severityLabels[severity] == "" {
		return nil, "", httputil.NewClientError("Pick a severity: Minor, Major, Critical or Blocker.")
	}
	return &severity, area, nil
}

// Context keys the client may send, and how each is checked. Anything else is
// dropped; the role is set by the server.
var (
	contextIDKeys     = []string{"project_id", "file_id", "job_id", "segment_id"}
	contextStringKeys = map[string]int{"path": 300, "browser": 100, "os": 100, "viewport": 20, "app_version": 64}
)

// ViaAssistant marks a report the AI assistant wrote.
const ViaAssistant = "AI assistant"

// tokenRoutes are app paths whose next segment is a secret (a share link, an
// invite or a password reset). It is replaced, so no secret is stored.
var tokenRoutes = []string{"/shared/charts/", "/invite/", "/reset-password/"}

// buildContext keeps the allowed, well-formed keys of the client's context and
// adds the reporter's role and, when set, how the issue was sent (via). A malformed context is dropped, not refused: it is
// a help for the operators, and the report matters more.
func buildContext(raw, role, via string) ([]byte, error) {
	in := map[string]any{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			in = map[string]any{}
		}
	}
	out := map[string]any{}
	for _, k := range contextIDKeys {
		if id, ok := positiveID(in[k]); ok {
			out[k] = id
		}
	}
	for k, max := range contextStringKeys {
		s, ok := in[k].(string)
		if !ok {
			continue
		}
		if k == "path" {
			s = cleanPath(s)
		}
		s = strings.TrimSpace(s)
		if s == "" || utf8.RuneCountInString(s) > max {
			continue
		}
		out[k] = s
	}
	if role != "" {
		out["role"] = role
	}
	// The form sends the one value it may (a report the assistant wrote);
	// anything else is dropped.
	if via == "" && in["via"] == ViaAssistant {
		via = ViaAssistant
	}
	if via != "" {
		out["via"] = via
	}
	return json.Marshal(out)
}

func positiveID(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		if n > 0 && n == float64(int64(n)) {
			return int64(n), true
		}
	case string:
		if id, err := strconv.ParseInt(n, 10, 64); err == nil && id > 0 {
			return id, true
		}
	}
	return 0, false
}

// cleanPath drops the query string and fragment, and replaces the secret
// segment of a token route.
func cleanPath(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	for _, prefix := range tokenRoutes {
		if strings.HasPrefix(p, prefix) {
			return prefix + ":token"
		}
	}
	return p
}
