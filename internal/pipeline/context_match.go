package pipeline

import (
	"context"

	"openloc/internal/logging"
	"openloc/internal/timing"
	"openloc/internal/translationmemory"
)

// 101% (in-context exact) matching. A TM entry records the md5 of
// its document neighbors' source text at write-back time; during pretranslation
// a byte-exact source hit whose stored neighbor hashes equal the current
// segment's neighbor hashes is promoted from 100% to 101%.

// buildNeighborHashes computes per-segment neighbor-context hashes for every
// segment in a file. Neighbors are taken by array-adjacency over `position`
// (so gaps in position don't matter) — the same notion the write side uses.
//
// Best-effort: an error returns nil and the TM phase degrades to plain 100%
// (no 101% promotion) rather than failing.
func (s *Service) buildNeighborHashes(ctx context.Context, fileID int64) map[int64]translationmemory.NeighborContext {
	// Whole-file operation, so n is 1 per TM phase — the interesting number
	// is ms on large files, where this is a full segment scan plus an md5 per
	// segment before any TM lookup happens.
	ctx, done := timing.Start(ctx, timing.StageTMContextMatch)
	defer func() { done(nil) }()

	segs, err := s.queries.ListSegmentSourcesByFile(ctx, fileID)
	if err != nil {
		logging.Warn(ctx, err, "neighbor-hash build failed; 101% matching disabled this run", "pipeline-neighbor-hash-failed", logging.FileID(fileID))
		return nil
	}
	out := make(map[int64]translationmemory.NeighborContext, len(segs))
	for i, seg := range segs {
		var prev, next string
		if i > 0 {
			prev = segs[i-1].SourceText
		}
		if i < len(segs)-1 {
			next = segs[i+1].SourceText
		}
		out[seg.ID] = translationmemory.NewNeighborContext(prev, next)
	}
	return out
}

// resolveContextHashes returns the md5 of the segment's previous/next source
// text (md5("") at a document boundary) for storage on a TM entry at
// write-back time. On lookup error it returns "","" — the caller stores NULL
// context, so the entry never scores 101% and degrades to plain 100%.
func (s *Service) resolveContextHashes(ctx context.Context, segmentID int64) (prevHash, nextHash string) {
	nb, err := s.queries.GetSegmentNeighborSources(ctx, segmentID)
	if err != nil {
		logging.Warn(ctx, err, "neighbor lookup failed; storing no context", "tm-write-neighbor-lookup-failed", logging.SegmentID(segmentID))
		return "", ""
	}
	return translationmemory.ContextHash(nb.PrevSource), translationmemory.ContextHash(nb.NextSource)
}
