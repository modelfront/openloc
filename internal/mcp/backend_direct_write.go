package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"openloc/internal/auth"
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/taskedit"
	"openloc/internal/translation"
)

// Write tools, plus the ListFiles and ListMembers reads.
//
// The SECURITY rules in backend_direct.go's header apply here with more force,
// because a scoping mistake in a write corrupts data rather than leaking it.
// Every method below starts from a tenant-scoped lookup and uses only ids
// returned by it.

// maxCommentLength bounds a single comment. It is stricter than the REST path
// on purpose: an agent can generate unbounded prose into a comment field.
const maxCommentLength = 5000

// --- Reads ---------------------------------------------------------------------

func (b *directBackend) ListFiles(ctx context.Context, in ListFilesIn) (FileList, error) {
	// Tenant-scoped project fetch first: the file queries take a bare
	// projectID and do NOT scope by tenant.
	if _, err := b.project(ctx, in.ProjectID); err != nil {
		return FileList{}, err
	}
	limit := clampLimit(in.Limit)
	files, err := b.q.ListMCPFilesPage(ctx, db.ListMCPFilesPageParams{
		ProjectID: in.ProjectID, Cursor: in.Cursor, Lim: int32(limit + 1),
	})
	if err != nil {
		return FileList{}, fmt.Errorf("list files: %w", err)
	}
	out := FileList{Files: make([]FileSummary, 0, min(len(files), limit))}
	if len(files) > limit {
		files = files[:limit]
		out.HasMore = true
		out.NextCursor = files[len(files)-1].ID
	}

	// job_count is the whole reason this tool exists (a file with zero jobs is
	// invisible via list_jobs), so it is counted for the page in one query.
	ids := make([]int64, len(files))
	for i, f := range files {
		ids[i] = f.ID
	}
	counts, err := b.q.CountJobsByFiles(ctx, ids)
	if err != nil {
		return FileList{}, fmt.Errorf("count jobs for files: %w", err)
	}
	jobsPerFile := make(map[int64]int, len(counts))
	for _, c := range counts {
		jobsPerFile[c.FileID] = int(c.JobCount)
	}

	for _, f := range files {
		out.Files = append(out.Files, FileSummary{
			ID:           f.ID,
			Name:         f.Name,
			Format:       string(f.Format),
			SegmentCount: f.SegmentCount,
			CreatedAt:    tsString(f.CreatedAt),
			JobCount:     jobsPerFile[f.ID],
		})
	}
	out.Count = len(out.Files)
	return out, nil
}

func (b *directBackend) ListMembers(ctx context.Context, in ListMembersIn) (MemberList, error) {
	tenantID, _ := scope(ctx)
	// Purpose-built query (active users only — deactivated users can't be
	// assigned work): carries language pairs + live assignment counts so
	// vendor questions are one call. No email column
	// exists on it at all — the address-book exclusion holds structurally.
	limit := clampLimit(in.Limit)
	after, err := parseMembersCursor(in.Cursor)
	if err != nil {
		return MemberList{}, err
	}
	rows, err := b.q.ListMCPMembers(ctx, db.ListMCPMembersParams{
		TenantID: tenantID, HasCursor: in.Cursor != "", AfterName: after.name, AfterID: after.id,
		Lim: int32(limit + 1),
	})
	if err != nil {
		return MemberList{}, fmt.Errorf("list members: %w", err)
	}
	out := MemberList{Members: make([]Member, 0, min(len(rows), limit))}
	if len(rows) > limit {
		rows = rows[:limit]
		out.HasMore = true
		last := rows[len(rows)-1]
		out.NextCursor = membersCursor{name: last.Name, id: last.ID}.String()
	}
	for _, m := range rows {
		pairs := m.LanguagePairs
		if pairs == nil {
			pairs = []string{}
		}
		out.Members = append(out.Members, Member{
			UserID:        m.ID,
			Name:          m.Name,
			Role:          m.Role,
			LanguagePairs: pairs,
			ActiveJobs:    m.ActiveJobs,
		})
	}
	out.Count = len(out.Members)
	return out, nil
}

// --- Writes -------------------------------------------------------------------

// translation is the tenant-scoped translation lookup every segment-level write
// funnels through. Centralised so no write can skip the tenant check or the
// linguist assignment check, mirroring the `project` and `job` helpers.
func (b *directBackend) translation(ctx context.Context, translationID int64) (db.Translation, error) {
	tenantID, _ := scope(ctx)
	t, err := b.q.GetTranslationTenant(ctx, db.GetTranslationTenantParams{
		ID:       translationID,
		TenantID: tenantID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Translation{}, errNotFound
		}
		return db.Translation{}, fmt.Errorf("get translation: %w", err)
	}
	ok, err := auth.LinguistMayAccessTranslation(ctx, b.q, t.ID)
	if err != nil {
		return db.Translation{}, fmt.Errorf("check job assignment: %w", err)
	}
	if !ok {
		return db.Translation{}, errNotFound
	}
	return t, nil
}

func (b *directBackend) AddComment(ctx context.Context, in AddCommentIn) (CommentResult, error) {
	content := strings.TrimSpace(in.Content)
	if content == "" {
		return CommentResult{}, invalidf("content is required")
	}
	if len(content) > maxCommentLength {
		return CommentResult{}, invalidf(
			"comment is too long (%d characters, max %d)", len(content), maxCommentLength)
	}
	if _, err := b.translation(ctx, in.TranslationID); err != nil {
		return CommentResult{}, err
	}
	_, userID := scope(ctx)
	c, err := b.q.CreateComment(ctx, db.CreateCommentParams{
		TranslationID: in.TranslationID,
		UserID:        &userID,
		Content:       content,
	})
	if err != nil {
		return CommentResult{}, fmt.Errorf("create comment: %w", err)
	}
	return CommentResult{
		CommentID:     c.ID,
		TranslationID: in.TranslationID,
		CreatedAt:     tsString(c.CreatedAt),
	}, nil
}

