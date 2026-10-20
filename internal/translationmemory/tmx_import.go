package translationmemory

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/logging"
	"openloc/internal/tmx"
)

// tmxImportBatch is how many entries one multi-row INSERT carries:
// 500 rows * 12 columns stays well under PostgreSQL's 65535 parameters.
const tmxImportBatch = 500

// importBatchHook, when set (tests only), runs after each batch an import
// writes and records.
var importBatchHook func(importID int64)

// errFirstEntry stops the up-front check at the file's first entry.
var errFirstEntry = errors.New("tmx: first entry found")

// StartImport copies a TMX upload to a file in the spool directory, records the import and
// returns its tm_imports row at once; the entries are read from the copy and
// upserted batch by batch in the background (progress on ListTMImports). The
// upload is only checked up to its first entry: a file that is not XML or
// holds no entry is a ValidationError here, and XML that breaks later fails
// the import row, keeping the entries read before the break. The caller has
// already verified the TM belongs to the tenant.
func (s *Service) StartImport(ctx context.Context, tenantID, tmID int64, fileName string, r io.Reader) (db.TmImport, error) {
	sweepImportSpool(ctx)
	dir := importSpoolDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return db.TmImport{}, fmt.Errorf("create spool dir: %w", err)
	}
	spool, err := os.CreateTemp(dir, "upload-*.tmx")
	if err != nil {
		return db.TmImport{}, fmt.Errorf("create spool file: %w", err)
	}
	path := spool.Name()
	handedOff := false
	defer func() {
		if !handedOff {
			s.removeSpool(ctx, path)
		}
	}()
	_, copyErr := io.Copy(spool, r)
	if err := errors.Join(copyErr, spool.Close()); err != nil {
		return db.TmImport{}, fmt.Errorf("spool TMX upload: %w", err)
	}

	if err := checkTMXHead(path); err != nil {
		return db.TmImport{}, err
	}

	imp, err := s.queries.CreateTMImport(ctx, db.CreateTMImportParams{
		TmID: tmID, TenantID: tenantID, FileName: fileName,
	})
	if err != nil {
		return db.TmImport{}, fmt.Errorf("create import record: %w", err)
	}
	handedOff = true
	go s.processImport(imp.ID, tmID, path)
	return imp, nil
}

// checkTMXHead reads the spooled file up to its first entry.
func checkTMXHead(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open spool file: %w", err)
	}
	defer f.Close()
	err = tmx.StreamEntries(f, 1, func([]tmx.Entry) error { return errFirstEntry })
	switch {
	case errors.Is(err, errFirstEntry):
		return nil
	case err != nil:
		return invalid("invalid TMX file — check that the file is well-formed XML")
	default:
		return invalid("TMX file contains no translation entries")
	}
}

func (s *Service) removeSpool(ctx context.Context, path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		logging.Warn(ctx, err, "TMX spool file not removed", "tm-import-spool-remove-failed")
	}
}

// processImport counts the spooled TMX's entries (a file whose XML breaks
// fails here, before anything is written), then streams them into the TM in
// batches, and deletes the spool file when it ends. A batch the
// multi-row INSERT refuses is retried one entry at a time. Each batch touches
// the spool file, so the spool sweep leaves a running import's file alone.
func (s *Service) processImport(importID, tmID int64, path string) {
	ctx := context.Background()
	defer s.removeSpool(ctx, path)
	var total, imported, skipped int32
	startTime := time.Now()

	// Every tm-import event carries the TM and import ids; the counted ones
	// add count (the entries in the file), imported and skipped.
	importAttrs := func(extra ...slog.Attr) []slog.Attr {
		return append([]slog.Attr{logging.TMID(tmID), logging.TMImportID(importID)}, extra...)
	}
	countAttrs := func(extra ...slog.Attr) []slog.Attr {
		return importAttrs(append([]slog.Attr{
			logging.Count(int(total)),
			logging.Label("imported", strconv.Itoa(int(imported))),
			logging.Label("skipped", strconv.Itoa(int(skipped))),
		}, extra...)...)
	}
	// write records the counts. A failed per-batch write is a warning (the
	// next one repeats it); a failed first or final write leaves the row
	// wrong for good.
	write := func(status string) error {
		return s.queries.UpdateTMImportProgress(ctx, db.UpdateTMImportProgressParams{
			ID: importID, Status: status, TotalUnits: total, Imported: imported, Skipped: skipped,
		})
	}
	fail := func(cause error, msg string) {
		logging.Error(ctx, cause, "TM import failed", "tm-import-failed", countAttrs()...)
		if err := write("failed"); err != nil {
			logging.Error(ctx, err, "TM import could not be marked failed", "tm-import-mark-failed-failed", importAttrs()...)
		}
		if err := s.queries.UpdateTMImportError(ctx, db.UpdateTMImportErrorParams{ID: importID, ErrorMessage: &msg}); err != nil {
			logging.Error(ctx, err, "TM import could not be marked failed", "tm-import-mark-failed-failed", importAttrs()...)
		}
	}

	n, err := countTMXEntries(path)
	total = int32(n)
	if werr := write("processing"); werr != nil {
		logging.Error(ctx, werr, "TM import status update failed", "tm-import-status-failed", importAttrs()...)
	}
	if isTMXSyntaxError(err) {
		// Checked before any insert, so a fixed re-upload does not duplicate
		// the entries before the break.
		fail(err, fmt.Sprintf("The TMX file is not well-formed XML after entry %d. Nothing was imported; fix the file and upload it again.", n))
		return
	}
	if err != nil {
		fail(err, "The uploaded file could not be read. Upload it again.")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		fail(err, "The uploaded file could not be read. Upload it again.")
		return
	}
	defer f.Close()

	batchNum, read := 0, 0
	streamErr := tmx.StreamEntries(f, tmxImportBatch, func(batch []tmx.Entry) error {
		batchNum++
		read += len(batch)
		n, err := s.batchUpsertTM(ctx, tmID, batch)
		if err != nil {
			logging.Warn(ctx, err, "TM import batch failed; falling back to single inserts", "tm-import-batch-failed", importAttrs(logging.Label("batch", strconv.Itoa(batchNum)))...)
			n = s.insertTMEntriesOneByOne(ctx, tmID, batch, read-len(batch), &skipped, importAttrs)
		}
		imported += int32(n)
		if err := write("processing"); err != nil {
			logging.Warn(ctx, err, "TM import progress update failed", "tm-import-progress-failed", importAttrs()...)
		}
		now := time.Now()
		if err := os.Chtimes(path, now, now); err != nil {
			logging.Warn(ctx, err, "TMX spool file not touched", "tm-import-spool-touch-failed", importAttrs()...)
		}
		if importBatchHook != nil {
			importBatchHook(importID)
		}
		return nil
	})
	if streamErr != nil {
		fail(streamErr, fmt.Sprintf("The import stopped after entry %d because the file could not be read further. The %d entries before that point were imported.", read, imported))
		return
	}

	if imported+skipped != total {
		logging.Error(ctx, nil, "TM import counts do not add up; entries may be lost", "tm-import-count-mismatch", countAttrs()...)
	}
	logging.Info(ctx, "TM import completed", "tm-import-completed", countAttrs(logging.Duration(time.Since(startTime)))...)
	if err := write("completed"); err != nil {
		logging.Error(ctx, err, "TM import could not be marked completed", "tm-import-mark-completed-failed", importAttrs()...)
	}
}

