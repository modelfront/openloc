package pipeline

import (
	db "openloc/internal/database/sqlcgen"
	"openloc/internal/inlinetag"
	"openloc/internal/ptr"
)

// Inline tags on the QE+APE path. Both backends — the LLM runner
// (aistep_llm.go) and extensions (aistep_ext.go) — send the reviewer the same chip
// form the CAT editor shows, so it can judge and fix tag placement instead of
// reviewing text the tags were stripped from.
//
// The MT path does this inside mt.LLMEngine.TranslateTagged, where the engine
// interface gives it a seam. QE+APE has no such seam, so it happens here.

// encodedBatch is one batch's text with inline tags rendered as chip labels.
// Index-aligned with the batch; Tables[i] is nil for a segment with no tags.
type encodedBatch struct {
	Sources []string
	Targets []string
	Tables  []*inlinetag.Table
	// Tagged reports whether any row carried tags, i.e. whether the request
	// needs the tag contract in its prompt.
	Tagged bool
}

// encodeBatchTags chip-encodes each row's source and target.
//
// A row's source and target share ONE table. Encoding them separately would
// give the same tag a different label on each side, leaving the model nothing
// to match on — which is the whole reason the source is shown at all.
func encodeBatchTags(batch []db.ListPreTranslatedByFileRow) encodedBatch {
	out := encodedBatch{
		Sources: make([]string, len(batch)),
		Targets: make([]string, len(batch)),
		Tables:  make([]*inlinetag.Table, len(batch)),
	}
	for i, r := range batch {
		out.Sources[i], out.Targets[i], out.Tables[i] = encodeTagPair(
			r.SourceText, ptr.Deref(r.SourceRichText),
			ptr.Deref(r.ActiveText), ptr.Deref(r.ActiveRichText))
		if out.Tables[i] != nil {
			out.Tagged = true
		}
	}
	return out
}

// encodeTagPair chip-encodes one segment's source and target against a shared
// table, returning a nil table when the segment has no inline tags.
//
// The source side decides: a target with no rich text yet still gets chips from
// the source's table once the model places them.
func encodeTagPair(plainSource, richSource, plainTarget, richTarget string) (source, target string, tbl *inlinetag.Table) {
	if richSource == "" {
		return plainSource, plainTarget, nil
	}
	tbl = inlinetag.NewTable()
	source = inlinetag.EncodeWith(tbl, richSource)
	target = plainTarget
	if richTarget != "" {
		target = inlinetag.EncodeWith(tbl, richTarget)
	}
	return source, target, tbl
}

// decodePostedit turns a chip-encoded postedit back into markup, returning the
// rich form for active_rich_text, its tag-stripped form for active_text, and
// any chip label that could not be resolved.
//
// An untagged segment passes straight through: rich and plain are the same
// string, and the caller must not write it to the rich column.
func decodePostedit(postedit string, tbl *inlinetag.Table) (plain, rich string, unknown []string) {
	rich, unknown = inlinetag.Decode(postedit, tbl)
	if tbl.Empty() {
		return rich, rich, unknown
	}
	return stripInlineTags(rich), rich, unknown
}
