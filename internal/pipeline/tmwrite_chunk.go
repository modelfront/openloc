package pipeline

import (
	"context"
	"fmt"

	db "openloc/internal/database/sqlcgen"
)

// tmWriteChunk is how many approved segments SaveApprovedToTM writes to one
// TM per transaction.
const tmWriteChunk = 200

// tmEntryWrite is one approved segment on its way to the write TMs.
type tmEntryWrite struct {
	translationID          int64
	segmentID              int64
	source, target         string
	sourceRich, targetRich string
	prevHash, nextHash     string
}

// tmChunkFailure is one segment a TM refused.
type tmChunkFailure struct {
	translationID int64
	err           error
}

// upsertTMChunk writes a chunk of segments to one TM in one transaction of a
// few statements: the source locks for the whole chunk (in lock-key order, so
// two writers sharing sources cannot deadlock), then one upsert per round
// that also archives every target it overwrites, with the same rules as
// upsertTMWithHistory. ON CONFLICT cannot touch one row twice, so a source
// repeated in the chunk is split across rounds in document order: round k
// holds each source's k-th occurrence, and the result equals writing the
// segments one by one. When the chunk fails, its segments are written one
// transaction each, so a segment the TM refuses holds back only itself and its
// error is reported for it. Returns the segments saved.
func (s *Service) upsertTMChunk(ctx context.Context, tmID int64, sourceLang, targetLang, updatedBy string, changedBy *int64, reason string, chunk []tmEntryWrite) (int, []tmChunkFailure) {
	sources := make([]string, len(chunk))
	for i, w := range chunk {
		sources[i] = w.source
	}
	err := s.txr.InTx(ctx, func(q db.Querier) error {
		if err := q.AcquireTMSourceLocks(ctx, db.AcquireTMSourceLocksParams{
			TmID: tmID, SourceLanguage: sourceLang, TargetLanguage: targetLang, SourceTexts: sources,
		}); err != nil {
			return fmt.Errorf("acquire TM source locks: %w", err)
		}
		for _, round := range roundsBySource(chunk) {
			p := db.UpsertTMPipelineEntriesParams{
				TmID: tmID, SourceLanguage: sourceLang, TargetLanguage: targetLang,
				UpdatedBy: updatedBy, ChangedByUserID: changedBy, ChangeReason: reason,
				SourceTexts:      make([]string, len(round)),
				TargetTexts:      make([]string, len(round)),
				SourceRichTexts:  make([]string, len(round)),
				TargetRichTexts:  make([]string, len(round)),
				PrevSourceHashes: make([]string, len(round)),
				NextSourceHashes: make([]string, len(round)),
			}
			for i, w := range round {
				p.SourceTexts[i], p.TargetTexts[i] = w.source, w.target
				p.SourceRichTexts[i], p.TargetRichTexts[i] = w.sourceRich, w.targetRich
				p.PrevSourceHashes[i], p.NextSourceHashes[i] = w.prevHash, w.nextHash
			}
			if _, err := q.UpsertTMPipelineEntries(ctx, p); err != nil {
				return fmt.Errorf("upsert TM entries: %w", err)
			}
		}
		return nil
	})
	if err == nil {
		return len(chunk), nil
	}

	saved := 0
	var refused []tmChunkFailure
	for _, w := range chunk {
		if err := s.upsertTMWithHistory(ctx, tmID, sourceLang, targetLang, w.source, w.target, w.sourceRich, w.targetRich, updatedBy, changedBy, reason, w.prevHash, w.nextHash); err != nil {
			refused = append(refused, tmChunkFailure{translationID: w.translationID, err: err})
			continue
		}
		saved++
	}
	return saved, refused
}

// roundsBySource splits a chunk so no round repeats a source: round k holds
// the k-th occurrence of each source, in chunk order.
func roundsBySource(chunk []tmEntryWrite) [][]tmEntryWrite {
	seen := make(map[string]int, len(chunk))
	var rounds [][]tmEntryWrite
	for _, w := range chunk {
		k := seen[w.source]
		seen[w.source] = k + 1
		if k == len(rounds) {
			rounds = append(rounds, nil)
		}
		rounds[k] = append(rounds[k], w)
	}
	return rounds
}
