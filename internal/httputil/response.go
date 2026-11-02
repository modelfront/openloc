package httputil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// ParseID extracts an int64 URL parameter from a chi route.
func ParseID(r *http.Request, param string) (int64, error) {
	return strconv.ParseInt(chi.URLParam(r, param), 10, 64)
}

// ParseIDList reads a comma-separated list of positive ids from query
// parameter name. Absent or empty → (nil, nil). More than max ids, or any
// entry that is not a positive integer, is an error naming the parameter.
func ParseIDList(r *http.Request, name string, max int) ([]int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > max {
		return nil, fmt.Errorf("%s: at most %d ids", name, max)
	}
	ids := make([]int64, len(parts))
	for i, p := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("%s: %q is not a positive id", name, p)
		}
		ids[i] = id
	}
	return ids, nil
}

func JSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func Error(w http.ResponseWriter, status int, message string) {
	JSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    http.StatusText(status),
			"message": message,
		},
	})
}

// FieldError writes an error that belongs to one form field, so the form can
// show it under that field. field is the request body's JSON key.
func FieldError(w http.ResponseWriter, status int, field, message string) {
	JSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    http.StatusText(status),
			"message": message,
			"field":   field,
		},
	})
}

// maxJSONBodySize limits JSON request bodies to 1 MB.
const maxJSONBodySize = 1 << 20

func Decode(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxJSONBodySize)
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// Data writes a 200 JSON response with {"data": v}.
func Data(w http.ResponseWriter, v any) {
	JSON(w, http.StatusOK, map[string]any{"data": v})
}

// Created writes a 201 JSON response with {"data": v}.
func Created(w http.ResponseWriter, v any) {
	JSON(w, http.StatusCreated, map[string]any{"data": v})
}

// NoContent writes a 204 response with no body.
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// SanitizeFilename removes characters unsafe for Content-Disposition headers.
func SanitizeFilename(name string) string {
	r := strings.NewReplacer(
		"\"", "_",
		"\r", "",
		"\n", "",
		";", "_",
		"/", "_",
		"\\", "_",
	)
	return r.Replace(name)
}

// SetAttachmentFilename writes a Content-Disposition attachment header that
// survives non-ASCII names. A bare filename="…" mangles Unicode (a UTF-8 arrow
// "→" shows up as "â†'" because browsers read the raw bytes as Latin-1), so we
// emit both: an ASCII-only filename= fallback for ancient clients, and an
// RFC 5987, with the real name percent-encoded:
//
//	filename*=UTF-8''<name>
func SetAttachmentFilename(w http.ResponseWriter, name string) {
	safe := SanitizeFilename(name)
	w.Header().Set("Content-Disposition",
		"attachment; filename=\""+asciiFallback(safe)+"\"; filename*=UTF-8''"+rfc5987Escape(safe))
}

// asciiFallback replaces every non-ASCII byte with '_' for the legacy filename=.
func asciiFallback(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] < 0x80 {
			b.WriteByte(s[i])
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// rfc5987Escape percent-encodes a UTF-8 string per RFC 5987's attr-char set.
func rfc5987Escape(s string) string {
	const hex = "0123456789ABCDEF"
	const unreserved = "!#$&+-.^_`|~"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			strings.IndexByte(unreserved, c) >= 0 {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
		}
	}
	return b.String()
}
