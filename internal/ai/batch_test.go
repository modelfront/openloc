package ai

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Shared helpers for the batched-request tests. Both client_test.go
// and predict_test.go drive stub providers that must speak the contract:
// request [{"id",…context keys}] → reply {"results":[{"id",…}]}.

// reqSegment is one segment as sent: the id alongside its context keys, flat.
type reqSegment struct {
	ID string `json:"id"`
	// Everything else, so a test can assert on the keys a prompt chose to send.
	Rest map[string]json.RawMessage `json:"-"`
}

func (s *reqSegment) UnmarshalJSON(b []byte) error {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return err
	}
	if id, ok := all["id"]; ok {
		if err := json.Unmarshal(id, &s.ID); err != nil {
			return err
		}
		delete(all, "id")
	}
	s.Rest = all
	return nil
}

// text is the segment's source text — what a stub provider "translates" and
// echoes into its fake reply.
func (s reqSegment) text() string {
	var str string
	if err := json.Unmarshal(s.Rest["source"], &str); err == nil {
		return str
	}
	return ""
}

// raw is the whole context as JSON, minus the id: for a stub that scripts its
// answer off a key other than the source, and for asserting what a prompt chose
// to send.
func (s reqSegment) raw() string {
	if len(s.Rest) == 0 {
		return ""
	}
	out, err := json.Marshal(s.Rest)
	if err != nil {
		return ""
	}
	return string(out)
}

// decodeRequest pulls the system prompt and the batched segments out of a
// provider request body (OpenAI messages shape, or Claude's system+messages).
func decodeRequest(t *testing.T, body []byte) (system string, segs []reqSegment) {
	t.Helper()
	var req struct {
		System   string `json:"system"`
		Messages []struct {
			Role, Content string
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode request: %v (body %s)", err, body)
	}
	system = req.System
	user := ""
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			system = m.Content
		case "user":
			user = m.Content
		}
	}
	if err := json.Unmarshal([]byte(user), &segs); err != nil {
		t.Fatalf("user message must be a flat [{\"id\",…}] array, got %q (err %v)", user, err)
	}
	return system, segs
}

// jsonBody carries a plain string as a segment body — the shape a prose segment
// prompt renders to. These tests care about size and
// framing, not about the body's structure.
func jsonBody(s string) json.RawMessage {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return b
}

// resultsReply wraps per-segment JSON objects in the response envelope.
func resultsReply(objects ...string) string {
	return `{"results":[` + strings.Join(objects, ",") + `]}`
}

// The user message reads like the editor: one segment per line, and inline
// tags and & as written, not \u003c-escaped.
func TestBatchUserMessage_OneSegmentPerLineWithoutEscapes(t *testing.T) {
	msg, err := batchUserMessage([]batchItem{
		{ID: "1", Content: translateContent(TranslateInput{Text: "See <1>details</1>."})},
		{ID: "2", Content: predictContent(PredictInput{Source: "A & B", Translation: "A & B"})},
	})
	if err != nil {
		t.Fatal(err)
	}
	const want = "[\n" +
		`  {"id":"1","source":"See <1>details</1>."},` + "\n" +
		`  {"id":"2","source":"A & B","translation":"A & B"}` + "\n" +
		"]"
	if msg != want {
		t.Errorf("user message:\n%s\nwant:\n%s", msg, want)
	}
}

// Two and a half batches' worth, derived from the constant so the case stays
// a count split whatever llmBatchSize becomes.
func TestChunkBatches_SplitsByCount(t *testing.T) {
	const remainder = 5
	n := llmBatchSize*2 + remainder
	items := make([]batchItem, n)
	for i := range items {
		items[i] = batchItem{ID: fmt.Sprintf("s%d", i), Content: jsonBody("short")}
	}
	got := chunkBatches(items, batchItemContent)
	if len(got) != 3 {
		t.Fatalf("%d items at batch size %d → want 3 batches, got %d", n, llmBatchSize, len(got))
	}
	if len(got[0]) != llmBatchSize || len(got[1]) != llmBatchSize || len(got[2]) != remainder {
		t.Errorf("want %d/%d/%d, got %d/%d/%d",
			llmBatchSize, llmBatchSize, remainder, len(got[0]), len(got[1]), len(got[2]))
	}
}

func TestChunkBatches_SplitsByCharBudget(t *testing.T) {
	big := strings.Repeat("x", llmBatchCharBudget/2+1)
	items := []batchItem{
		{ID: "1", Content: jsonBody(big)},
		{ID: "2", Content: jsonBody(big)},
		{ID: "3", Content: jsonBody("small")},
	}
	got := chunkBatches(items, batchItemContent)
	if len(got) != 2 {
		t.Fatalf("two over-half-budget items must not share a request; got %d batches", len(got))
	}
	if len(got[0]) != 1 || got[0][0].ID != "1" {
		t.Errorf("first batch should hold only item 1, got %+v", got[0])
	}
}

