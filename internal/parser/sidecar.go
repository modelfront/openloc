package parser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// SidecarURL is the base URL for the parser sidecar service.
// Set from main.go after config load.
var SidecarURL string

// sidecarClient allows 60s because DOCX/PDF parsing of large files takes
// seconds.
var sidecarClient = &http.Client{Timeout: 60 * time.Second}

// sidecarMaxResponseSize is the maximum response body size from the sidecar (100MB).
const sidecarMaxResponseSize = 100 * 1024 * 1024

// DocxSidecar delegates DOCX parsing to the Python parser sidecar.
type DocxSidecar struct {
	BaseURL string
}

func (d DocxSidecar) Parse(content []byte) ([]Segment, error) {
	return sidecarParse(d.BaseURL, "/parse/docx", content)
}

// Reconstruct sends translations with their chips; the sidecar rebuilds bold,
// italic and links from them.
func (d DocxSidecar) Reconstruct(original []byte, translations map[string]string) ([]byte, error) {
	return sidecarReconstruct(d.BaseURL, "/reconstruct/docx", original, translations)
}

// PdfSidecar delegates PDF parsing to the Python parser sidecar.
// PDF is import-only — Reconstruct always returns an error.
type PdfSidecar struct {
	BaseURL string
}

func (p PdfSidecar) Parse(content []byte) ([]Segment, error) {
	return sidecarParse(p.BaseURL, "/parse/pdf", content)
}

// exportOnlyError marks PdfSidecar as a parser that never writes a file.
func (PdfSidecar) exportOnlyError() {}

func (p PdfSidecar) Reconstruct(_ []byte, _ map[string]string) ([]byte, error) {
	return nil, &ExportError{Msg: "A PDF cannot be exported in its original form. Export as bilingual XLIFF instead."}
}

// sidecarParse sends file content to the sidecar and returns parsed segments.
func sidecarParse(baseURL, path string, content []byte) ([]Segment, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", "upload")
	if err != nil {
		return nil, fmt.Errorf("create multipart: %w", err)
	}
	if _, err := part.Write(content); err != nil {
		return nil, fmt.Errorf("write file content: %w", err)
	}
	writer.Close()

	req, err := http.NewRequest(http.MethodPost, baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := sidecarClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("parser sidecar unavailable: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, sidecarMaxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("read sidecar response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Try to extract error message from JSON response
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("parser sidecar error: %s", errResp.Error)
		}
		return nil, fmt.Errorf("parser sidecar returned status %d", resp.StatusCode)
	}

	var parseResp struct {
		Segments []struct {
			Key      string `json:"key"`
			Text     string `json:"text"`
			RichText string `json:"rich_text"`
			Position int    `json:"position"`
			Context  string `json:"context"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(respBody, &parseResp); err != nil {
		return nil, fmt.Errorf("decode sidecar response: %w", err)
	}

	segments := make([]Segment, len(parseResp.Segments))
	for i, s := range parseResp.Segments {
		segments[i] = Segment{
			Key:      s.Key,
			Text:     s.Text,
			RichText: s.RichText,
			Position: s.Position,
			Context:  s.Context,
		}
	}
	return segments, nil
}

// sidecarReconstruct sends original file + translations to the sidecar for reconstruction.
func sidecarReconstruct(baseURL, path string, original []byte, translations map[string]string) ([]byte, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add original file
	part, err := writer.CreateFormFile("file", "original")
	if err != nil {
		return nil, fmt.Errorf("create multipart file: %w", err)
	}
	if _, err := part.Write(original); err != nil {
		return nil, fmt.Errorf("write original content: %w", err)
	}

	// Add translations as JSON string
	transJSON, err := json.Marshal(translations)
	if err != nil {
		return nil, fmt.Errorf("marshal translations: %w", err)
	}
	if err := writer.WriteField("translations", string(transJSON)); err != nil {
		return nil, fmt.Errorf("write translations field: %w", err)
	}
	writer.Close()

	req, err := http.NewRequest(http.MethodPost, baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := sidecarClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("parser sidecar unavailable: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, sidecarMaxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("read sidecar response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("parser sidecar error: %s", errResp.Error)
		}
		return nil, fmt.Errorf("parser sidecar returned status %d", resp.StatusCode)
	}

	return respBody, nil
}
