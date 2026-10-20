// The capture rules, pinned on the HTTP requests the real LLM adapters send:
// no model input ever carries the action trail or a watch-mode recording, and
// none carries more than one image. The vendor is a local fake that records
// every request body and answers from a script.
package chat_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"openloc/internal/chat"
	"openloc/internal/issues"
	"openloc/internal/testutil"
)

const (
	trailMarker     = "trail-marker-7f3a"
	recordingMarker = "recording-marker-9c1e"
)

// fakeVendor speaks the Anthropic Messages, OpenAI Chat Completions and
// OpenAI Responses streaming APIs. Each request takes the next scripted
// answer: "" for a short text, else a tool call to that tool with toolArgs.
type fakeVendor struct {
	mu       sync.Mutex
	bodies   []string
	script   []string
	toolArgs string
	// refuseImages answers 400 to a request that carries a PNG, as a vendor
	// does for a model that cannot take images.
	refuseImages bool
}

// pngBase64 is how a PNG starts once base64-encoded in a request body.
const pngBase64 = "iVBORw0KGg"

func (v *fakeVendor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	v.mu.Lock()
	v.bodies = append(v.bodies, string(raw))
	if v.refuseImages && strings.Contains(string(raw), pngBase64) {
		v.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"This model does not support image input."}}`)
		return
	}
	tool := ""
	if len(v.script) > 0 {
		tool, v.script = v.script[0], v.script[1:]
	}
	v.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	write := func(event, data string) {
		if event != "" {
			fmt.Fprintf(w, "event: %s\n", event)
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
	}
	args, _ := json.Marshal(v.toolArgs)
	switch r.URL.Path {
	case "/v1/messages":
		write("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[],"stop_reason":null,"usage":{"input_tokens":1,"output_tokens":0}}}`)
		stop := "end_turn"
		if tool == "" {
			write("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
			write("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Noted."}}`)
		} else {
			stop = "tool_use"
			write("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"`+tool+`","input":{}}}`)
			write("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":`+string(args)+`}}`)
		}
		write("content_block_stop", `{"type":"content_block_stop","index":0}`)
		write("message_delta", `{"type":"message_delta","delta":{"stop_reason":"`+stop+`"},"usage":{"output_tokens":2}}`)
		write("message_stop", `{"type":"message_stop"}`)
	case "/v1/chat/completions":
		if tool == "" {
			write("", `{"choices":[{"delta":{"content":"Noted."},"finish_reason":"stop"}]}`)
		} else {
			write("", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"`+tool+`","arguments":`+string(args)+`}}]},"finish_reason":"tool_calls"}]}`)
		}
		write("", "[DONE]")
	case "/v1/responses":
		if tool == "" {
			write("", `{"type":"response.output_text.delta","delta":"Noted."}`)
		} else {
			item := `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"` + tool + `","arguments":` + string(args) + `}`
			write("", `{"type":"response.output_item.added","item":`+item+`}`)
			write("", `{"type":"response.output_item.done","item":`+item+`}`)
		}
		write("", `{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`)
	default:
		http.NotFound(w, r)
	}
}

func (v *fakeVendor) requests() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.bodies...)
}

