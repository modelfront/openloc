package testutil

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// DoRequest sends an authenticated JSON request through the router and returns the recorder.
// If body is nil, no request body is sent.
func DoRequest(t testing.TB, router http.Handler, method, path string, body any, userID, tenantID int64) *httptest.ResponseRecorder {
	t.Helper()

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("DoRequest: failed to marshal body: %v", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	if userID > 0 {
		req.Header.Set("Authorization", AuthHeader(userID, tenantID))
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// DoRequestWithRawToken sends a request with a verbatim bearer token. Used for
// personal access tokens, where the point is to exercise the real
// credential-resolution path rather than a synthesized JWT.
func DoRequestWithRawToken(t testing.TB, router http.Handler, method, path string, body any, rawToken string) *httptest.ResponseRecorder {
	t.Helper()

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("DoRequestWithRawToken: failed to marshal body: %v", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	if rawToken != "" {
		req.Header.Set("Authorization", "Bearer "+rawToken)
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// DoRequestWithRole sends an authenticated JSON request with a specific role.
func DoRequestWithRole(t testing.TB, router http.Handler, method, path string, body any, userID, tenantID int64, role string) *httptest.ResponseRecorder {
	t.Helper()

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("DoRequestWithRole: failed to marshal body: %v", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	if userID > 0 {
		req.Header.Set("Authorization", AuthHeaderWithRole(userID, tenantID, role))
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// DoRequestRaw sends an authenticated request with a raw body (for multipart uploads, etc.).
func DoRequestRaw(t testing.TB, router http.Handler, method, path string, body io.Reader, contentType string, userID, tenantID int64) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if userID > 0 {
		req.Header.Set("Authorization", AuthHeader(userID, tenantID))
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// DoFileUpload sends a multipart file upload request. contentType is not
// sent: the multipart part carries no type of its own.
func DoFileUpload(t testing.TB, router http.Handler, path, filename, contentType, content string, userID, tenantID int64) *httptest.ResponseRecorder {
	t.Helper()
	return DoMultipartUpload(t, router, path, filename, []byte(content), nil, userID, tenantID, "")
}

// DoFileUploadWithRole sends a multipart file upload request with a specific
// role embedded in the JWT. Use this when the route is gated by RequireRole.
func DoFileUploadWithRole(t testing.TB, router http.Handler, path, filename, content string, userID, tenantID int64, role string) *httptest.ResponseRecorder {
	t.Helper()
	return DoMultipartUpload(t, router, path, filename, []byte(content), nil, userID, tenantID, role)
}

// MultipartBody builds a multipart body: content as the `file` field, after
// any extra form fields. The bytes are written as they are, so a binary file
// such as an XLSX stays intact. It returns the body and its Content-Type.
func MultipartBody(t testing.TB, filename string, content []byte, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			t.Fatalf("MultipartBody: write field %s: %v", k, err)
		}
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("MultipartBody: create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("MultipartBody: write file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("MultipartBody: close: %v", err)
	}
	return &buf, writer.FormDataContentType()
}

// DoMultipartUpload posts MultipartBody as role ("" for the default role;
// userID 0 for no token).
func DoMultipartUpload(t testing.TB, router http.Handler, path, filename string, content []byte, fields map[string]string, userID, tenantID int64, role string) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := MultipartBody(t, filename, content, fields)
	req := httptest.NewRequest("POST", path, body)
	req.Header.Set("Content-Type", contentType)
	switch {
	case userID <= 0:
	case role == "":
		req.Header.Set("Authorization", AuthHeader(userID, tenantID))
	default:
		req.Header.Set("Authorization", AuthHeaderWithRole(userID, tenantID, role))
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// DecodeJSON decodes the response body into the given value.
func DecodeJSON(t testing.TB, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(v); err != nil {
		t.Fatalf("DecodeJSON: %v (body: %s)", err, rec.Body.String())
	}
}

// DecodeData decodes the `data` of the response's {"data": …} envelope. A
// body without `data`, such as an error envelope, fails the test.
func DecodeData[T any](t testing.TB, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("DecodeData: %v (status %d, body: %s)", err, rec.Code, rec.Body.String())
	}
	if env.Data == nil {
		t.Fatalf("DecodeData: no data in the %d response: %s", rec.Code, rec.Body.String())
	}
	var out T
	if err := json.Unmarshal(env.Data, &out); err != nil {
		t.Fatalf("DecodeData: %v (status %d, body: %s)", err, rec.Code, rec.Body.String())
	}
	return out
}

// AssertStatus checks that the response has the expected HTTP status code.
func AssertStatus(t testing.TB, rec *httptest.ResponseRecorder, expected int) {
	t.Helper()
	if rec.Code != expected {
		t.Fatalf("expected status %d, got %d (body: %s)", expected, rec.Code, rec.Body.String())
	}
}
