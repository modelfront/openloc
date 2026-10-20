package pipeline

import "context"

// context.go — context-key helpers for cross-cutting attribution.
//
// `triggered_by` is the user who initiated a pipeline run (uploader on
// upload-auto, the clicker on manual Pre-translate). It needs to reach
// emission sites (applyTMFill / applyFuzzyMatch / saveMTChunk /
// RunAIStep's APE branch) without threading a parameter through 7+ function
// signatures, so we plumb it via context.
//
// The async worker unwraps `PipelinePayload.TriggeredBy` and wraps the
// pipeline's working ctx via WithTriggeredBy before invoking
// TranslateFile / RunAIStep. Emission helpers read it via
// TriggeredByFromContext and use it as the history row's `created_by`.
// Synchronous HTTP-handler entry points (used when no enqueuer is
// configured) wrap the request ctx the same way.

type triggeredByKey struct{}

// WithTriggeredBy returns a new context that carries the given user ID
// as the `triggered_by` attribution for any emissions that fire under it.
// Pass nil to explicitly clear (rarely needed — absent value is treated
// as nil).
func WithTriggeredBy(ctx context.Context, userID *int64) context.Context {
	return context.WithValue(ctx, triggeredByKey{}, userID)
}

// TriggeredByFromContext returns the user ID attached to ctx, or nil if
// none. Safe to call on any context.
func TriggeredByFromContext(ctx context.Context) *int64 {
	v, ok := ctx.Value(triggeredByKey{}).(*int64)
	if !ok {
		return nil
	}
	return v
}
