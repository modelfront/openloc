package mcp

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/llmdebug"
)

const (
	defaultLLMCallLimit = 10
	// maxLLMCallLimit is low because one call carries up to 2 × 32 KiB of text.
	maxLLMCallLimit = 20
	maxLLMGroups    = 200
)

// WireLLMLog hands the LLM call log to a backend built by
// NewDirectBackendWithPool. Without it the log tools answer that there is no
// log; the server lists them only when Deps.LLMLog is set.
func WireLLMLog(b Backend, src llmdebug.Snapshotter) {
	if d, ok := b.(*directBackend); ok {
		d.llmLog = src
	}
}

// ListLLMCalls is the caller's organization's calls. The log has no tenant, so
// a call belongs to the org that owns its file; calls without a file are
// never shown. An id from another org is the same not-found as a missing one.
func (b *directBackend) ListLLMCalls(ctx context.Context, in ListLLMCallsIn) (LLMCallList, error) {
	tenantID, _ := scope(ctx)
	if err := b.checkLLMLogIDs(ctx, tenantID, in); err != nil {
		return LLMCallList{}, err
	}
	return b.listLLMCalls(ctx, in, func(entries []llmdebug.Entry) (map[int64]bool, error) {
		ids, err := b.q.ListTenantFileIDsIn(ctx, db.ListTenantFileIDsInParams{Ids: llmdebug.FileIDs(entries), TenantID: tenantID})
		if err != nil {
			return nil, fmt.Errorf("own llm log files: %w", err)
		}
		own := make(map[int64]bool, len(ids))
		for _, id := range ids {
			own[id] = true
		}
		return own, nil
	})
}

// SupportListLLMCalls is every organization's calls, for the support team
// (requireOperator).
func (b *directBackend) SupportListLLMCalls(ctx context.Context, in ListLLMCallsIn) (LLMCallList, error) {
	return b.listLLMCalls(ctx, in, nil)
}

// checkLLMLogIDs refuses a job, file or project of another org.
func (b *directBackend) checkLLMLogIDs(ctx context.Context, tenantID int64, in ListLLMCallsIn) error {
	var err error
	if in.JobID != 0 {
		_, err = b.q.GetJobTenant(ctx, db.GetJobTenantParams{ID: in.JobID, TenantID: tenantID})
	}
	if err == nil && in.FileID != 0 {
		_, err = b.q.GetFileTenant(ctx, db.GetFileTenantParams{ID: in.FileID, TenantID: tenantID})
	}
	if err == nil && in.ProjectID != 0 {
		_, err = b.q.GetProject(ctx, db.GetProjectParams{ID: in.ProjectID, TenantID: tenantID})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return fmt.Errorf("check llm log ids: %w", err)
	}
	return nil
}

// listLLMCalls filters a snapshot of the log. own, when set, returns the file
// ids of the snapshot the caller may read.
func (b *directBackend) listLLMCalls(ctx context.Context, in ListLLMCallsIn, own func([]llmdebug.Entry) (map[int64]bool, error)) (LLMCallList, error) {
	if b.llmLog == nil {
		return LLMCallList{}, invalidf("this server keeps no LLM call log")
	}
	f, err := llmLogFilter(in)
	if err != nil {
		return LLMCallList{}, err
	}
	entries, complete := b.llmLog.Snapshot(ctx)
	out := LLMCallList{Calls: []LLMCall{}, Groups: []llmdebug.Group{}, Partial: !complete}
	if n := len(entries); n > 0 {
		out.Oldest = entries[n-1].Time.UTC().Format(time.RFC3339)
	}

	if own != nil {
		if f.FileIDs, err = own(entries); err != nil {
			return LLMCallList{}, err
		}
	}
	if in.ProjectID != 0 {
		ids, err := b.q.ListProjectFileIDs(ctx, in.ProjectID)
		if err != nil {
			return LLMCallList{}, fmt.Errorf("project llm log files: %w", err)
		}
		f.FileIDs = intersect(f.FileIDs, ids)
	}

	if in.GroupBy != "" {
		limit := in.Limit
		if limit <= 0 || limit > maxLLMGroups {
			limit = maxLLMGroups
		}
		out.Groups, out.TotalGroups = llmdebug.Groups(entries, f, in.GroupBy, limit)
		if out.Groups == nil {
			out.Groups = []llmdebug.Group{}
		}
		out.Count = len(out.Groups)
		return out, nil
	}

	limit := in.Limit
	if limit <= 0 {
		limit = defaultLLMCallLimit
	}
	limit = min(limit, maxLLMCallLimit)
	page, next := llmdebug.Page(entries, f, limit)
	for _, e := range page {
		out.Calls = append(out.Calls, llmCall(e))
	}
	out.Count = len(out.Calls)
	if next != 0 {
		out.NextCursor, out.HasMore = strconv.FormatUint(next, 10), true
	}
	return out, nil
}

// llmLogFilter turns the arguments into a filter, minus the file sets.
func llmLogFilter(in ListLLMCallsIn) (llmdebug.Filter, error) {
	f := llmdebug.Filter{JobID: in.JobID, FileID: in.FileID}
	switch in.Kind {
	case "", llmdebug.KindMT, llmdebug.KindQE, llmdebug.KindAPE, llmdebug.KindExtension:
		f.Kind = in.Kind
	default:
		return f, invalidf("kind must be mt, qe, ape or extension")
	}
	switch in.GroupBy {
	case "", llmdebug.GroupByJob, llmdebug.GroupByFile:
	default:
		return f, invalidf("group_by must be job or file")
	}
	for _, t := range []struct {
		raw  string
		name string
		dst  *time.Time
	}{{in.From, "from", &f.From}, {in.To, "to", &f.To}} {
		if t.raw == "" {
			continue
		}
		ts, err := time.Parse(time.RFC3339, t.raw)
		if err != nil {
			return f, invalidf("%s must be an RFC 3339 time, like 2026-10-30T09:00:00Z", t.name)
		}
		*t.dst = ts
	}
	if in.Cursor != "" {
		c, err := strconv.ParseUint(in.Cursor, 10, 64)
		if err != nil || c == 0 {
			return f, invalidf("cursor must be the next_cursor of a previous response")
		}
		f.Before = c
	}
	return f, nil
}

// intersect narrows set (nil = everything) to ids.
func intersect(set map[int64]bool, ids []int64) map[int64]bool {
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if set == nil || set[id] {
			out[id] = true
		}
	}
	return out
}

func llmCall(e llmdebug.Entry) LLMCall {
	c := LLMCall{
		Time: e.Time.UTC().Format(time.RFC3339Nano), Kind: e.Kind, Provider: e.Provider, Model: e.Model,
		Engine: e.Engine, Step: e.Step, JobID: e.JobID, FileID: e.FileID, Project: e.Project,
		Status: e.Status, DurationMs: e.DurationMs, Error: e.Error, Segments: e.Segments, Retry: e.Retry,
		System: e.System, User: e.User, Response: e.ResponseBody,
	}
	if e.User == "" {
		c.RequestBody = e.RequestBody
	}
	return c
}
