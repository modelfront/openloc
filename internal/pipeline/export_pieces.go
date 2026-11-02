package pipeline

import (
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/parser"
	"openloc/internal/ptr"
)

// joinSplitPieces folds the rows a split or join made for this language back
// into one row per original key, so Reconstruct sees the keys a fresh parse of
// the file produces. The pieces of one key are joined in document order with
// the source's gaps, their chip ids shifted back; a piece with no
// translation contributes its source, and a key none of whose pieces is
// translated keeps no translation, as an untranslated segment does. The
// folded row's source is its pieces' sources joined, which is the original
// segment's. Every other row passes through, and order is kept.
func joinSplitPieces(rows []db.ListApprovedForExportRow) []db.ListApprovedForExportRow {
	groups := map[string][]db.ListApprovedForExportRow{}
	for _, r := range rows {
		if r.ExportKey != nil {
			groups[*r.ExportKey] = append(groups[*r.ExportKey], r)
		}
	}
	if len(groups) == 0 {
		return rows
	}
	out := make([]db.ListApprovedForExportRow, 0, len(rows))
	for _, r := range rows {
		if r.ExportKey == nil {
			out = append(out, r)
			continue
		}
		pieces, ok := groups[*r.ExportKey]
		if !ok {
			continue // already folded at its first piece
		}
		delete(groups, *r.ExportKey)
		out = append(out, foldPieces(*r.ExportKey, pieces))
	}
	return out
}

func foldPieces(key string, rows []db.ListApprovedForExportRow) db.ListApprovedForExportRow {
	source := make([]parser.Piece, len(rows))
	target := make([]parser.Piece, len(rows))
	translated := false
	for i, r := range rows {
		gap := ptr.Deref(r.GapBefore)
		base := int(r.ChipBase)
		source[i] = parser.Piece{Text: r.SourceText, Rich: ptr.Deref(r.SourceRichText), ChipBase: base, Gap: gap}
		target[i] = source[i]
		if r.ActiveText != nil && *r.ActiveText != "" {
			translated = true
			target[i] = parser.Piece{Text: *r.ActiveText, Rich: ptr.Deref(r.ActiveRichText), ChipBase: base, Gap: gap}
		}
	}
	folded := rows[0]
	folded.SegmentKey = &key
	folded.ExportKey = nil
	folded.GapBefore = nil
	folded.ChipBase = 0
	text, rich := parser.JoinPieces(source)
	folded.SourceText, folded.SourceRichText = text, ptr.NilIfZero(rich)
	folded.ActiveText, folded.ActiveRichText = nil, nil
	if translated {
		text, rich = parser.JoinPieces(target)
		folded.ActiveText, folded.ActiveRichText = &text, ptr.NilIfZero(rich)
	}
	return folded
}