// A single segment larger than the whole budget must still be sent, alone —
// never silently dropped.
func TestChunkBatches_OversizedSegmentTravelsAlone(t *testing.T) {
	items := []batchItem{
		{ID: "1", Content: jsonBody("small")},
		{ID: "2", Content: jsonBody(strings.Repeat("y", llmBatchCharBudget*2))},
		{ID: "3", Content: jsonBody("small")},
	}
	got := chunkBatches(items, batchItemContent)
	var seen []string
	for _, b := range got {
		for _, it := range b {
			seen = append(seen, it.ID)
		}
	}
	if strings.Join(seen, ",") != "1,2,3" {
		t.Fatalf("every item must survive chunking in order, got %v", seen)
	}
	for _, b := range got {
		if len(b) > 1 {
			for _, it := range b {
				if it.ID == "2" {
					t.Error("the oversized segment must travel in its own request")
				}
			}
		}
	}
}

func TestChunkBatches_Empty(t *testing.T) {
	if got := chunkBatches(nil, batchItemContent); got != nil {
		t.Fatalf("no items → no batches, got %v", got)
	}
}

// Claude requires max_tokens; pins that it scales with the batch so an APE
// batch that returns a postedit per segment is not truncated.
func TestBatchMaxTokens_ScalesWithBatch(t *testing.T) {
	if got := batchMaxTokens(1, false); got != 4096 {
		t.Errorf("single segment should keep the 4096 floor, got %d", got)
	}
	if got := batchMaxTokens(10, false); got != 10240 {
		t.Errorf("10 segments → 10240, got %d", got)
	}
	if got := batchMaxTokens(1000, false); got != 32000 {
		t.Errorf("must cap at 32000, got %d", got)
	}
}

// Thinking shares the max_tokens budget with the answer, so the allowance has
// to grow or a batched reply gets spent on reasoning and truncated
func TestBatchMaxTokens_ThinkingGetsHeadroom(t *testing.T) {
	if got := batchMaxTokens(1, true); got != 8192 {
		t.Errorf("thinking floor should be 8192, got %d", got)
	}
	if got := batchMaxTokens(10, true); got != 20480 {
		t.Errorf("10 thinking segments → 20480, got %d", got)
	}
	if got := batchMaxTokens(1000, true); got != 64000 {
		t.Errorf("thinking cap should be 64000, got %d", got)
	}
	if batchMaxTokens(10, true) <= batchMaxTokens(10, false) {
		t.Error("thinking must never get a smaller budget than non-thinking")
	}
}

func TestParseBatchResults_AcceptsBothShapes(t *testing.T) {
	for name, raw := range map[string]string{
		"results envelope": `{"results":[{"id":"7","target":"a"},{"id":"9","target":"b"}]}`,
		"bare array":       `[{"id":"7","target":"a"},{"id":"9","target":"b"}]`,
		"fenced":           "```json\n{\"results\":[{\"id\":\"7\"},{\"id\":\"9\"}]}\n```",
		"numeric ids":      `{"results":[{"id":7},{"id":9}]}`,
	} {
		got, envelope := parseBatchResults(raw)
		if !envelope {
			t.Errorf("%s: should be recognized as a contract reply", name)
		}
		if _, ok := got["7"]; !ok {
			t.Errorf("%s: missing id 7 in %v", name, got)
		}
		if _, ok := got["9"]; !ok {
			t.Errorf("%s: missing id 9 in %v", name, got)
		}
	}
}

// Prose with no JSON at all is not a contract reply — the caller may then
// salvage a one-segment batch from the raw text.
func TestParseBatchResults_ReportsMissingEnvelope(t *testing.T) {
	got, envelope := parseBatchResults("The translation looks fine to me!")
	if envelope || len(got) != 0 {
		t.Fatalf("want no envelope and no results, got envelope=%v results=%v", envelope, got)
	}
	if _, envelope := parseBatchResults(`{"results":[]}`); !envelope {
		t.Error("an empty results array IS a contract reply — the model deliberately answered nothing")
	}
}

func TestParseBatchResults_DropsUnusableEntries(t *testing.T) {
	got, _ := parseBatchResults(`{"results":[{"target":"no id"},{"id":"","target":"blank"},{"id":"5","target":"ok"}]}`)
	if len(got) != 1 {
		t.Fatalf("only the id-bearing entry is usable, got %v", got)
	}
	if _, ok := got["5"]; !ok {
		t.Errorf("want id 5, got %v", got)
	}
}
