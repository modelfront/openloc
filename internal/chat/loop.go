package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"openloc/internal/logging"
	"openloc/internal/mcp"
)

// Loop caps. maxIterations bounds model calls per user message: each
// iteration is one model call, and tool-using answers typically need 2-4.
// Eight is generous headroom without letting a confused model burn a budget.
const (
	maxIterations    = 8
	defaultMaxTokens = 4096
	// maxToolResultChars truncates one tool result before it re-enters the
	// model context. list_segments pages at 50 rows; this fits that with room
	// while keeping a pathological result from flooding the context window.
	maxToolResultChars = 32_000
	// maxHistoryMessages bounds how much persisted history replays per turn.
	maxHistoryMessages = 30
)

// Event is one streamed occurrence the UI renders as it happens.
type Event struct {
	Type string `json:"type"` // "text" | "tool_start" | "tool_end" | "client_action" | "confirm_required"
	Text string `json:"text,omitempty"`
	Tool string `json:"tool,omitempty"`
	OK   bool   `json:"ok,omitempty"`
	// Client actions: the model asked the BROWSER to do something.
	// Fire-and-forget — the loop synthesizes a success result and moves on;
	// the panel executes the action when the event arrives.
	Action string `json:"action,omitempty"` // "navigate" | "render_chart" | "highlight" | "tour"
	Path   string `json:"path,omitempty"`
	// Anchor and Caption drive action="highlight"; Steps drives "tour"
	// (guide.go). Anchors are validated against the page registry.
	Anchor  string     `json:"anchor,omitempty"`
	Caption string     `json:"caption,omitempty"`
	Steps   []TourStep `json:"steps,omitempty"`
	// Replies are the one-click answers for action="suggest_replies".
	Replies []string `json:"replies,omitempty"`
	// Reason is the line next to a pause tool's button (ask_screenshot,
	// ask_repeat; pause.go).
	Reason string `json:"reason,omitempty"`
	// Playbook names the saved tour for action="playbook_tour".
	Playbook string `json:"playbook,omitempty"`
	// Chart is the validated chart spec for action="render_chart".
	// Already normalized server-side; the panel renders
	// it verbatim.
	Chart json.RawMessage `json:"chart,omitempty"`
	// Confirm carries a guarded tool's preview for type="confirm_required"
	// (confirm.go). Only the browser sees its token, never the model.
	Confirm *ConfirmRequest `json:"confirm,omitempty"`
}

// chartToolName is the chat-layer chart tool. Like
// navigate_to it acts on the user's browser: the loop validates the spec,
// emits it as a client action, and synthesizes success. Pair it with the
// openloc_query_metrics MCP tool: query first, then chart the rows.
const chartToolName = "render_chart"

// chartMaxPoints bounds the data a chart can carry. Aggregates land far
// below this; hitting it means the model should re-query at a coarser grain.
const chartMaxPoints = 200

var chartTypes = map[string]bool{"line": true, "bar": true, "area": true, "pie": true}

func chartToolDef() ToolDef {
	return ToolDef{
		Name: chartToolName,
		Description: "Render a chart inline in the chat from rows you already have (typically " +
			"the result of openloc_query_metrics). Use line/area for time series, bar for " +
			"category comparisons, pie only for shares of a small whole. x_key names the " +
			"category/time field in the rows; series lists the numeric fields to plot. Keep " +
			"data as the rows themselves — do not re-shape or round them. To break ONE number " +
			"down by a category (words by kind, by engine, by language), set series_key to that " +
			"field and give series a single numeric field: each category value becomes its own " +
			"band, stacked on area/bar. The chart renders " +
			"immediately; summarize the takeaway in one sentence rather than repeating the data.",
		InputSchema: json.RawMessage(`{
		"type": "object",
		"properties": {
			"title": {"type": "string", "description": "short chart title"},
			"type": {"type": "string", "enum": ["line", "bar", "area", "pie"]},
			"x_key": {"type": "string", "description": "field of each row used as the x axis (or pie label)"},
			"series": {"type": "array", "items": {"type": "string"}, "description": "numeric row fields to plot, 1-5. With series_key set, give exactly one."},
			"series_key": {"type": "string", "description": "optional: a CATEGORY field whose values each become a series (e.g. kind, mt_engine, target_language). Use it when the rows repeat the same x with different categories. Must not equal x_key."},
			"data": {"type": "array", "items": {"type": "object"}, "description": "the rows, max 200"}
		},
		"required": ["type", "x_key", "series", "data"]
	}`),
	}
}

