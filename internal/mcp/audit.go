package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/errtrack"
	"openloc/internal/logging"
)

// Audit trail for MCP tool calls.
//
// It answers two questions. Support: "the assistant said it couldn't find my
// project" is otherwise undebuggable — you cannot tell whether the model called
// the wrong tool, passed a bad id, or hit a role gate. And accountability:
// "which agent changed this segment".

// Outcomes recorded for every invocation.
const (
	outcomeOK     = "ok"
	outcomeError  = "error"
	outcomeDenied = "denied"
)

// auditWriter is the slice of db.Querier the audit path needs.
type auditWriter interface {
	InsertMCPToolCall(ctx context.Context, arg db.InsertMCPToolCallParams) error
}

// argsDigest returns a SHA-256 of the tool arguments.
//
// Deliberately a digest and not the arguments: tool args carry source text,
// search queries, and translated content. Storing them would turn the audit
// table into an uncontrolled second copy of customer data with a different
// retention story than `segments`. The digest still supports the questions an
// audit log is for — "was this the same call repeated 400 times?" — without
// holding the content.
func argsDigest(args any) string {
	if args == nil {
		return ""
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// recordDurable is record's synchronous sibling for WRITE tools:
// a mutation's audit row must not be lost to a database
// blip, so the insert happens in-line and a failure is returned to the caller.
// The write itself has already happened by then — the tool reports it as
// executed-but-unaudited rather than pretending either way.
func recordDurable(ctx context.Context, w auditWriter, tool string, args any, outcome string, errCode string, started time.Time) error {
	if w == nil {
		return nil
	}
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return fmt.Errorf("no claims in context")
	}
	// token_id references api_tokens, so it is set ONLY for a PAT. An OAuth
	// grant has no api_tokens row — leaving this nil
	// inserts NULL instead of a 0 that would violate the FK and drop the row.
	var tokenID *int64
	if info := auth.PATInfoFromContext(ctx); info != nil && info.Kind == auth.CredentialPAT && info.TokenID != 0 {
		id := info.TokenID
		tokenID = &id
	}
	var codePtr *string
	if errCode != "" {
		codePtr = &errCode
	}
	userID := claims.UserID
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return w.InsertMCPToolCall(writeCtx, db.InsertMCPToolCallParams{
		TenantID:   claims.TenantID,
		UserID:     &userID,
		TokenID:    tokenID,
		Tool:       tool,
		ArgsDigest: argsDigest(args),
		Outcome:    outcome,
		ErrorCode:  codePtr,
		DurationMs: int32(time.Since(started).Milliseconds()),
	})
}

// record writes one audit row for a read or for a call that changed nothing
// (denied, invalid, failed); a successful write uses recordDurable.
//
// Fire-and-forget with a detached context, matching the notification-send
// pattern in api.go: the calling request's context is cancelled the moment the
// tool response is written, so a synchronous write would race the response, and
// a blocking write would put a second round trip on every tool call. When
// nothing changed, losing an audit row to a database blip is an acceptable
// trade.
//
// context.WithoutCancel preserves the auth claims (unused here, but keeps the
// detached context honest) while stripping the cancel signal.
func record(ctx context.Context, w auditWriter, tool string, args any, outcome string, errCode string, started time.Time) {
	if w == nil {
		return
	}
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		// No claims means the request never passed auth.Middleware, which should
		// be impossible for a mounted tool. Log loudly rather than silently
		// dropping — an audit gap is a security-relevant event.
		logging.Error(ctx, nil, "MCP tool call has no claims; audit row skipped", "mcp-audit-no-claims", logging.Label("tool", tool))
		return
	}

	// token_id references api_tokens, so it is set ONLY for a PAT. An OAuth
	// grant has no api_tokens row — leaving this nil
	// inserts NULL instead of a 0 that would violate the FK and drop the row.
	var tokenID *int64
	if info := auth.PATInfoFromContext(ctx); info != nil && info.Kind == auth.CredentialPAT && info.TokenID != 0 {
		id := info.TokenID
		tokenID = &id
	}
	var codePtr *string
	if errCode != "" {
		codePtr = &errCode
	}
	userID := claims.UserID
	durationMs := time.Since(started).Milliseconds()

	params := db.InsertMCPToolCallParams{
		TenantID:   claims.TenantID,
		UserID:     &userID,
		TokenID:    tokenID,
		Tool:       tool,
		ArgsDigest: argsDigest(args),
		Outcome:    outcome,
		ErrorCode:  codePtr,
		DurationMs: int32(durationMs),
	}

	bgCtx := context.WithoutCancel(ctx)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				logging.Error(bgCtx, fmt.Errorf("%v", rec), "MCP audit write panicked", "mcp-audit-panic", logging.Label("tool", tool))
				errtrack.CapturePanic(bgCtx, rec, "mcp", "audit-write")
			}
		}()
		writeCtx, cancel := context.WithTimeout(bgCtx, 5*time.Second)
		defer cancel()
		if err := w.InsertMCPToolCall(writeCtx, params); err != nil {
			logging.Error(writeCtx, err, "MCP tool call not recorded", "mcp-audit-record-failed", logging.Label("tool", tool))
		}
	}()
}
