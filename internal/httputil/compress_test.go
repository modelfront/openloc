package httputil

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	kzstd "github.com/klauspost/compress/zstd"
)

func compressed(t *testing.T, h http.Handler) http.Handler {
	t.Helper()
	return Compress()(h)
}

func serveBody(contentType, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = io.WriteString(w, body)
	})
}

func get(h http.Handler, acceptEncoding string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, enc string, body []byte) string {
	t.Helper()
	var r io.Reader
	switch enc {
	case "gzip":
		gr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r = gr
	case "zstd":
		zr, err := kzstd.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer zr.Close()
		r = zr
	default:
		return string(body)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

var bigJSON = `{"data":[` + strings.Repeat(`{"id":1,"status":"approved"},`, 200) + `{}]}`

func TestCompress_NegotiatesTheEncoding(t *testing.T) {
	h := compressed(t, serveBody("application/json", bigJSON))
	for _, tc := range []struct{ accept, want string }{
		{"gzip, deflate, br, zstd", "zstd"},
		{"gzip", "gzip"},
		{"", ""},
		{"br", ""},
	} {
		rec := get(h, tc.accept)
		if got := rec.Header().Get("Content-Encoding"); got != tc.want {
			t.Errorf("Accept-Encoding %q: Content-Encoding = %q, want %q", tc.accept, got, tc.want)
			continue
		}
		if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
			t.Errorf("Accept-Encoding %q: Vary = %q, want Accept-Encoding", tc.accept, rec.Header().Get("Vary"))
		}
		if got := decode(t, tc.want, rec.Body.Bytes()); got != bigJSON {
			t.Errorf("Accept-Encoding %q: body does not round-trip", tc.accept)
		}
		if tc.want != "" && rec.Body.Len() >= len(bigJSON)/4 {
			t.Errorf("Accept-Encoding %q: %d bytes from %d, want real compression", tc.accept, rec.Body.Len(), len(bigJSON))
		}
	}
}

func TestCompress_LeavesSmallAndBinaryBodiesAlone(t *testing.T) {
	for name, h := range map[string]http.Handler{
		"small json": serveBody("application/json", `{"ok":true}`),
		"docx":       serveBody("application/vnd.openxmlformats-officedocument.wordprocessingml.document", bigJSON),
		"zip":        serveBody("application/zip", bigJSON),
	} {
		rec := get(compressed(t, h), "gzip, zstd")
		if enc := rec.Header().Get("Content-Encoding"); enc != "" {
			t.Errorf("%s: Content-Encoding = %q, want none", name, enc)
		}
	}
}

// An event stream is never compressed, and its headers reach the client as
// soon as the stream starts, not with the first event.
func TestCompress_EventStreamOpensAtOnceUncompressed(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(compressed(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse, ok := StartSSE(w)
		if !ok {
			return
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_ = sse.Event("holds", map[string]string{"pad": strings.Repeat("x", 4096)})
	})))
	defer srv.Close()
	defer close(release)

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept-Encoding", "gzip, zstd")
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableCompression: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("stream headers did not arrive before the first event: %v", err)
	}
	defer resp.Body.Close()
	if enc := resp.Header.Get("Content-Encoding"); enc != "" {
		t.Fatalf("event stream Content-Encoding = %q, want none", enc)
	}
	release <- struct{}{}
	br := bufio.NewReader(resp.Body)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("reading stream: %v", err)
		}
		if line == "event: holds\n" {
			return
		}
	}
}
