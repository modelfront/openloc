package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/httputil"
	"openloc/internal/ptr"
	"openloc/internal/sliceutil"
	"openloc/internal/terminology"
	"openloc/internal/tmprofile"
	"openloc/internal/translationmemory"
)

// Translation memory tools. Every method resolves the TM with the
// tenant-scoped GetTMCollection before any follow-up query on its id, and
// calls the same translationmemory.Service as the REST handlers.

func (b *directBackend) tmService() *translationmemory.Service {
	return translationmemory.NewService(b.q, b.pool, b.pool)
}

// resourceErr maps the domain packages' errors onto the MCP error kinds.
func resourceErr(err error, what string) error {
	if err == nil {
		return nil
	}
	var (
		tmInvalid      translationmemory.ValidationError
		tmInUse        translationmemory.InUseError
		profileInUse   tmprofile.InUseError
		termImportFail terminology.ImportError
	)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return errNotFound
	case errors.As(err, &tmInvalid):
		return invalidf("%s", tmInvalid.Msg)
	case errors.As(err, &tmInUse):
		return invalidf("%s. Remove it from those settings first.", tmInUse.Error())
	case errors.As(err, &profileInUse):
		return invalidf("%s", profileInUse.Error())
	case errors.As(err, &termImportFail):
		return invalidf("%s", termImportFail.Msg)
	case errors.Is(err, translationmemory.ErrConflict):
		return invalidf("someone changed this entry since you read it — re-read it with " +
			"openloc_list_tm_entries and decide again before overwriting their work")
	case httputil.IsUniqueViolation(err):
		return invalidf("that name is already taken in this organization; choose another")
	}
	if msg, ok := httputil.AsClientError(err); ok {
		return invalidf("%s", msg)
	}
	return fmt.Errorf("%s: %w", what, err)
}

func (b *directBackend) tmCollection(ctx context.Context, tmID int64) (db.TmCollection, error) {
	tenantID, _ := scope(ctx)
	tm, err := b.q.GetTMCollection(ctx, db.GetTMCollectionParams{ID: tmID, TenantID: tenantID})
	if err != nil {
		return db.TmCollection{}, resourceErr(err, "get TM")
	}
	return tm, nil
}

// needPool refuses, in words the model can relay, an operation this backend
// cannot run without a pool: what names it ("creating a workflow").
func (b *directBackend) needPool(what string) error {
	if b.pool == nil {
		return invalidf("%s is unavailable on this server", what)
	}
	return nil
}

