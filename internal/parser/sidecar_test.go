package parser

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDocxSidecarParse(t *testing.T) {
	const docxRich = `<g id="1" ctype="bold">Hello</g> World`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/parse/docx" {
			t.Errorf("expected /parse/docx, got %s", r.URL.Path)
		}

		// Verify file was sent
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("no file in request: %v", err)
		}
		content, _ := io.ReadAll(file)
		if string(content) != "fake-docx-bytes" {
			t.Errorf("unexpected file content: %q", string(content))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"segments": []map[string]any{
				{"key": "para:0", "text": "Hello World", "rich_text": docxRich, "position": 0, "context": "body"},
				{"key": "para:1", "text": "Second paragraph", "position": 1, "context": "body"},
			},
		})
	}))
	defer server.Close()

	p := DocxSidecar{BaseURL: server.URL}
	segments, err := p.Parse([]byte("fake-docx-bytes"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(segments) != 2 {
		t.Fatalf("got %d segments, want 2", len(segments))
	}
	if segments[0].Key != "para:0" || segments[0].Text != "Hello World" {
		t.Errorf("segment 0: key=%q text=%q", segments[0].Key, segments[0].Text)
	}
	if segments[0].RichText != docxRich {
		t.Errorf("segment 0 rich text = %q, want %q", segments[0].RichText, docxRich)
	}
	if segments[1].Context != "body" {
		t.Errorf("segment 1 context = %q, want body", segments[1].Context)
	}
}

func TestDocxSidecarReconstruct(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reconstruct/docx" {
			t.Errorf("expected /reconstruct/docx, got %s", r.URL.Path)
		}

		// Verify file was sent
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("no file in request: %v", err)
		}
		content, _ := io.ReadAll(file)
		if string(content) != "original-docx" {
			t.Errorf("unexpected original content: %q", string(content))
		}

		// Verify translations JSON
		trans := r.FormValue("translations")
		var transMap map[string]string
		if err := json.Unmarshal([]byte(trans), &transMap); err != nil {
			t.Fatalf("invalid translations JSON: %v", err)
		}
		if transMap["para:0"] != "Hola Mundo" {
			t.Errorf("translation[para:0] = %q, want Hola Mundo", transMap["para:0"])
		}
		// Chips reach the sidecar as they are; it rebuilds links and formatting from them.
		if want := `Visite <g id="1" ctype="link">el sitio</g>`; transMap["para:1"] != want {
			t.Errorf("translation[para:1] = %q, want %q", transMap["para:1"], want)
		}

		w.Write([]byte("reconstructed-docx-bytes"))
	}))
	defer server.Close()

	p := DocxSidecar{BaseURL: server.URL}
	result, err := p.Reconstruct([]byte("original-docx"), map[string]string{
		"para:0": "Hola Mundo",
		"para:1": `Visite <g id="1" ctype="link">el sitio</g>`,
	})
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if string(result) != "reconstructed-docx-bytes" {
		t.Errorf("result = %q, want reconstructed-docx-bytes", string(result))
	}
}

func TestPdfSidecarParse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/parse/pdf" {
			t.Errorf("expected /parse/pdf, got %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"segments": []map[string]any{
				{"key": "page:0:block:0", "text": "Page one text", "position": 0, "context": "page:0"},
			},
		})
	}))
	defer server.Close()

	p := PdfSidecar{BaseURL: server.URL}
	segments, err := p.Parse([]byte("fake-pdf-bytes"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(segments) != 1 {
		t.Fatalf("got %d segments, want 1", len(segments))
	}
	if segments[0].Key != "page:0:block:0" {
		t.Errorf("segment key = %q", segments[0].Key)
	}
	if segments[0].Context != "page:0" {
		t.Errorf("segment context = %q", segments[0].Context)
	}
}

func TestPdfSidecarReconstructReturnsError(t *testing.T) {
	p := PdfSidecar{BaseURL: "http://unused"}
	_, err := p.Reconstruct(nil, nil)
	if err == nil {
		t.Fatal("expected error for PDF reconstruct")
	}
	// Handlers show this message to the user, so it must be typed and must
	// point at the export that works.
	var ee *ExportError
	if !errors.As(err, &ee) {
		t.Fatalf("err %v must be an *ExportError", err)
	}
	if !strings.Contains(ee.Msg, "bilingual XLIFF") {
		t.Errorf("message must point at the bilingual XLIFF export, got %q", ee.Msg)
	}
}

func TestSidecarParseError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Failed to parse DOCX: corrupt file",
		})
	}))
	defer server.Close()

	p := DocxSidecar{BaseURL: server.URL}
	_, err := p.Parse([]byte("corrupt"))
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if got := err.Error(); got != "parser sidecar error: Failed to parse DOCX: corrupt file" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestSidecarUnavailable(t *testing.T) {
	p := DocxSidecar{BaseURL: "http://localhost:59999"}
	_, err := p.Parse([]byte("test"))
	if err == nil {
		t.Fatal("expected error when sidecar is down")
	}
}
