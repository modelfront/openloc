// Package jsonutil holds JSON helpers shared across packages.
package jsonutil

import (
	"bytes"
	"encoding/json"
)

// MarshalNoEscape is json.Marshal without HTML escaping: <, > and & stay as
// written instead of becoming \u003c-style escapes. Use it for JSON a model or
// a person reads, never for JSON embedded in an HTML page.
func MarshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