// inResourceTx runs fn in a transaction; commit=false always rolls back,
// which previews run to validate exactly like the real change. what names the
// operation for needPool.
func (b *directBackend) inResourceTx(ctx context.Context, what string, commit bool, fn func(q db.Querier) error) error {
	if err := b.needPool(what); err != nil {
		return err
	}
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := fn(db.New(tx)); err != nil {
		return err
	}
	if !commit {
		return nil
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func tmSummary(tm db.TmCollection, entries int64) TMSummary {
	return TMSummary{
		TMID: tm.ID, Name: tm.Name, SourceLanguage: ptr.Deref(tm.SourceLanguage),
		TargetLanguages: sliceutil.NonNil(tm.TargetLanguages), EntryCount: entries,
		CustomerID: ptr.Deref(tm.CustomerID), CreatedAt: tsString(tm.CreatedAt),
	}
}

func (b *directBackend) ListTMs(ctx context.Context, _ ListTMsIn) (TMList, error) {
	tenantID, _ := scope(ctx)
	rows, err := b.q.ListTMCollections(ctx, tenantID)
	if err != nil {
		return TMList{}, fmt.Errorf("list TMs: %w", err)
	}
	out := make([]TMSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, TMSummary{
			TMID: r.ID, Name: r.Name, SourceLanguage: ptr.Deref(r.SourceLanguage),
			TargetLanguages: sliceutil.NonNil(r.TargetLanguages), EntryCount: r.EntryCount,
			CustomerID: ptr.Deref(r.CustomerID), CustomerName: ptr.Deref(r.CustomerName),
			CreatedBy: r.CreatedByName, CreatedAt: tsString(r.CreatedAt),
		})
	}
	return TMList{TMs: out, Count: len(out)}, nil
}

func (b *directBackend) GetTM(ctx context.Context, in GetTMIn) (TMDetail, error) {
	tm, err := b.tmCollection(ctx, in.TMID)
	if err != nil {
		return TMDetail{}, err
	}
	entries, err := b.q.CountTMEntries(ctx, db.CountTMEntriesParams{TmID: tm.ID})
	if err != nil {
		return TMDetail{}, fmt.Errorf("count TM entries: %w", err)
	}
	pairs, err := b.q.ListTMLanguagePairs(ctx, tm.ID)
	if err != nil {
		return TMDetail{}, fmt.Errorf("list TM language pairs: %w", err)
	}
	refs, err := b.q.CountTMReferences(ctx, tm.ID)
	if err != nil {
		return TMDetail{}, fmt.Errorf("count TM references: %w", err)
	}
	out := TMDetail{TMSummary: tmSummary(tm, entries), LanguagePairs: []TMLanguagePair{}, Assignments: refs}
	for _, p := range pairs {
		out.LanguagePairs = append(out.LanguagePairs, TMLanguagePair{SourceLanguage: p.SourceLanguage, TargetLanguage: p.TargetLanguage})
	}
	return out, nil
}

func (b *directBackend) ListTMEntries(ctx context.Context, in ListTMEntriesIn) (TMEntryPage, error) {
	tm, err := b.tmCollection(ctx, in.TMID)
	if err != nil {
		return TMEntryPage{}, err
	}
	page, err := b.tmService().ListEntries(ctx, tm.ID, translationmemory.EntryFilter{
		SourceLanguage: in.SourceLanguage, TargetLanguage: in.TargetLanguage,
		Query: in.Query, Cursor: in.Cursor, Limit: int32(clampLimit(in.Limit)),
	})
	if err != nil {
		return TMEntryPage{}, resourceErr(err, "list TM entries")
	}
	out := TMEntryPage{Entries: []TMEntry{}, NextCursor: page.NextCursor, HasMore: page.HasMore}
	if page.Total != translationmemory.UnknownTotal {
		out.Total = &page.Total
	}
	for _, e := range page.Entries {
		out.Entries = append(out.Entries, tmEntry(e))
	}
	for _, m := range page.Matches {
		out.Entries = append(out.Entries, TMEntry{
			EntryID: m.ID, SourceLanguage: m.SourceLanguage, TargetLanguage: m.TargetLanguage,
			SourceText: m.SourceText, TargetText: m.TargetText,
			HasTags:   m.SourceRichText != nil || m.TargetRichText != nil,
			UpdatedBy: m.UpdatedBy, Version: tsVersion(m.UpdatedAt),
		})
	}
	return out, nil
}

func tmEntry(e db.TranslationMemory) TMEntry {
	return TMEntry{
		EntryID: e.ID, SourceLanguage: e.SourceLanguage, TargetLanguage: e.TargetLanguage,
		SourceText: e.SourceText, TargetText: e.TargetText,
		HasTags:   e.SourceRichText != nil || e.TargetRichText != nil,
		UpdatedBy: e.UpdatedBy, Version: tsVersion(e.UpdatedAt),
	}
}

func tmImport(i db.TmImport) TMImport {
	return TMImport{
		ImportID: i.ID, TMID: i.TmID, FileName: i.FileName, Status: i.Status,
		TotalUnits: i.TotalUnits, Imported: i.Imported, Skipped: i.Skipped,
		ErrorMessage: ptr.Deref(i.ErrorMessage),
		CreatedAt:    tsString(i.CreatedAt), UpdatedAt: tsString(i.UpdatedAt),
	}
}

func (b *directBackend) ListTMImports(ctx context.Context, in GetTMIn) (TMImportList, error) {
	tm, err := b.tmCollection(ctx, in.TMID)
	if err != nil {
		return TMImportList{}, err
	}
	rows, err := b.q.ListTMImports(ctx, tm.ID)
	if err != nil {
		return TMImportList{}, fmt.Errorf("list TM imports: %w", err)
	}
	out := make([]TMImport, 0, len(rows))
	for _, r := range rows {
		out = append(out, tmImport(r))
	}
	return TMImportList{Imports: out, Count: len(out)}, nil
}

func (b *directBackend) CreateTM(ctx context.Context, in CreateTMIn) (TMSummary, error) {
	tenantID, userID := scope(ctx)
	var customerID *int64
	if in.CustomerID != 0 {
		customerID = &in.CustomerID
	}
	tm, err := b.tmService().CreateCollection(ctx, tenantID, userID, translationmemory.CreateCollectionInput{
		Name: strings.TrimSpace(in.Name), SourceLanguage: in.SourceLanguage,
		TargetLanguages: in.TargetLanguages, CustomerID: customerID,
	})
	if err != nil {
		return TMSummary{}, resourceErr(err, "create TM")
	}
	return tmSummary(tm, 0), nil
}

func (b *directBackend) RenameTM(ctx context.Context, in RenameTMIn) (TMSummary, error) {
	tenantID, _ := scope(ctx)
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return TMSummary{}, invalidf("name is required")
	}
	tm, err := b.q.RenameTMCollection(ctx, db.RenameTMCollectionParams{ID: in.TMID, Name: name, TenantID: tenantID})
	if err != nil {
		return TMSummary{}, resourceErr(err, "rename TM")
	}
	entries, err := b.q.CountTMEntries(ctx, db.CountTMEntriesParams{TmID: tm.ID})
	if err != nil {
		return TMSummary{}, fmt.Errorf("count TM entries: %w", err)
	}
	return tmSummary(tm, entries), nil
}

