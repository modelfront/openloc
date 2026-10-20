package terminology

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/errtrack"
	"openloc/internal/langcode"
	"openloc/internal/logging"
	"openloc/internal/tbx"
)

// Concepts committed per transaction on TBX and CSV import. Batching cuts the
// number of commits (and their fsyncs) ~100x vs one tx per concept, the
// difference between seconds and minutes on a 26k-concept file.
const importBatchSize = 200

// ImportError is a file the importer rejects; Msg is safe to show the user.
type ImportError struct{ Msg string }

func (e ImportError) Error() string { return e.Msg }

// ImportResult counts what an import wrote. Terms is set by TBX only.
type ImportResult struct {
	Imported int `json:"imported"`
	Terms    int `json:"terms,omitempty"`
	Skipped  int `json:"skipped"`
}

// csvColumns maps logical fields to 0-based column indices; -1 means absent.
type csvColumns struct {
	source, target, status, notes, domain int
	sourceLang, targetLang                int
}

// at returns the trimmed cell at index i, or "" when the column is absent or the
// row is too short.
func (c csvColumns) at(record []string, i int) string {
	if i < 0 || i >= len(record) {
		return ""
	}
	return record[i]
}

// firstNonEmpty returns the first non-empty argument, or "" if all are empty.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// parseCSVHeader maps a header row's names to column indices, so column order is
// not fixed. Unknown headers (e.g. target_language) are ignored.
func parseCSVHeader(header []string) csvColumns {
	cols := csvColumns{source: -1, target: -1, status: -1, notes: -1, domain: -1, sourceLang: -1, targetLang: -1}
	for i, name := range header {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "source_term", "source":
			cols.source = i
		case "target_term", "target":
			cols.target = i
		case "status":
			cols.status = i
		case "notes":
			cols.notes = i
		case "domain":
			cols.domain = i
		case "source_language":
			cols.sourceLang = i
		case "target_language":
			cols.targetLang = i
		}
	}
	return cols
}

// ImportCSV imports a bilingual CSV into base. A header row is honored
// (columns mapped by name, any order); otherwise the positional order
// source,target,status,notes,domain. Languages resolve per row: a
// source_language/target_language column wins, else defSource/defTarget, else
// the base's source and its sole target language. Rows that cannot form a
// concept are skipped, not fatal.
func (s *Service) ImportCSV(ctx context.Context, base db.TermBasis, userID int64, r io.Reader, defSource, defTarget string) (ImportResult, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1 // flexible column count
	reader.TrimLeadingSpace = true

	// defSource/defTarget set the pair for the whole file; per-row
	// source_language/target_language columns override them. Source falls back
	// to the base's source_language; target falls back to the base's sole
	// target_language when there's exactly one (else the row needs an explicit
	// target and is skipped).
	defSource = langcode.Normalize(defSource)
	defTarget = langcode.Normalize(defTarget)
	soleTarget := ""
	if len(base.TargetLanguages) == 1 {
		soleTarget = base.TargetLanguages[0]
	}

	var imported, skipped int
	// Rows are written importBatchSize concepts at a time, as they are read.
	pending := make([]ConceptInput, 0, importBatchSize)
	flush := func() {
		n, _, sk := importConceptsBatched(pending, importBatchSize,
			func(batch []ConceptInput) (int, error) { return s.bulkInsertBatch(ctx, base, batch, &userID) },
			s.createConceptAlone(ctx, base, userID, "termbase-import-csv-row-skipped"))
		imported += n
		skipped += sk
		pending = pending[:0]
	}
	// Column layout. Defaults are the legacy positional order (source, target,
	// status, notes, domain) for headerless files; a header row remaps them by
	// name so any column order — including the notes-last export format — imports
	// correctly.
	cols := csvColumns{source: 0, target: 1, status: 2, notes: 3, domain: 4, sourceLang: -1, targetLang: -1}
	lineNum := 0
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			skipped++
			continue
		}
		lineNum++

		// Header row: a first row that names both a source and a target column
		// (in any order, after a UTF-8 byte order mark) remaps the columns by
		// name and is skipped.
		if lineNum == 1 && len(record) > 0 {
			record[0] = strings.TrimPrefix(record[0], "\ufeff")
			if header := parseCSVHeader(record); header.source >= 0 && header.target >= 0 {
				cols = header
				continue
			}
		}

		sourceTerm := strings.TrimSpace(cols.at(record, cols.source))
		targetTerm := strings.TrimSpace(cols.at(record, cols.target))
		if sourceTerm == "" || targetTerm == "" {
			skipped++
			continue
		}

		status := "approved"
		if st := strings.TrimSpace(cols.at(record, cols.status)); isValidStatus(st) {
			status = st
		}

		srcLang := firstNonEmpty(langcode.Normalize(cols.at(record, cols.sourceLang)), defSource, base.SourceLanguage)
		tgtLang := firstNonEmpty(langcode.Normalize(cols.at(record, cols.targetLang)), defTarget, soleTarget)
		// Need distinct source + target to form a bilingual concept.
		if srcLang == "" || tgtLang == "" || srcLang == tgtLang {
			skipped++
			continue
		}

		// A bilingual row becomes a concept with a source-language term + a
		// target-language term (the concept-native write path).
		ci := ConceptInput{
			Domain: strings.TrimSpace(cols.at(record, cols.domain)),
			Terms: []ConceptTermInput{
				{Language: srcLang, Text: sourceTerm, Status: "approved"},
				{Language: tgtLang, Text: targetTerm, Status: status, Notes: strings.TrimSpace(cols.at(record, cols.notes))},
			},
		}
		pending = append(pending, ci)
		if len(pending) == importBatchSize {
			flush()
		}
	}
	flush()

	s.syncBaseLanguages(ctx, base.ID)
	return ImportResult{Imported: imported, Skipped: skipped}, nil
}

