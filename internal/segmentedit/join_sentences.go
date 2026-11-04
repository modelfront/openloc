package segmentedit

import (
	"context"
	"errors"
	"fmt"
	"io"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/parser"
)

// planSentenceJoin joins sentences of one block, or runs of them a join made
// before, into one row over the whole run. Its source and chips are the run as
// the file's parser numbers it, so export writes its translation over those
// sentences and the text between them. A sentence split in this job joins
// only with all of its pieces.
func (s *Service) planSentenceJoin(ctx context.Context, job db.Job, tenantID int64, rows []row, between []row) (joinPlan, error) {
	blocks, err := s.sentenceBlocks(ctx, job, tenantID)
	if err != nil {
		return joinPlan{}, err
	}
	block := -1
	runs := make(map[int64][2]int, len(rows))
	var order [][2]int
	for _, r := range rows {
		b, i, j, ok := locate(blocks, exportKey(r))
		if !ok || (block >= 0 && b != block) {
			return joinPlan{}, ErrOtherUnit
		}
		block = b
		run := [2]int{i, j}
		runs[r.TranslationID] = run
		if len(order) == 0 || order[len(order)-1] != run {
			if len(order) > 0 && run[0] != order[len(order)-1][1]+1 {
				return joinPlan{}, ErrNotAdjacent
			}
			order = append(order, run)
		}
	}
	if len(order) < 2 {
		return joinPlan{}, ErrOtherUnit
	}
	picked := make(map[int64]bool, len(rows))
	touched := map[string]bool{}
	for _, r := range rows {
		picked[r.TranslationID] = true
		touched[exportKey(r)] = true
	}
	for _, b := range between {
		if touched[exportKey(b)] && !picked[b.TranslationID] {
			return joinPlan{}, ErrJoinPartial
		}
	}

	first, last := order[0][0], order[len(order)-1][1]
	rng, err := blocks[block].Range(first, last)
	if errors.Is(err, parser.ErrSentenceRange) {
		return joinPlan{}, ErrJoinUnsafe
	}
	if err != nil {
		return joinPlan{}, err
	}
	// A row's chips are numbered within its own run; the joined run numbers
	// them from its first sentence's offset.
	shift := make(map[[2]int]int, len(order))
	for _, run := range order {
		off := rng.Offsets[run[0]-first]
		if run[0] != run[1] {
			old, err := blocks[block].Range(run[0], run[1])
			if err != nil {
				return joinPlan{}, err
			}
			off -= old.Offsets[0]
		}
		shift[run] = off
	}
	plan := joinPlan{segment: languageSegment(job, rows[0], rng.Key, parser.Piece{Text: rng.Text, Rich: rng.Rich})}
	plan.targetText, plan.targetRich = joinTargets(rows, func(r row) int {
		return int(r.ChipBase) + shift[runs[r.TranslationID]]
	})
	return plan, nil
}

// sentenceBlocks reads the job's file as export does, and lists the blocks
// that split into sentences. A format with one segment per unit (PO, Excel,
// XLIFF without <mrk> sentences) has none a join could put together.
func (s *Service) sentenceBlocks(ctx context.Context, job db.Job, tenantID int64) ([]parser.SentenceBlock, error) {
	f, err := s.q.GetFileTenant(ctx, db.GetFileTenantParams{ID: job.FileID, TenantID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("get file: %w", err)
	}
	opts, err := parser.FileOptions(f.InlineHtml, f.XlsxLayout, f.Segmentation, f.FilterProfile)
	if err != nil {
		return nil, err
	}
	p, err := parser.GetParserWith(string(f.Format), opts)
	if err != nil {
		return nil, err
	}
	joiner, ok := p.(parser.SentenceJoiner)
	if !ok {
		return nil, ErrOtherUnit
	}
	if s.storage == nil {
		return nil, fmt.Errorf("file storage is not configured")
	}
	body, err := s.storage.Download(ctx, f.StoragePath)
	if err != nil {
		return nil, fmt.Errorf("download original: %w", err)
	}
	defer body.Close()
	original, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read original: %w", err)
	}
	return joiner.SentenceBlocks(original)
}

// locate finds the block and the sentences a sentence or range key names.
func locate(blocks []parser.SentenceBlock, key string) (block, i, j int, ok bool) {
	for b, blk := range blocks {
		if i, j, ok := blk.Locate(key); ok {
			return b, i, j, true
		}
	}
	return 0, 0, 0, false
}