func (b *directBackend) LockSegment(ctx context.Context, in LockSegmentIn) (LockResult, error) {
	t, err := b.translation(ctx, in.TranslationID)
	if err != nil {
		return LockResult{}, err
	}

	if in.Unlock {
		// Anyone who may edit the segment may unlock it, like the unlock routes.
		if err := b.q.UnlockTranslation(ctx, t.ID); err != nil {
			return LockResult{}, fmt.Errorf("unlock translation: %w", err)
		}
		return LockResult{TranslationID: t.ID, Locked: false}, nil
	}

	// Locking approves the segment: managers only, like the lock routes.
	// Enforced here rather than by tool tier because lock and unlock are one
	// tool. The approval is the assistant's, so it stays out of TM.
	if !auth.IsManagerRole(auth.RoleFromContext(ctx)) {
		return LockResult{}, invalidf(
			"locking a segment requires a project manager, admin or owner")
	}
	s, err := b.jobServices()
	if err != nil {
		return LockResult{}, err
	}
	fileID, err := b.q.GetTranslationFileID(ctx, t.ID)
	if err != nil {
		return LockResult{}, fmt.Errorf("get translation file: %w", err)
	}
	tenantID, userID := scope(ctx)
	switch err := s.Translations.LockOne(ctx, tenantID, userID, t, fileID, translation.ConfirmedViaAssistant); {
	case errors.Is(err, translation.ErrLockEmpty):
		return LockResult{}, invalidf("%s", translation.ErrLockEmpty.Error())
	case errors.Is(err, translation.ErrConflict):
		return LockResult{}, invalidf("the segment changed; read it again and retry")
	case err != nil:
		return LockResult{}, serviceErr(err, "lock segment")
	}
	return LockResult{TranslationID: t.ID, Locked: true}, nil
}

// SaveDraft writes a proposed translation WITHOUT approving it.
//
// This is deliberately the only content-writing tool in this slice. A draft
// leaves the segment in needs-review and — unlike a confirm — writes nothing to
// translation memory, so an agent's proposal cannot silently become leverage for
// every future job. A human still confirms in the editor.
//
// Mirrors translation.HandleSaveDraft: the locked check, the task edit guard
// (without its reopen), the optimistic-locking argument, and the
// ErrNoRows→held-vs-conflict-vs-not-found disambiguation.
func (b *directBackend) SaveDraft(ctx context.Context, in SaveDraftIn) (SegmentWriteResult, error) {
	text := in.Text
	if strings.TrimSpace(text) == "" {
		return SegmentWriteResult{}, invalidf("text is required")
	}

	existing, err := b.translation(ctx, in.TranslationID)
	if err != nil {
		return SegmentWriteResult{}, err
	}
	if existing.Locked {
		return SegmentWriteResult{}, invalidf(
			"segment is locked — it must be unlocked before it can be edited")
	}
	// The same task rule as the editor, minus the reopen: a draft from a tool
	// never puts a completed workflow task back in progress.
	tenantID, _ := scope(ctx)
	if _, err := taskedit.New(b.q, nil).Check(ctx, tenantID, taskedit.Ref{TranslationID: in.TranslationID},
		taskedit.Opts{NoReopen: true}); err != nil {
		return SegmentWriteResult{}, serviceErr(err, "check task")
	}

	expected, err := parseExpectedUpdatedAt(in.ExpectedUpdatedAt)
	if err != nil {
		return SegmentWriteResult{}, err
	}

	_, userID := scope(ctx)
	t, err := b.q.SaveTranslationDraft(ctx, db.SaveTranslationDraftParams{
		ID:                in.TranslationID,
		HumanText:         &text,
		EditedBy:          &userID,
		ExpectedUpdatedAt: expected,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// A row someone else holds, or one that moved since the caller read it.
		// Surfacing a conflict rather than a generic failure is what lets a
		// model re-read and reconsider instead of retrying blindly into a lost
		// update.
		err = translation.ExplainNoRows(ctx, b.q, tenantID, in.TranslationID, userID, expected.Valid)
	}
	if err != nil {
		return SegmentWriteResult{}, serviceErr(err, "save draft")
	}

	return SegmentWriteResult{
		TranslationID: t.ID,
		Status:        string(t.Status),
		UpdatedAt:     tsVersion(t.UpdatedAt),
		// Always zero: drafts are never written to TM. Stated explicitly so the
		// difference from confirm is visible in the result, not just the docs.
		TMEntriesWritten: 0,
	}, nil
}

// parseExpectedUpdatedAt turns the optional RFC3339 string into the nullable
// timestamp the query expects. An unparseable value is a caller error, not a
// silent "no version check" — otherwise a typo would quietly disable optimistic
// locking and reintroduce lost updates.
func parseExpectedUpdatedAt(raw string) (pgtype.Timestamptz, error) {
	if strings.TrimSpace(raw) == "" {
		return pgtype.Timestamptz{}, nil
	}
	// RFC3339Nano parses both the truncated and the full-precision forms, so a
	// caller echoing back exactly what a read tool returned always works.
	ts, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return pgtype.Timestamptz{}, invalidf(
			"expected_updated_at must be an RFC3339 timestamp like 2026-08-04T12:00:00Z, got %q", raw)
	}
	return pgtype.Timestamptz{Time: ts, Valid: true}, nil
}