// executeRenderChart validates the spec and emits it as a client action.
// Validation errors go back to the model, which reads and corrects.
func executeRenderChart(args json.RawMessage, onEvent func(Event)) (content string, isError bool) {
	var in struct {
		Title     string           `json:"title"`
		Type      string           `json:"type"`
		XKey      string           `json:"x_key"`
		Series    []string         `json:"series"`
		SeriesKey string           `json:"series_key,omitempty"`
		Data      []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return fmt.Sprintf("invalid chart arguments: %v", err), true
	}
	if !chartTypes[in.Type] {
		return fmt.Sprintf("unknown chart type %q; use line, bar, area or pie", in.Type), true
	}
	if in.XKey == "" {
		return "x_key is required", true
	}
	if len(in.Series) == 0 || len(in.Series) > 5 {
		return "series must list 1-5 numeric fields", true
	}
	// series_key splits ONE number by a category, so a second value column has
	// nowhere to go — saying so beats silently plotting only the first.
	if in.SeriesKey != "" {
		if in.SeriesKey == in.XKey {
			return "series_key must differ from x_key — it names the field to split into bands, not the axis", true
		}
		if len(in.Series) != 1 {
			return "with series_key set, series must name exactly one numeric field (the number being split)", true
		}
	}
	if len(in.Data) == 0 {
		return "data is empty — query the metric first, then chart its rows", true
	}
	if len(in.Data) > chartMaxPoints {
		return fmt.Sprintf("data has %d rows, max %d — re-query at a coarser grain (e.g. month instead of day)", len(in.Data), chartMaxPoints), true
	}
	if len(in.Title) > 120 {
		in.Title = in.Title[:120]
	}
	spec, err := json.Marshal(in)
	if err != nil {
		return fmt.Sprintf("chart spec not serializable: %v", err), true
	}
	onEvent(Event{Type: "client_action", Action: "render_chart", Tool: chartToolName, Chart: spec})
	return "chart rendered in the chat", false
}

// ToolCallSummary is what persists per executed call — UI chips, not replay.
type ToolCallSummary struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
}

// Result is the completed assistant turn.
type Result struct {
	Text         string
	ToolCalls    []ToolCallSummary
	InputTokens  int64
	OutputTokens int64
}

// toolSession is the slice of an MCP client session the loop needs; narrowed
// for the fake in tests.
type toolSession interface {
	ListTools(ctx context.Context, params *sdk.ListToolsParams) (*sdk.ListToolsResult, error)
	CallTool(ctx context.Context, params *sdk.CallToolParams) (*sdk.CallToolResult, error)
}

// RunOptions are the switches of one turn.
type RunOptions struct {
	// AllowWrites offers the write tools.
	AllowWrites bool
	// SavedTours lets start_tour run a playbook's saved tour. They run in the
	// Playground, so it is off when the Playground is.
	SavedTours bool
	// IssueReports: the deployment takes issue reports, so the playbook that
	// writes one is offered.
	IssueReports bool
	// PagePath is the page the user is on (a hint from the browser). The
	// pause tools are not offered on the support team's pages.
	PagePath string
}

// Run drives the agent loop: model → tools → model, until the model stops
// asking for tools or the iteration cap trips. onEvent receives streaming
// text deltas and tool lifecycle markers in emission order.
func Run(ctx context.Context, llm LLM, session toolSession, system SystemPrompt, history []Message, opts RunOptions, onEvent func(Event)) (*Result, error) {
	tools, err := listTools(ctx, session, opts.AllowWrites)
	if err != nil {
		return nil, fmt.Errorf("chat: list tools: %w", err)
	}
	// Chat-layer client tools ride alongside the MCP set.
	tools = append(tools, navigateToolDef(), chartToolDef(), highlightToolDef(), tourToolDef(opts.SavedTours), getPlaybookToolDef(opts.IssueReports), repliesToolDef())
	pauses := pauseTools(opts)
	tools = append(tools, pauses...)

	msgs := oneImage(history)
	res := &Result{}

	for i := 0; i < maxIterations; i++ {
		req := Request{
			System:         system.Stable,
			SystemVolatile: system.Volatile,
			Messages:       msgs,
			Tools:          tools,
			MaxTokens:      defaultMaxTokens,
		}
		stream := func(req Request) (*Turn, error) {
			return llm.Stream(ctx, req, func(delta string) {
				onEvent(Event{Type: "text", Text: delta})
			})
		}
		turn, err := stream(req)
		// A model that takes no images refuses the request: the turn goes on
		// with the text marker in the image's place.
		if err != nil && hasImage(msgs) && imageRefused(err) {
			logging.Warn(ctx, err, "the model refused the screenshot; retrying with a text marker", "chat-image-refused")
			msgs = withoutImages(msgs)
			req.Messages = msgs
			turn, err = stream(req)
		}
		if err != nil {
			return nil, err
		}
		res.InputTokens += turn.InputTokens
		res.OutputTokens += turn.OutputTokens
		if turn.Text != "" {
			res.Text += turn.Text
		}

		if turn.StopReason != StopToolUse || len(turn.ToolCalls) == 0 {
			return res, nil
		}

		// Thread the assistant's tool request and our results back into the
		// conversation, then go around again.
		msgs = append(msgs, Message{Role: "assistant", Text: turn.Text, ToolCalls: turn.ToolCalls})
		results := make([]ToolResult, 0, len(turn.ToolCalls))
		paused := false
		for _, tc := range turn.ToolCalls {
			onEvent(Event{Type: "tool_start", Tool: tc.Name})
			var content string
			var isErr bool
			switch tc.Name {
			case navigateToolName:
				content, isErr = executeNavigate(tc.Args, onEvent)
			case chartToolName:
				content, isErr = executeRenderChart(tc.Args, onEvent)
			case highlightToolName:
				content, isErr = executeHighlight(tc.Args, onEvent)
			case tourToolName:
				content, isErr = executeTour(tc.Args, opts.SavedTours, onEvent)
			case getPlaybookToolName:
				content, isErr = executeGetPlaybook(tc.Args, opts)
			case repliesToolName:
				content, isErr = executeSuggestReplies(tc.Args, onEvent)
			case screenshotToolName, repeatToolName:
				if len(pauses) == 0 {
					content, isErr = "this tool is not available on this page", true
					break
				}
				content, isErr = executePause(tc.Name, tc.Args, paused, onEvent)
				paused = paused || !isErr
			default:
				var confirm *ConfirmRequest
				content, isErr, confirm = executeTool(ctx, session, tc)
				if confirm != nil {
					onEvent(Event{Type: "confirm_required", Tool: tc.Name, Confirm: confirm})
				}
			}
			if isErr && isChatLayerTool(tc.Name) {
				// MCP tool failures land in the audit table; the browser-side
				// tools have no other trace. The content is the validation
				// message (anchors, field names), not user data.
				logging.Warn(ctx, nil, "chat tool call refused: "+truncate(content, 300), "chat.client_tool_refused",
					slog.String("chat.tool", tc.Name))
			}
			onEvent(Event{Type: "tool_end", Tool: tc.Name, OK: !isErr})
			res.ToolCalls = append(res.ToolCalls, ToolCallSummary{Name: tc.Name, OK: !isErr})
			results = append(results, ToolResult{ID: tc.ID, Content: content, IsError: isErr})
		}
		msgs = append(msgs, Message{Role: "user", ToolResults: results})
		// The user acts next: the browser shows the button, and the outcome
		// comes back as their next message.
		if paused {
			return res, nil
		}
	}

	// Cap tripped: return what we have with an honest marker rather than
	// erroring away a partially useful answer.
	res.Text += "\n\n_(stopped: too many tool calls for one message — ask a narrower question)_"
	return res, nil
}