// ImportTBX imports a TBX file (v2 martif or v3 tbx, DCA style, any dialect
// via the mapping registry) into base as full multilingual concepts — all
// languages, synonyms and per-term status preserved. An unsupported
// or empty file is an ImportError.
func (s *Service) ImportTBX(ctx context.Context, base db.TermBasis, userID int64, r io.Reader) (ImportResult, error) {
	concepts, report, err := tbx.ParseConcepts(r)
	if err != nil {
		// A recognizable-but-unsupported file gets an actionable message and a
		// notice recording which type was tried (demand signal — the
		// type/root/style are structural, not customer data).
		var unsup *tbx.UnsupportedError
		if errors.As(err, &unsup) {
			errtrack.Capture(ctx, err, errtrack.CaptureOptions{
				Class:     errtrack.ClassUnsupportedImport,
				Message:   "unsupported TBX file rejected on import",
				Subsystem: "terminology",
				Event:     "import-tbx-unsupported",
				Extra: map[string]any{
					"tbx_root":  unsup.Root,
					"tbx_type":  unsup.Type,
					"tbx_style": unsup.Style,
				},
			})
			return ImportResult{}, ImportError{Msg: unsup.Msg}
		}
		logging.Warn(ctx, err, "TBX parse failed", "termbase-import-tbx-parse-failed", logging.TermbaseID(base.ID))
		return ImportResult{}, ImportError{Msg: "Could not parse TBX file — it may be malformed or not a valid TBX document."}
	}
	if len(concepts) == 0 {
		return ImportResult{}, ImportError{Msg: tbxEmptyImportReason(report)}
	}

	inputs := make([]ConceptInput, 0, len(concepts))
	for _, c := range concepts {
		ci := ConceptInput{Definition: c.Definition, Domain: c.Domain}
		for _, t := range c.Terms {
			ci.Terms = append(ci.Terms, ConceptTermInput{
				Language:     t.Language,
				Text:         t.Text,
				Status:       t.Status,
				PartOfSpeech: t.PartOfSpeech,
				Notes:        t.Notes,
			})
		}
		inputs = append(inputs, ci)
	}
	imported, terms, skipped := importConceptsBatched(inputs, importBatchSize,
		func(batch []ConceptInput) (int, error) { return s.bulkInsertBatch(ctx, base, batch, &userID) },
		s.createConceptAlone(ctx, base, userID, "termbase-import-tbx-concept-skipped"))

	s.syncBaseLanguages(ctx, base.ID)
	return ImportResult{Imported: imported, Terms: terms, Skipped: skipped}, nil
}