func (f *chatFixture) uploadID(t *testing.T, name string, data []byte) string {
	t.Helper()
	rec := f.upload(t, f.ownerA, f.tenantA.ID, name, data)
	var env struct {
		Data struct{ ID string } `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Data.ID == "" {
		t.Fatalf("upload %s: %d %s", name, rec.Code, rec.Body.String())
	}
	return env.Data.ID
}

func TestIntegrationChatCaptureRulesOnAdapterRequests(t *testing.T) {
	adapters := map[string]func(url string) chat.LLM{
		"anthropic":        func(url string) chat.LLM { return chat.NewAnthropic("k", url, "claude-test") },
		"chat completions": func(url string) chat.LLM { return chat.NewOpenAI("k", url, "gpt-test") },
		"responses":        func(url string) chat.LLM { return chat.NewOpenAIResponses("k", url, "gpt-test") },
	}
	for name, newLLM := range adapters {
		t.Run(name, func(t *testing.T) {
			f := newChatFixture(t, nil)
			svc := f.wireIssues()

			// An issue whose report carried a trail and a recording, and the
			// user on its page while they talk to the assistant.
			issue, err := svc.Create(context.Background(), issues.Member{TenantID: f.tenantA.ID, UserID: f.ownerA.ID, Role: "owner"},
				issues.NewIssue{Kind: "bug", Severity: "major", Title: "Export fails", Description: "It does.",
					Trail: `[{"at":"2026-10-16T10:00:00Z","kind":"click","target":"` + trailMarker + `"}]`})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(context.Background(),
				`UPDATE issue_captures SET recording = $2 WHERE issue_id = $1`, issue.ID,
				`{"events":[{"kind":"input","value":"`+recordingMarker+`"}]}`); err != nil {
				t.Fatal(err)
			}

			vendor := &fakeVendor{
				script:   []string{"", "openloc_report_issue", ""},
				toolArgs: `{"kind":"bug","severity":"major","area":"editor","title":"Grid is empty","description":"It is.","verification":"verified","verification_note":"The second screenshot shows an empty grid."}`,
			}
			srv := httptest.NewServer(vendor)
			t.Cleanup(srv.Close)
			f.handler.SetLLMFactory(func(_, _, _, _ string) chat.LLM { return newLLM(srv.URL) })

			a, b := testutil.PNG("A"), testutil.PNG("B")
			idA, idB := f.uploadID(t, "a.png", a), f.uploadID(t, "b.png", b)
			page := fmt.Sprintf("/issues/%d", issue.ID)
			send := func(body string) int64 {
				rec := f.do(t, "POST", "/api/chat", body, f.ownerA, f.tenantA.ID)
				events := sseEvents(t, rec.Body.String())
				for _, ev := range events {
					if ev.Name == "error" {
						t.Fatalf("turn failed: %v", ev.Data)
					}
				}
				conv := turnOf(events).conversationID
				if conv == 0 {
					t.Fatalf("no done event: %s", rec.Body.String())
				}
				return conv
			}
			conv := send(fmt.Sprintf(`{"message":"Here is the first screenshot.","screenshot_upload_id":%q,"page_path":%q}`, idA, page))
			send(fmt.Sprintf(`{"conversation_id":%d,"message":"And the second one. Please report it.","screenshot_upload_id":%q,"page_path":%q,"visible_anchors":["chat.launcher"]}`, conv, idB, page))

			reqs := vendor.requests()
			if len(reqs) != 3 {
				t.Fatalf("the vendor got %d requests, want 3", len(reqs))
			}
			encA, encB := base64.StdEncoding.EncodeToString(a), base64.StdEncoding.EncodeToString(b)
			for i, body := range reqs {
				if strings.Contains(body, trailMarker) || strings.Contains(body, recordingMarker) {
					t.Errorf("request %d carries the trail or the recording", i+1)
				}
				if n := strings.Count(body, encA) + strings.Count(body, encB); n > 1 {
					t.Errorf("request %d carries %d images", i+1, n)
				}
			}
			if !strings.Contains(reqs[0], encA) {
				t.Error("the first turn's request does not carry its screenshot")
			}
			for i, body := range reqs[1:] {
				if strings.Contains(body, encA) || strings.Count(body, encB) != 1 {
					t.Errorf("second turn, request %d: want screenshot B alone", i+1)
				}
				if !strings.Contains(body, idA) {
					t.Errorf("second turn, request %d: the first screenshot's marker is missing", i+1)
				}
			}
		})
	}
}

// A vendor that refuses images (a 4xx through each real adapter's error
// mapping) gets the same turn again with the screenshot as its text note,
// and the turn still answers.
func TestIntegrationChatImageRefusalRetriesWithoutTheImage(t *testing.T) {
	adapters := map[string]func(url string) chat.LLM{
		"anthropic":        func(url string) chat.LLM { return chat.NewAnthropic("k", url, "claude-test") },
		"chat completions": func(url string) chat.LLM { return chat.NewOpenAI("k", url, "gpt-test") },
		"responses":        func(url string) chat.LLM { return chat.NewOpenAIResponses("k", url, "gpt-test") },
	}
	for name, newLLM := range adapters {
		t.Run(name, func(t *testing.T) {
			f := newChatFixture(t, nil)
			vendor := &fakeVendor{refuseImages: true, script: []string{""}}
			srv := httptest.NewServer(vendor)
			t.Cleanup(srv.Close)
			f.handler.SetLLMFactory(func(_, _, _, _ string) chat.LLM { return newLLM(srv.URL) })

			id := f.uploadID(t, "grid.png", testutil.PNG("grid"))
			rec := f.do(t, "POST", "/api/chat",
				fmt.Sprintf(`{"message":"The grid is empty.","screenshot_upload_id":%q}`, id), f.ownerA, f.tenantA.ID)
			answered := false
			for _, ev := range sseEvents(t, rec.Body.String()) {
				if ev.Name == "error" {
					t.Fatalf("the turn failed instead of retrying: %v", ev.Data)
				}
				answered = answered || ev.Name == "done"
			}
			reqs := vendor.requests()
			if !answered || len(reqs) != 2 {
				t.Fatalf("answered %v after %d requests, want 2: %s", answered, len(reqs), rec.Body.String())
			}
			if !strings.Contains(reqs[0], pngBase64) {
				t.Error("the first request did not carry the screenshot")
			}
			if strings.Contains(reqs[1], pngBase64) || !strings.Contains(reqs[1], "cannot view images") {
				t.Errorf("the retry still carries the image or lacks its note: %s", reqs[1])
			}
		})
	}
}
