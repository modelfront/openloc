package pipeline

import (
	"context"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/mt"
)

// ctxAwareQuerier mirrors real pgx behavior the in-memory mock skips: a write
// on a canceled context fails. Needed so this test can prove the save path is
// detached from the run context.
type ctxAwareQuerier struct {
	*mockQuerier
}

func (m *ctxAwareQuerier) UpdateTranslationMT(ctx context.Context, arg db.UpdateTranslationMTParams) (db.Translation, error) {
	if err := ctx.Err(); err != nil {
		return db.Translation{}, err
	}
	return m.mockQuerier.UpdateTranslationMT(ctx, arg)
}

func (m *ctxAwareQuerier) DeleteQAResultsForTranslations(ctx context.Context, translationIDs []int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.mockQuerier.DeleteQAResultsForTranslations(ctx, translationIDs)
}

// disconnectingEngine translates successfully and then cancels the run
// context — the moment a cancellation most expensively lands: after the
// engine round trip is paid for, before the results are saved.
type disconnectingEngine struct {
	cancel context.CancelFunc
}

func (e disconnectingEngine) Name() string { return "disconnecting" }
func (e disconnectingEngine) Translate(ctx context.Context, req mt.TranslateRequest) ([]mt.TranslateResult, error) {
	res, err := mt.Mock{}.Translate(ctx, req)
	e.cancel()
	return res, err
}

// TestSaveMTChunkSurvivesCancelAfterEngineReturn pins that a cancel between
// the engine round trip and the DB save keeps the chunk. The
// save must run on a detached, bounded context so paid-for engine output
// always persists.
func TestSaveMTChunkSurvivesCancelAfterEngineReturn(t *testing.T) {
	mq := newMockQuerier()
	mq.addFileProject(1, 10, nil, "en", []string{"de"})
	mq.untranslated[1] = []db.ListUntranslatedByFileRow{
		{ID: 100, SegmentID: 1, TargetLanguage: "de", SourceText: "Hello"},
		{ID: 101, SegmentID: 2, TargetLanguage: "de", SourceText: "World"},
	}
	q := &ctxAwareQuerier{mockQuerier: mq}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := disconnectingEngine{cancel: cancel}
	svc := newService(q, &passthroughTxRunner{q: q}, nil, func(string, string, string, map[string]any) (mt.Engine, error) { return engine, nil })

	// The run itself is allowed to report the cancellation (the next chunk
	// boundary sees a dead ctx); what must not happen is losing the chunk
	// that was already translated.
	_, _ = svc.TranslateFile(ctx, int64(1), 1, "en", "de", nil, FullPipeline)

	if len(mq.updateMTCalls) != 2 {
		t.Fatalf("MT chunk dropped on cancel-after-translate: %d of 2 segments saved, want 2", len(mq.updateMTCalls))
	}
	// The stale-QA drop rides in the same detached block as the save.
	// Left on the run context it would fail here and the
	// saved rows would keep findings describing their pre-MT text.
	if len(mq.droppedQAResultIDs) != 2 {
		t.Errorf("stale QA drop skipped on cancel: dropped %v, want both saved ids", mq.droppedQAResultIDs)
	}
}
