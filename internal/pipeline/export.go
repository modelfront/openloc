package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/parser"
	"openloc/internal/ptr"
)

// ExportOriginal returns the file exactly as it was uploaded — the customer's
// source document, untouched. Backs the "Source file" download option
// and is the shared original-fetch step for ExportFile.
//
// Tenant scoping: the file lookup goes through GetFileTenant, so a fileID from
// another tenant returns ErrNoRows rather than that tenant's bytes.
func (s *Service) ExportOriginal(ctx context.Context, tenantID, fileID int64) ([]byte, string, error) {
	file, err := s.queries.GetFileTenant(ctx, db.GetFileTenantParams{ID: fileID, TenantID: tenantID})
	if err != nil {
		return nil, "", fmt.Errorf("get file: %w", err)
	}
	data, err := s.downloadOriginal(ctx, file.StoragePath)
	if err != nil {
		return nil, "", err
	}
	return data, file.Name, nil
}

// downloadOriginal streams the stored upload into memory. Split out so
// ExportFile and ExportOriginal share one storage code path.
func (s *Service) downloadOriginal(ctx context.Context, storagePath string) ([]byte, error) {
	// Tests and storage-less deployments run with a nil FileStorage; uploads
	// made under one leave storage_path empty. Fail with a readable error
	// rather than a nil dereference.
	if s.storage == nil {
		return nil, fmt.Errorf("file storage is not configured")
	}
	reader, err := s.storage.Download(ctx, storagePath)
	if err != nil {
		return nil, fmt.Errorf("download original: %w", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read original file: %w", err)
	}
	return data, nil
}

// ExportFile reconstructs the translated file for one target language. A
// parser whose export depends on the job's languages (an XLSX workbook with a
// column per language, a PowerPoint deck that marks translated text with the
// target language) implements parser.LangAwareReconstructor and is told the
// project's source language and the target; every other parser uses
// Reconstruct.
//
// Tenant scoping: caller passes tenantID;
// the file lookup is tenant-scoped via GetFileTenant.
func (s *Service) ExportFile(ctx context.Context, tenantID, fileID int64, targetLang string) ([]byte, string, error) {
	file, err := s.queries.GetFileTenant(ctx, db.GetFileTenantParams{ID: fileID, TenantID: tenantID})
	if err != nil {
		return nil, "", fmt.Errorf("get file: %w", err)
	}

	original, err := s.downloadOriginal(ctx, file.StoragePath)
	if err != nil {
		return nil, "", err
	}

	rows, err := s.queries.ListApprovedForExport(ctx, db.ListApprovedForExportParams{
		FileID:         fileID,
		TargetLanguage: targetLang,
	})
	if err != nil {
		return nil, "", fmt.Errorf("list translations: %w", err)
	}

	layout, err := parser.FileXLSXLayout(file.XlsxLayout)
	if err != nil {
		return nil, "", fmt.Errorf("read stored xlsx layout: %w", err)
	}
	p, err := exportParser(string(file.Format), parser.ParseOptions{InlineHTML: file.InlineHtml, XLSXLayout: layout}, original, rows)
	if err != nil {
		return nil, "", fmt.Errorf("get parser: %w", err)
	}

	translations := exportTranslations(rows, string(file.Format), file.InlineHtml)
	var result []byte
	if lp, ok := p.(parser.LangAwareReconstructor); ok {
		var project db.Project
		project, err = s.queries.GetProject(ctx, db.GetProjectParams{ID: file.ProjectID, TenantID: tenantID})
		if err != nil {
			return nil, "", fmt.Errorf("get project: %w", err)
		}
		langs := parser.ExportLangs{Source: project.SourceLanguage, Target: targetLang}
		result, err = lp.ReconstructForLang(ctx, original, translations, langs)
	} else {
		result, err = p.Reconstruct(original, translations)
	}
	if err != nil {
		return nil, "", fmt.Errorf("reconstruct: %w", err)
	}

	return result, file.Name, nil
}

// UserExportMessage returns the message to show when an export failed for a
// reason the user can fix (see parser.ExportError). ok is false for every
// other error, which handlers log and answer with a generic 500.
func UserExportMessage(err error) (msg string, ok bool) {
	var ee *parser.ExportError
	if errors.As(err, &ee) {
		return ee.Msg, true
	}
	return "", false
}

// exportParser returns the parser that writes a file's export, reading its
// strings as HTML or plain text as they were parsed (opts.InlineHTML). An Android
// file whose stored segments hold the raw string content gets RawText, so its
// stored escapes are decoded before they are written. An HTML file gets its
// segments' stored source, which says how each stored translation
// numbers its tags.
func exportParser(format string, opts parser.ParseOptions, original []byte, rows []db.ListApprovedForExportRow) (parser.Parser, error) {
	if format == "html" {
		stored := make(map[string]parser.StoredSource, len(rows))
		for _, r := range rows {
			if r.SegmentKey != nil {
				stored[*r.SegmentKey] = parser.StoredSource{Text: r.SourceText, RichText: ptr.Deref(r.SourceRichText)}
			}
		}
		return parser.HTMLWithStoredSource(stored), nil
	}
	if format != "android_xml" {
		return parser.GetParserWith(format, opts)
	}
	stored := make(map[string]string, len(rows))
	for _, r := range rows {
		if r.SegmentKey != nil {
			stored[*r.SegmentKey] = r.SourceText
		}
	}
	raw, err := parser.AndroidStoredRaw(original, stored)
	if err != nil {
		return nil, err
	}
	return parser.AndroidXML{RawText: raw}, nil
}

// exportTranslations picks, per segment key, the text Reconstruct writes for
// a file of this format: the rich text (inline tags) for tag-aware formats,
// parser.TextFormatTranslation for JSON, PO, TXT and XLSX, else the plain
// text, with HTML formatting stripped where the format has none. inlineHTML
// is the file's "Strings contain HTML" mode.
//
// Generic XML and PowerPoint keep their chips but not editor formatting: in
// XML without a schema no element means "bold", and a PowerPoint run carries
// its formatting on its chip, so <strong>/<em> are dropped rather than written
// out as escaped characters.
func exportTranslations(rows []db.ListApprovedForExportRow, format string, inlineHTML bool) map[string]string {
	textFormat := parser.InlineHTMLFormat(format)
	preserveHTML := format == "html" || format == "android_xml"
	stripRichFormatting := format == "xml" || format == "pptx"
	isXLIFF := format == "xliff" || format == "mqxliff" || format == "sdlxliff"
	isHTML := format == "html"
	isDOCX := format == "docx"
	isPPTX := format == "pptx"
	// Generic XML and Android strings round-trip inline elements as chips;
	// without the rich text the <sup>, <b> and <meta-ref> tags are lost on export.
	isXML := format == "xml" || format == "android_xml"
	translations := make(map[string]string, len(rows))
	for _, row := range rows {
		if row.SegmentKey == nil {
			continue
		}
		if textFormat {
			if row.ActiveText != nil {
				translations[*row.SegmentKey] = parser.TextFormatTranslation(*row.ActiveText, ptr.Deref(row.ActiveRichText), inlineHTML)
			}
			continue
		}
		// For tag-aware formats: prefer rich text for round-trip tag preservation.
		// active_rich_text holds the current visible target (== the human's
		// text for confirmed rows).
		if isXLIFF || isHTML || isDOCX || isXML || isPPTX {
			if row.ActiveRichText != nil && *row.ActiveRichText != "" {
				rich := *row.ActiveRichText
				if stripRichFormatting {
					rich = parser.StripFormattingTags(rich)
				}
				translations[*row.SegmentKey] = rich
				continue
			}
		}
		if row.ActiveText != nil {
			text := *row.ActiveText
			if !preserveHTML && parser.HasHTMLFormatting(text) {
				text = parser.StripHTMLFormatting(text)
			}
			translations[*row.SegmentKey] = text
		}
	}
	return translations
}

// ExportBilingualXLIFF emits a bilingual XLIFF 1.2 document carrying every
// segment in the file (regardless of source format) with both <source>
// and <target>. Used by external translators working in their own CAT tools
// against our project's segments — the most common interop request.
//
// The round-trip contract: the trans-unit `id` attribute equals
// `segments.segment_key`, so a translator who edits this XLIFF and sends
// it back can be re-imported via POST /api/files/{fileID}/reimport-xliff. The integration test
// TestIntegration_BilingualExport_RoundTripsViaReimport pins this
// end-to-end.
//
// We deliberately use a separate query (ListAllForBilingualExport) and a
// separate code path (parser.SerializeBilingualXLIFF) instead of reusing
// the source-format-mirror export, because:
//  1. Bilingual export needs ALL segments (incl. untranslated), not just
//     approved/auto_approved.
//  2. Bilingual export needs source_rich_text, locked and the
//     pre-translation lineage (origin, TM score, matched TM unit), none of
//     which the mirror path uses.
//  3. The XLIFF state / state-qualifier mapping is bilingual-export-specific
//     (mirror exports don't emit state for non-XLIFF source formats).
//
// `sourceLang` and `targetLang` are required and must be non-empty
// language codes — the serializer rejects empty values to prevent
// malformed XLIFF.
func (s *Service) ExportBilingualXLIFF(ctx context.Context, tenantID, fileID int64, sourceLang, targetLang string) ([]byte, string, error) {
	file, err := s.queries.GetFileTenant(ctx, db.GetFileTenantParams{ID: fileID, TenantID: tenantID})
	if err != nil {
		return nil, "", fmt.Errorf("get file: %w", err)
	}

	rows, err := s.queries.ListAllForBilingualExport(ctx, db.ListAllForBilingualExportParams{
		FileID:         fileID,
		TargetLanguage: targetLang,
		TenantID:       tenantID,
	})
	if err != nil {
		return nil, "", fmt.Errorf("list segments for bilingual export: %w", err)
	}

	segments := make([]parser.BilingualSegment, 0, len(rows))
	for _, row := range rows {
		// Every segment carries a key (set by the parser at upload time).
		// Fail loudly on a missing one: a silently dropped row could not be
		// matched back on re-import, so the translator's edits would be lost.
		if row.SegmentKey == nil || *row.SegmentKey == "" {
			return nil, "", fmt.Errorf("segment %d has nil/empty segment_key — bilingual export aborted to prevent silent round-trip loss", row.SegmentID)
		}
		seg := parser.BilingualSegment{
			Key:        *row.SegmentKey,
			SourceText: row.SourceText,
		}
		if row.SourceRichText != nil {
			seg.SourceRichText = *row.SourceRichText
		}

		// active_* holds the current visible target (== human's text for
		// confirmed rows). Empty target stays empty — the serializer emits
		// `<target state="needs-translation"></target>` so the
		// recipient tool surfaces the segment for translation.
		if row.ActiveRichText != nil && *row.ActiveRichText != "" {
			seg.TargetRichText = *row.ActiveRichText
		}
		if row.ActiveText != nil && *row.ActiveText != "" {
			seg.TargetText = *row.ActiveText
		}

		// status comes from translations.status (NULL when no row
		// exists for this language → empty string → mapped to
		// needs-translation by the serializer). Same nil-check for
		// locked, which is also nullable from the LEFT JOIN.
		//
		// Untranslated rows carry status='needs_review' with active_text NULL
		// (untranslated is derived from empty text). For XLIFF export
		// an empty target MUST still map to
		// needs-translation, so only propagate the status when there's actual
		// text — otherwise leave it empty so the serializer emits
		// needs-translation.
		if row.Status.Valid && (seg.TargetText != "" || seg.TargetRichText != "") {
			seg.Status = string(row.Status.TranslationStatus)
		}
		if row.Locked != nil {
			seg.Locked = *row.Locked
		}

		// Pre-translation lineage: becomes state-qualifier, the TM score
		// and the matched TM unit (<alt-trans>).
		if row.Origin.Valid {
			seg.Origin = string(row.Origin.TranslationOrigin)
		}
		if row.TmMatchPercent != nil {
			p := int(*row.TmMatchPercent)
			seg.MatchPercent = &p
		}
		if row.MatchSourceText != nil && row.MatchTargetText != nil {
			seg.Match = &parser.TMMatch{SourceText: *row.MatchSourceText, TargetText: *row.MatchTargetText}
		}

		segments = append(segments, seg)
	}

	// `original` (required XLIFF 1.2 <file> attribute) carries the source
	// document's name so the recipient tool — memoQ rejects files lacking it —
	// can identify the file in the bilingual package.
	out, err := parser.SerializeBilingualXLIFF(file.Name, sourceLang, targetLang, segments)
	if err != nil {
		return nil, "", fmt.Errorf("serialize bilingual XLIFF: %w", err)
	}

	return out, BilingualXLIFFFilename(file.Name, targetLang), nil
}

// BilingualXLIFFFilename is the download name of a file's bilingual XLIFF:
// the source extension becomes .xlf so the recipient tool routes it to its
// XLIFF importer ("report.docx" → "report_es.xlf").
func BilingualXLIFFFilename(name, targetLang string) string {
	return fmt.Sprintf("%s_%s.xlf", stripExt(name), targetLang)
}

// SourceExportFilename is the download name of a file exported in its own
// format: the target language, then the original name.
func SourceExportFilename(name, targetLang string) string {
	return targetLang + "_" + name
}

// stripExt removes the final dot-extension from a filename, returning
// the basename. For "report.docx" → "report"; for "no_extension" →
// "no_extension"; for ".bashrc" → ".bashrc" (leading dot preserved).
func stripExt(name string) string {
	if dot := strings.LastIndex(name, "."); dot > 0 {
		return name[:dot]
	}
	return name
}