// countTMXEntries counts the entries of a TMX file in one streaming pass.
// Broken XML returns the entries before the break with the error.
func countTMXEntries(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n := 0
	err = tmx.StreamEntries(f, tmxImportBatch, func(batch []tmx.Entry) error {
		n += len(batch)
		return nil
	})
	return n, err
}

// isTMXSyntaxError reports whether err is broken XML in the file, which the
// import itself reports when it reaches the break.
func isTMXSyntaxError(err error) bool {
	var syn *xml.SyntaxError
	return errors.As(err, &syn)
}

// importSpoolDir is where TMX uploads wait for their import.
func importSpoolDir() string { return filepath.Join(os.TempDir(), "openloc-tmx-import") }

// sweepImportSpool removes spool files no import has touched for
// importStaleAfter: left behind by a process that died mid-import. It sweeps
// this process's disk only; every process that takes uploads sweeps its own
// on the next upload and on the scheduled sweep.
func sweepImportSpool(ctx context.Context) {
	dir := importSpoolDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logging.Warn(ctx, err, "TMX spool directory not readable", "tm-import-spool-sweep-failed")
		}
		return
	}
	cutoff := time.Now().Add(-importStaleAfter)
	removed := 0
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			logging.Warn(ctx, err, "stale TMX spool file not removed", "tm-import-spool-sweep-failed")
			continue
		}
		removed++
	}
	if removed > 0 {
		logging.Warn(ctx, nil, "stale TMX spool files removed", "tm-import-spool-swept", logging.Count(removed))
	}
}

// insertTMEntriesOneByOne inserts a batch the multi-row INSERT refused, one
// entry at a time, skipping (and counting in skipped) each one that fails.
// firstIndex is the batch's position in the file, for the log.
// Returns the entries inserted.
func (s *Service) insertTMEntriesOneByOne(ctx context.Context, tmID int64, batch []tmx.Entry, firstIndex int, skipped *int32, importAttrs func(...slog.Attr) []slog.Attr) int {
	inserted := 0
	for i, e := range batch {
		creator := e.Creator
		if creator == "" {
			creator = e.ChangeUser
		}
		if _, err := s.queries.InsertTM(ctx, db.InsertTMParams{
			TmID:           tmID,
			SourceLanguage: e.SourceLang,
			TargetLanguage: e.TargetLang,
			SourceText:     e.SourceText,
			TargetText:     e.TargetText,
			CreatedBy:      creator,
			TuID:           e.TUId,
			SourceRichText: e.SourceRichText, // "" → NULL via NULLIF in SQL
			TargetRichText: e.TargetRichText,
		}); err != nil {
			*skipped++
			if *skipped <= 10 { // log the first 10 individual errors only
				logging.Warn(ctx, err, "TM import entry skipped", "tm-import-entry-skipped", importAttrs(logging.Label("entry_index", strconv.Itoa(firstIndex+i)))...)
			}
			continue
		}
		inserted++
	}
	return inserted
}