// tbxEmptyImportReason builds an actionable message for a TBX import that parsed
// but yielded no concepts.
func tbxEmptyImportReason(report tbx.Report) string {
	if report.Concepts == 0 {
		return "No terms found in the TBX file — it contains no concept entries."
	}
	// Concepts existed but none held a usable term.
	if len(report.LanguagesSeen) > 0 {
		return fmt.Sprintf("The TBX file's concepts contained no importable terms (languages seen: %s).",
			strings.Join(report.LanguagesSeen, ", "))
	}
	return "The TBX file's concepts contained no importable terms."
}

// importConceptsBatched drives the batched-import control flow: try the fast
// pipelined bulk insert for each batch of batchSize concepts; if a batch fails
// (its tx rolled back), fall back to inserting that batch's concepts one at a
// time so a single malformed concept is skipped rather than dropping its ~199
// healthy neighbors. The DB work is injected (bulk / single) so this stays pure
// and unit-testable without a database.
func importConceptsBatched(concepts []ConceptInput, batchSize int,
	bulk func(batch []ConceptInput) (terms int, err error),
	single func(ci ConceptInput) error,
) (imported, terms, skipped int) {
	for batch := range slices.Chunk(concepts, batchSize) {
		if tc, err := bulk(batch); err == nil {
			imported += len(batch)
			terms += tc
			continue
		}
		// Batch aborted — re-run one concept at a time to isolate + skip the bad one.
		for _, ci := range batch {
			if err := single(ci); err != nil {
				skipped++
				continue
			}
			imported++
			terms += len(ci.Terms)
		}
	}
	return imported, terms, skipped
}

// createConceptAlone inserts one concept in a transaction of its own: the
// fallback of importConceptsBatched for a batch that failed. A concept the
// database refuses is logged under action and skipped.
func (s *Service) createConceptAlone(ctx context.Context, base db.TermBasis, userID int64, action string) func(ConceptInput) error {
	return func(ci ConceptInput) error {
		if err := s.inTx(ctx, func(q db.Querier) error {
			_, err := CreateConcept(ctx, q, base, ci, &userID)
			return err
		}); err != nil {
			logging.Warn(ctx, err, "import skipped a concept", action, logging.TermbaseID(base.ID))
			return err
		}
		return nil
	}
}

// bulkInsertBatch inserts a batch of concepts + all their terms with two pipelined
// pgx batches inside one transaction: first the concepts (RETURNING id), then every
// term keyed to those ids. This turns ~(concepts + terms) synchronous round-trips
// into two network flushes — the large-import speedup. Returns the number of terms
// written. All-or-nothing: any error rolls the whole batch back so the caller can
// safely retry it per-concept.
func (s *Service) bulkInsertBatch(ctx context.Context, base db.TermBasis, concepts []ConceptInput, userID *int64) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	// Phase 1 — insert concepts, capturing their generated ids in order.
	cb := &pgx.Batch{}
	for _, ci := range concepts {
		cb.Queue(
			`INSERT INTO term_concept (term_base_id, definition, domain, created_by, updated_by)
			 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			base.ID, ci.Definition, ci.Domain, userID, userID)
	}
	ids := make([]int64, len(concepts))
	cbr := tx.SendBatch(ctx, cb)
	for i := range concepts {
		if err := cbr.QueryRow().Scan(&ids[i]); err != nil {
			cbr.Close()
			return 0, err
		}
	}
	if err := cbr.Close(); err != nil {
		return 0, err
	}

	// Phase 2 — insert every term, keyed to its concept's new id.
	tb := &pgx.Batch{}
	termCount := 0
	for i, ci := range concepts {
		for _, t := range DedupeConceptTerms(ci.Terms) {
			status := t.Status
			if !isValidStatus(status) {
				status = "approved"
			}
			tb.Queue(
				`INSERT INTO term (concept_id, language, text, status, part_of_speech, notes, created_by, updated_by)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				ids[i], langcode.Normalize(t.Language), t.Text, status, t.PartOfSpeech, t.Notes, userID, userID)
			termCount++
		}
	}
	if termCount > 0 {
		tbr := tx.SendBatch(ctx, tb)
		for j := 0; j < termCount; j++ {
			if _, err := tbr.Exec(); err != nil {
				tbr.Close()
				return 0, err
			}
		}
		if err := tbr.Close(); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return termCount, nil
}
