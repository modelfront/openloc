package issues

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// A list page's cursor names the order it was made for and holds the last
// row's raw value of every compared column. The next page starts after those
// values, so an issue that changes between two pages neither repeats nor
// hides the rest of the list.
type cursorBody struct {
	Sort   string `json:"s"`
	Values []any  `json:"v"`
}

// encodeCursor is the cursor after row in this order; the client passes it
// back as an opaque string.
func encodeCursor(sig string, order []orderedPart, row issueRow) (string, error) {
	values := make([]any, len(order))
	for i, p := range order {
		values[i] = p.value(row)
	}
	raw, err := json.Marshal(cursorBody{Sort: sig, Values: values})
	if err != nil {
		return "", fmt.Errorf("encode issue cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// decodeCursor reads a cursor from the client into the query's values, one
// per compared column; nil is the first page. A cursor made for another order,
// or not by this server, is refused.
func decodeCursor(raw, sig string, order []orderedPart) ([]any, error) {
	if raw == "" {
		return nil, nil
	}
	invalid := clientError("Invalid cursor. Load the list again.")
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, invalid
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var body cursorBody
	if err := dec.Decode(&body); err != nil || body.Sort != sig || len(body.Values) != len(order) {
		return nil, invalid
	}
	values := make([]any, len(order))
	for i, p := range order {
		v, ok := cursorValue(body.Values[i], p.cast)
		if !ok {
			return nil, invalid
		}
		values[i] = v
	}
	return values, nil
}

// cursorValue is one decoded value as the query parameter for its column's
// type: a time for timestamptz (sent as Unix microseconds), an int64 for
// bigint, a string or nil for text.
func cursorValue(v any, cast string) (any, bool) {
	switch cast {
	case "text":
		if v == nil {
			return nil, true
		}
		s, ok := v.(string)
		return s, ok
	case "bigint", "timestamptz":
		n, ok := v.(json.Number)
		if !ok {
			return nil, false
		}
		i, err := n.Int64()
		if err != nil {
			return nil, false
		}
		if cast == "timestamptz" {
			return time.UnixMicro(i), true
		}
		return i, true
	}
	return nil, false
}
