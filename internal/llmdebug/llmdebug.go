// Package llmdebug is the capture layer for outbound AI provider calls
// (OpenAI/Claude MT + QE+APE, extension HTTP calls). The transport
// clients record every request/response pair into a Recorder: the in-memory
// Ring in dev (bounded, lost on restart, DEV_AUTO_LOGIN only) and the Store in
// production (NDJSON in object storage). GET /api/debug/llm/calls streams the
// feed to the /debug/llm page via cursor polling — from the Ring in dev, and
// from Reader behind RequirePlatformAdmin in production. A nil recorder means
// capture is off.
//
// Security invariant: API keys never enter the buffer. The LLM capture sites
// pass bodies but never headers (where OpenAI/Anthropic keys live); the
// extension host function redacts the injected key before recording.
package llmdebug

import (
	"context"
	"time"
)

// Kind values for Entry.Kind — set statically at each capture site.
const (
	KindMT        = "mt"
	KindQE        = "qe"
	KindAPE       = "ape"
	KindExtension = "extension"
)

// maxBodyCapture caps each captured body (request and response separately).
// 32 KiB × 2 bodies × ringCapacity 200 ≈ 12.8 MiB worst case — bounded dev
// memory even when every entry is a maxed-out prompt.
const maxBodyCapture = 32 << 10

// Entry is one captured provider call. The `json` tags are the wire format of
// GET /api/debug/llm/calls; tygo exports this struct to
// web/src/types/llmdebug.ts (keep tygo.yaml's include_types in sync).
type Entry struct {
	Seq        uint64    `json:"seq"`
	Time       time.Time `json:"time"`
	Kind       string    `json:"kind"`
	Provider   string    `json:"provider"`        // openai | claude | ext:<extension id>
	Model      string    `json:"model,omitempty"` // effective model; empty for extensions
	URL        string    `json:"url"`             // token already redacted by the capture site
	Status     int       `json:"status"`          // HTTP status; 0 = transport failure
	DurationMs int64     `json:"duration_ms"`
	Error      string    `json:"error,omitempty"` // redacted by the capture site

	RequestBody  string `json:"request_body"`  // literal outgoing body (truncated)
	ResponseBody string `json:"response_body"` // literal raw response (truncated)

	// LLM convenience fields (empty for extensions): the assembled prompts,
	// pre-marshaling — what the /debug/llm page renders as readable text.
	System string `json:"system,omitempty"`
	User   string `json:"user,omitempty"`

	// Host is the pod that WROTE this entry (persisted feed only; empty for the
	// dev in-memory ring). With replicas=2 an upload's MT task and AI-step task
	// can be handled by different pods, so when one half of a job is missing the
	// first question is "which pod persisted the half that survived" — and
	// without this the feed cannot answer it.
	Host string `json:"host,omitempty"`

	// How many segments this one request carried, and whether it is a retry of
	// segments an earlier batch's reply left out (the split-and-retry sends one
	// segment per request). Without these a feed of 10s and 1s is unreadable:
	// the 1s are a mix of file tails, over-budget segments travelling alone and
	// retries, and only the last of those means something went wrong.
	Segments int  `json:"segments,omitempty"`
	Retry    bool `json:"retry,omitempty"`

	// Pipeline context, copied from ctx (WithMeta) at record time.
	JobID   int64  `json:"job_id,omitempty"`
	FileID  int64  `json:"file_id,omitempty"`
	Project string `json:"project,omitempty"`
	Step    string `json:"step,omitempty"`
	Engine  string `json:"engine,omitempty"`
}

// normalize fills an Entry's pipeline context from ctx and truncates both
// bodies. Shared by Ring (the dev viewer's buffer) and Store (the prod NDJSON
// writer) so "what a recorded entry looks like" is defined once.
//
// It deliberately stamps NEITHER Seq NOR Time. Both are ordering keys, and the
// viewer sorts by one while displaying the other, so they must be assigned
// together, under the recorder's own ordering (see Ring.Record). normalize runs
// outside the Ring's mutex, so stamping Time here lets concurrent callers order
// Time and Seq differently.
func normalize(ctx context.Context, e Entry) Entry {
	m := MetaFromContext(ctx)
	if e.JobID == 0 {
		e.JobID = m.JobID
	}
	if e.FileID == 0 {
		e.FileID = m.FileID
	}
	if e.Project == "" {
		e.Project = m.Project
	}
	if e.Step == "" {
		e.Step = m.Step
	}
	if e.Engine == "" {
		e.Engine = m.Engine
	}
	e.RequestBody = truncateBody(e.RequestBody)
	e.ResponseBody = truncateBody(e.ResponseBody)
	return e
}

// truncateBody clips s to maxBodyCapture, marking the cut so neither the viewer
// nor a persisted object renders a silently incomplete payload as complete.
func truncateBody(s string) string {
	if len(s) <= maxBodyCapture {
		return s
	}
	return s[:maxBodyCapture] + "…[truncated]"
}