// UpdateTMEntry edits plain text. A side that carries inline tags keeps its
// tagged form when its text is unchanged and refuses a text change, since a
// plain-text edit would drop the tags.
func (b *directBackend) UpdateTMEntry(ctx context.Context, in UpdateTMEntryIn) (TMEntry, error) {
	if err := b.needPool("editing a TM entry"); err != nil {
		return TMEntry{}, err
	}
	tenantID, userID := scope(ctx)
	tm, err := b.tmCollection(ctx, in.TMID)
	if err != nil {
		return TMEntry{}, err
	}
	before, err := b.q.GetTMEntryScoped(ctx, db.GetTMEntryScopedParams{ID: in.EntryID, TmID: tm.ID, TenantID: tenantID})
	if err != nil {
		return TMEntry{}, resourceErr(err, "get TM entry")
	}
	sourceRich, err := keepRichText("source", before.SourceText, before.SourceRichText, in.SourceText)
	if err != nil {
		return TMEntry{}, err
	}
	targetRich, err := keepRichText("target", before.TargetText, before.TargetRichText, in.TargetText)
	if err != nil {
		return TMEntry{}, err
	}
	version, err := parseExpectedUpdatedAt(in.Version)
	if err != nil {
		return TMEntry{}, invalidf("version must be the value openloc_list_tm_entries returned, got %q", in.Version)
	}
	upd := translationmemory.UpdateEntryInput{
		SourceText: in.SourceText, TargetText: in.TargetText,
		SourceRichText: sourceRich, TargetRichText: targetRich,
		ChangeReason: "assistant",
	}
	if version.Valid {
		upd.ExpectedUpdatedAt = &version.Time
	}
	updated, err := b.tmService().UpdateEntry(ctx, tenantID, userID, tm.ID, in.EntryID, upd)
	if err != nil {
		return TMEntry{}, resourceErr(err, "update TM entry")
	}
	return tmEntry(updated), nil
}

func keepRichText(side, oldText string, oldRich *string, newText string) (string, error) {
	if oldRich == nil {
		return "", nil
	}
	if newText != oldText {
		return "", invalidf("the %s text carries inline tags, which a plain-text edit would drop; "+
			"edit this entry in the TM browser instead", side)
	}
	return *oldRich, nil
}

func (b *directBackend) ImportTMX(ctx context.Context, in ImportTMXIn) (TMImport, error) {
	if err := b.needPool("importing a TMX file"); err != nil {
		return TMImport{}, err
	}
	tenantID, _ := scope(ctx)
	tm, err := b.tmCollection(ctx, in.TMID)
	if err != nil {
		return TMImport{}, err
	}
	upload, body, err := b.openUpload(ctx, in.UploadID)
	if err != nil {
		return TMImport{}, err
	}
	defer body.Close()
	imp, err := b.tmService().StartImport(ctx, tenantID, tm.ID, upload.Filename, body)
	if err != nil {
		return TMImport{}, resourceErr(err, "import TMX")
	}
	out := tmImport(imp)
	out.Hint = "The import runs in the background. Check its progress with openloc_list_tm_imports."
	return out, nil
}

func (b *directBackend) ExportTMX(ctx context.Context, in GetTMIn) (ResourceExport, error) {
	tm, err := b.tmCollection(ctx, in.TMID)
	if err != nil {
		return ResourceExport{}, err
	}
	entries, err := b.q.CountTMEntries(ctx, db.CountTMEntriesParams{TmID: tm.ID})
	if err != nil {
		return ResourceExport{}, fmt.Errorf("count TM entries: %w", err)
	}
	if entries == 0 {
		return ResourceExport{}, invalidf("this TM has no entries to export")
	}
	return ResourceExport{DownloadPath: fmt.Sprintf("/api/tms/%d/export", tm.ID), Filename: tm.Name + ".tmx"}, nil
}

func (b *directBackend) PreviewDeleteTM(ctx context.Context, in DeleteTMIn) (Preview, error) {
	tm, err := b.tmCollection(ctx, in.TMID)
	if err != nil {
		return Preview{}, err
	}
	refs, err := b.q.CountTMReferences(ctx, tm.ID)
	if err != nil {
		return Preview{}, fmt.Errorf("count TM references: %w", err)
	}
	if refs > 0 {
		return Preview{}, resourceErr(translationmemory.InUseError{Assignments: refs}, "delete TM")
	}
	entries, err := b.q.CountTMEntries(ctx, db.CountTMEntriesParams{TmID: tm.ID})
	if err != nil {
		return Preview{}, fmt.Errorf("count TM entries: %w", err)
	}
	return Preview{
		Summary: fmt.Sprintf("Delete the translation memory %q.", tm.Name),
		Effects: []string{fmt.Sprintf("Its %d entries leave the TM list and are no longer searchable.", entries)},
	}, nil
}

func (b *directBackend) DeleteTM(ctx context.Context, in DeleteTMIn) (ResourceDeleted, error) {
	tenantID, _ := scope(ctx)
	tm, err := b.tmCollection(ctx, in.TMID)
	if err != nil {
		return ResourceDeleted{}, err
	}
	if err := b.tmService().DeleteCollection(ctx, tenantID, tm.ID); err != nil {
		return ResourceDeleted{}, resourceErr(err, "delete TM")
	}
	return ResourceDeleted{Kind: "tm", ID: tm.ID, Name: tm.Name}, nil
}
