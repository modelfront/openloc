package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"openloc/internal/jsonutil"
)

// Batched provider requests. N segments travel in one
// request as a flat JSON array and come back as an id-keyed array — one round
// trip and one system-prompt billing per batch rather than per segment.
//
// Both paths (AI-as-MT and QE+APE) share the envelope, the chunking and the
// id-matching + split-and-retry recovery below; only the per-segment output
// fields differ, and those live in client.go / predict.go.

const (
	// llmBatchSize is how many segments travel in one provider request,
	// cutting round trips and prompt-token spend by the same factor.
	//
	// The ceiling is the reply, not the request: APE returns a full postedit
	// per segment, and a batch whose JSON outgrows max_tokens is truncated
	// mid-array and costs a split-and-retry per lost segment. 10 keeps a
	// batch's output comfortably inside one response even for APE.
	llmBatchSize = 10

	// llmBatchCharBudget caps the rendered input characters per request, so a
	// handful of very long segments split into smaller batches instead of
	// blowing the context window. A single segment over the budget travels
	// alone rather than being dropped.
	llmBatchCharBudget = 12000
)

// batchItem is what the two paths hand to the shared batching machinery: a
// stable id to match the response on, and the rendered user-message body.
type batchItem struct {
	ID string
	// Content is the segment's context as a JSON object: the keys the prompt
	// chose, in the order the renderer wrote them. Always an object, so
	// batchUserMessage can splice the id in beside them.
	Content json.RawMessage
}

// chunkBatches splits items into request-sized batches, capping both the count
// (llmBatchSize) and the total content length (llmBatchCharBudget). Document
// order is preserved — the prompts tell the model the segments are consecutive,
// so reordering here would make that a lie.
func chunkBatches[T any](items []T, contentOf func(T) string) [][]T {
	var batches [][]T
	var cur []T
	curChars := 0
	for _, it := range items {
		n := len(contentOf(it))
		// Start a new batch when this item would break either cap. An item
		// over the budget on its own still gets its own batch (never dropped).
		if len(cur) > 0 && (len(cur) >= llmBatchSize || curChars+n > llmBatchCharBudget) {
			batches = append(batches, cur)
			cur, curChars = nil, 0
		}
		cur = append(cur, it)
		curChars += n
	}
	if len(cur) > 0 {
		batches = append(batches, cur)
	}
	return batches
}

// batchUserMessage builds the request body: a flat [{"id", …context keys}]
// array. JSON (rather than delimited text) means a segment containing our
// delimiters can't corrupt the framing.
//
// Each segment is one flat object: its id beside the context keys, with no
// wrapper object and no `content` nesting. Fewer braces for the model to walk,
// and the body reads as the list of things it is being handed. One segment per
// line, so the request reads like the editor's list of segments.
//
// Assembled by splicing rather than re-marshalling, because segmentprompt.Build
// controls key order on purpose (prompt caching turns on the bytes being stable)
// and Go's map marshalling would sort them. Content that is not an object —
// Blank() catches "{}" and null before this — degrades to the id alone rather
// than producing invalid JSON.
func batchUserMessage(items []batchItem) (string, error) {
	var b strings.Builder
	b.WriteString("[\n")
	for i, it := range items {
		if i > 0 {
			b.WriteString(",\n")
		}
		b.WriteString("  ")
		id, err := jsonutil.MarshalNoEscape(it.ID)
		if err != nil {
			return "", err
		}
		b.WriteString(`{"id":`)
		b.Write(id)
		if members := objectMembers(it.Content); len(members) > 0 {
			b.WriteByte(',')
			b.Write(members)
		}
		b.WriteByte('}')
	}
	b.WriteString("\n]")
	return b.String(), nil
}

// objectMembers is the inside of a JSON object — everything between the braces,
// in the order it was written. Empty for anything that is not an object.
func objectMembers(raw json.RawMessage) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return nil
	}
	return bytes.TrimSpace(trimmed[1 : len(trimmed)-1])
}

// promptFormats is the appended half of a system prompt — the output format,
// and nothing about the input.
type promptFormats struct {
	OutputFormat string `json:"output_format"`
}