func listTools(ctx context.Context, session toolSession, allowWrites bool) ([]ToolDef, error) {
	lt, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	defs := make([]ToolDef, 0, len(lt.Tools))
	for _, t := range lt.Tools {
		// Read-only mode WITHHOLDS write tools rather than letting the model
		// call them into a closed gate. Detection is the protocol's
		// readOnlyHint annotation, plus the few writes marked as allowed in
		// read-only mode (reporting an issue); an unannotated tool is
		// conservatively treated as a write, so a future tool that forgets its
		// annotation fails safe (invisible in read-only) instead of failing open.
		if !allowWrites && !mcp.AllowedInReadOnly(t) {
			continue
		}
		raw, err := json.Marshal(t.InputSchema)
		if err != nil {
			// Pinned impossible by TestInProcessToolSchemasMarshal; skip
			// defensively rather than failing the whole conversation.
			continue
		}
		defs = append(defs, ToolDef{Name: t.Name, Description: t.Description, InputSchema: raw})
	}
	return defs, nil
}

// executeTool runs one call and flattens the MCP result into text for the
// model. Tool-level failures (IsError results) are model-visible content, not
// Go errors — the model is supposed to read them and adjust.
//
// A guarded tool's preview comes back as a ConfirmRequest for the browser; the
// model gets the preview without its token.
func executeTool(ctx context.Context, session toolSession, tc ToolCall) (content string, isError bool, confirm *ConfirmRequest) {
	if carriesConfirmToken(tc.Args) {
		return "confirm_token is only accepted from the user's confirmation card. " +
			"Call the tool without it to show the user a preview.", true, nil
	}
	var args map[string]any
	if len(tc.Args) > 0 {
		if err := json.Unmarshal(tc.Args, &args); err != nil {
			return fmt.Sprintf("invalid tool arguments: %v", err), true, nil
		}
	}
	res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: tc.Name, Arguments: args})
	if err != nil {
		// Protocol-level failure (session died, ctx canceled) — still fed to
		// the model as an error result so one bad call doesn't kill the turn.
		return fmt.Sprintf("tool call failed: %v", err), true, nil
	}
	if cr, forModel, ok := asConfirmRequest(tc, res); ok {
		return forModel, false, cr
	}
	text := resultText(res)
	if len(text) > maxToolResultChars {
		text = text[:maxToolResultChars] + "\n…(truncated — narrow the query for the rest)"
	}
	if text == "" {
		text = "(empty result)"
	}
	return text, res.IsError, nil
}

// isChatLayerTool reports the tools the loop runs itself (browser actions,
// playbooks) rather than forwarding to MCP.
func isChatLayerTool(name string) bool {
	switch name {
	case navigateToolName, chartToolName, highlightToolName, tourToolName, getPlaybookToolName, repliesToolName:
		return true
	}
	return isPauseTool(name)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
