package pipeline

import (
	"context"
	"sort"
	"strings"
	"sync"

	"openloc/internal/extension"
	"openloc/pkg/extsdk"
)

// fakeExtPredictor is an extension.BatchPredictor driven by a verdict
// function. It records every batch it receives.
type fakeExtPredictor struct {
	name  string
	ape   bool
	batch int
	// verdict decides each row; nil approves everything.
	verdict func(extsdk.PredictRow) extsdk.PredictResult
	// respond replaces the whole response when set (for reordered or foreign
	// ids).
	respond func([]extsdk.PredictRow) extsdk.PredictResponse
	err     error
	mu      sync.Mutex // Predict runs concurrently
	calls   [][]extsdk.PredictRow
	langs   [][2]string
	metas   []extsdk.PredictMeta
}

func (f *fakeExtPredictor) Name() string {
	if f.name == "" {
		return "Mock QE"
	}
	return f.name
}
func (f *fakeExtPredictor) Provider() string { return extension.Provider("mockqe") }
func (f *fakeExtPredictor) APE() bool        { return f.ape }
func (f *fakeExtPredictor) BatchSize() int {
	if f.batch == 0 {
		return 30
	}
	return f.batch
}

func (f *fakeExtPredictor) Predict(_ context.Context, sourceLang, targetLang string, meta extsdk.PredictMeta, rows []extsdk.PredictRow) (extsdk.PredictResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, rows)
	f.metas = append(f.metas, meta)
	f.langs = append(f.langs, [2]string{sourceLang, targetLang})
	if f.err != nil {
		return extsdk.PredictResponse{}, f.err
	}
	if f.respond != nil {
		return f.respond(rows), nil
	}
	out := extsdk.PredictResponse{Rows: make([]extsdk.PredictResult, len(rows))}
	for i, r := range rows {
		if f.verdict == nil {
			out.Rows[i] = extsdk.PredictResult{ID: r.ID, Approved: true}
			continue
		}
		out.Rows[i] = f.verdict(r)
		out.Rows[i].ID = r.ID
	}
	return out, nil
}

// inBatchOrder returns the recorded batches and their metadata sorted by
// batch index: the step runs batches concurrently, so they arrive in any
// order.
func (f *fakeExtPredictor) inBatchOrder() ([][]extsdk.PredictRow, []extsdk.PredictMeta) {
	idx := make([]int, len(f.calls))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return f.metas[idx[a]].BatchIndex < f.metas[idx[b]].BatchIndex })
	calls := make([][]extsdk.PredictRow, len(idx))
	metas := make([]extsdk.PredictMeta, len(idx))
	for i, j := range idx {
		calls[i], metas[i] = f.calls[j], f.metas[j]
	}
	return calls, metas
}

// rowCount is the number of rows sent across every batch.
func (f *fakeExtPredictor) rowCount() int {
	n := 0
	for _, c := range f.calls {
		n += len(c)
	}
	return n
}

// fakeExtensions resolves predictors by key id, like extension.Service does
// for the tenant's saved keys. An unknown key behaves like a removed
// extension.
type fakeExtensions struct {
	byToken map[int64]extension.BatchPredictor
	// requested records every (tenant, key) the pipeline asked for.
	requested [][2]int64
	// err, when set, is returned for every key.
	err error
}

func (f *fakeExtensions) PredictorForToken(_ context.Context, tenantID, tokenID int64) (extension.BatchPredictor, error) {
	f.requested = append(f.requested, [2]int64{tenantID, tokenID})
	if f.err != nil {
		return nil, f.err
	}
	p, ok := f.byToken[tokenID]
	if !ok {
		return nil, extension.ErrNotInstalled
	}
	return p, nil
}

// extTokenID is the key addAIStepJob puts on its extension row.
func extTokenID(tenantID int64) int64 { return aiTokenID(tenantID, "ext") }

// newExtStepService builds a Service whose extension runtime resolves the
// key addAIStepJob uses (tenant 1) to pred.
func newExtStepService(mq *mockQuerier, pred extension.BatchPredictor) (*Service, *fakeExtensions) {
	exts := &fakeExtensions{byToken: map[int64]extension.BatchPredictor{extTokenID(1): pred}}
	svc := NewService(mq, &passthroughTxRunner{q: mq}, nil).SetAIProvider(nil, nil, identityDecryptor{})
	svc.SetExtensions(exts)
	return svc, exts
}

// approveUnless approves every row whose target does not contain bad.
func approveUnless(bad string) func(extsdk.PredictRow) extsdk.PredictResult {
	return func(r extsdk.PredictRow) extsdk.PredictResult {
		return extsdk.PredictResult{Approved: !strings.Contains(r.Target, bad)}
	}
}