// batchFormats produces the output instructions appended to every batched
// system prompt. fields is the per-segment field list (path-specific), already
// formatted as the inner object's body.
//
// schemaEnforced drops the half a JSON schema already guarantees — the literal
// shape and the no-markdown plea. It rides on every request, so on a model that
// cannot deviate from the schema it is several hundred tokens of pure cost.
//
// What survives either way is what a schema cannot express: that the reply
// carries exactly one result per input segment with the id echoed back. A
// schema can say `results` is an array of objects with an `id`; it cannot say
// the array must line up with the input. That pairing is how every verdict is
// routed, so it stays in words.
//
// The user message describes itself: a segment arrives as JSON with named keys,
// and a model reads "source" or "tm_matches" without being told what they are.
// Only what the keys CANNOT say stays in words — that each reply must pair with
// an input by id.
func batchFormats(fields string, schemaEnforced bool) promptFormats {
	output := "\n\nReturn exactly one result per input segment, echoing each \"id\" verbatim."
	if !schemaEnforced {
		output += fmt.Sprintf("\n\nReturn ONLY a JSON object:\n{\"results\": [{\"id\": string, %s}]}\nNo markdown, no code fences, no explanations — only the JSON object.", fields)
	}
	return promptFormats{OutputFormat: output}
}

// parseBatchResults pulls the per-segment objects out of a batched reply,
// keyed by id. Tolerates the two shapes models actually produce: the
// documented {"results":[…]} object and a bare top-level array. Entries
// without a usable id are dropped (the caller's retry covers the segment).
//
// envelope reports whether the reply spoke the contract at all. It separates
// "the model answered in our format and left this segment out" (don't guess)
// from "the model ignored the format entirely" (a one-segment batch can still
// be salvaged — see runBatches).
func parseBatchResults(raw string) (results map[string]json.RawMessage, envelope bool) {
	arr := extractResultsArray(raw)
	if arr == nil {
		return nil, false
	}
	out := make(map[string]json.RawMessage, len(arr))
	for _, item := range arr {
		var probe struct {
			ID any `json:"id"`
		}
		if err := json.Unmarshal(item, &probe); err != nil {
			continue
		}
		// Models sometimes echo a numeric id despite the string contract.
		var id string
		switch v := probe.ID.(type) {
		case string:
			id = strings.TrimSpace(v)
		case float64:
			id = fmt.Sprintf("%d", int64(v))
		}
		if id == "" {
			continue
		}
		out[id] = item
	}
	return out, true
}

// extractResultsArray returns the raw per-segment objects from either
// {"results":[…]} (the contract) or a bare […] top-level array.
func extractResultsArray(raw string) []json.RawMessage {
	if obj := extractJSONObject(raw); obj != "" {
		var wrapper struct {
			Results []json.RawMessage `json:"results"`
		}
		if json.Unmarshal([]byte(obj), &wrapper) == nil && wrapper.Results != nil {
			return wrapper.Results
		}
	}
	if arr := extractJSONArray(raw); arr != "" {
		var items []json.RawMessage
		if json.Unmarshal([]byte(arr), &items) == nil {
			return items
		}
	}
	return nil
}

// extractJSONArray slices the first '[' to the last ']' of raw, stripping
// markdown fences and prose wrappers. The object counterpart is
// extractJSONObject (predict.go). Empty when no array is present.
func extractJSONArray(raw string) string {
	s := strings.TrimSpace(raw)
	start := strings.IndexByte(s, '[')
	end := strings.LastIndexByte(s, ']')
	if start < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}

// batchMaxTokens scales Claude's required max_tokens with the batch size. An
// APE batch returns a full postedit per segment, so a fixed cap would silently
// truncate the JSON and lose the tail of the batch.
//
// thinking doubles the allowance because Anthropic's max_tokens caps thinking
// and answer tokens TOGETHER, and on the current models thinking is on unless
// switched off. A cap sized for output alone gets spent on
// reasoning and truncates the results array mid-batch — which then costs a
// split-and-retry per lost segment. Raising a ceiling is free unless it's used.
func batchMaxTokens(batchLen int, thinking bool) int {
	perSegment, min, max := 1024, 4096, 32000
	if thinking {
		perSegment, min, max = 2048, 8192, 64000
	}
	n := perSegment * batchLen
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}
