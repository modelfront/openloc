package timing_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"openloc/internal/timing"
)

// sumSelf is the only legal way to total a stage map. If this ever exceeds the
// root span's wall clock, some millisecond is being counted twice.
func sumSelf(stages map[string]timing.Stage) int64 {
	var total int64
	for _, s := range stages {
		total += s.SelfMS
	}
	return total
}

// TestSelfTime_NestedSpansDoNotDoubleCount is THE invariant test for
// self-time: nested spans must partition time, never
// duplicate it.
//
// Shape mirrors production exactly — a task span wrapping an MT save span
// wrapping a tag-transfer span, which is the real chain
// task.pipeline → mt.db_save → mt.tag_transfer.
//
// Assertions are exact identities, not bounds on the sleeps: absolute
// wall-clock windows in this file flake under a loaded `go test ./...` because
// scheduler delay lands on whichever span is open.
//
// Anti-tautology: delete the `selfMS := totalMS - sp.children()` subtraction in
// recorder.go (make self equal total) and the partition assertion below fails
// with roughly triple the real elapsed time.
func TestSelfTime_NestedSpansDoNotDoubleCount(t *testing.T) {
	ctx, rec := timing.WithRecorder(context.Background())

	rootCtx, doneRoot := timing.Start(ctx, timing.StageTaskPipeline)
	time.Sleep(10 * time.Millisecond) // overhead the task owns itself

	saveCtx, doneSave := timing.Start(rootCtx, timing.StageMTDBSave)
	time.Sleep(10 * time.Millisecond) // the save's own work

	_, doneTags := timing.Start(saveCtx, timing.StageMTTagTransfer)
	time.Sleep(20 * time.Millisecond)
	doneTags(nil)

	doneSave(nil)
	doneRoot(nil)

	stages := rec.Stages()
	root := stages[timing.StageTaskPipeline]
	save := stages[timing.StageMTDBSave]
	tags := stages[timing.StageMTTagTransfer]

	// Total is inclusive, so the outer spans must be at least as large as what
	// they contain — that's the relationship that makes double-counting
	// possible in the first place.
	if root.TotalMS < save.TotalMS || save.TotalMS < tags.TotalMS {
		t.Fatalf("totals are not nested: root=%d save=%d tags=%d",
			root.TotalMS, save.TotalMS, tags.TotalMS)
	}

	// THE invariant, as an exact identity rather than a window around the
	// sleeps. `selfMS = totalMS - children()` is integer arithmetic in the
	// recorder and these spans are strictly nested, so the self times telescope
	// to precisely the root's total no matter how long the sleeps really ran.
	// How long they ran is the one thing a loaded `go test ./...` controls.
	if partition := sumSelf(stages); partition != root.TotalMS {
		t.Errorf("self times sum to %dms, want exactly the root's %dms — nested time is counted twice or lost",
			partition, root.TotalMS)
	}

	// The same identity per span: a leaf keeps everything, a parent keeps what
	// its child did not.
	if tags.SelfMS != tags.TotalMS {
		t.Errorf("tag transfer self = %dms but total = %dms — a leaf keeps all its time",
			tags.SelfMS, tags.TotalMS)
	}
	if want := save.TotalMS - tags.TotalMS; save.SelfMS != want {
		t.Errorf("mt.db_save self = %dms, want %dms (%dms total minus the tag transfer's %dms) — the nested span must be subtracted",
			save.SelfMS, want, save.TotalMS, tags.TotalMS)
	}
	if want := root.TotalMS - save.TotalMS; root.SelfMS != want {
		t.Errorf("task.pipeline self = %dms, want %dms (%dms total minus the save's %dms) — everything nested must be subtracted",
			root.SelfMS, want, root.TotalMS, save.TotalMS)
	}
}

// TestSelfTime_UnthreadedContextIsASibling documents the one way a caller can
// still get this wrong, so the failure mode is known rather than surprising.
//
// Passing the ORIGINAL ctx to nested work (instead of the ctx Start returned)
// makes the inner span a sibling of the outer one. Both then keep their full
// time and the pair over-reports. This is why every call site threads the
// returned context, and why the package doc says to do it even for spans that
// have no children today.
func TestSelfTime_UnthreadedContextIsASibling(t *testing.T) {
	ctx, rec := timing.WithRecorder(context.Background())

	// Correct: inner span started from the outer span's context.
	outerCtx, doneOuter := timing.Start(ctx, timing.StageMTDBSave)
	_, doneInner := timing.Start(outerCtx, timing.StageMTTagTransfer)
	time.Sleep(15 * time.Millisecond)
	doneInner(nil)
	doneOuter(nil)

	// Assert the STRUCTURAL identity (self = total − children), not a
	// wall-clock threshold. It's exact integer arithmetic in the recorder, so
	// it holds under any amount of scheduler noise — an absolute millisecond
	// bound does not, because noise lands on the parent, not the child.
	threaded := rec.Stages()
	outer, inner := threaded[timing.StageMTDBSave], threaded[timing.StageMTTagTransfer]
	// Guard first: at inner.TotalMS == 0 the identity below degenerates to
	// self == total, which is the very hazard the unthreaded half asserts, and
	// the check would pass vacuously.
	if inner.TotalMS <= 0 {
		t.Fatalf("threaded: inner span recorded %dms, so subtraction proves nothing", inner.TotalMS)
	}
	if got, want := outer.SelfMS, outer.TotalMS-inner.TotalMS; got != want {
		t.Errorf("threaded: db_save self = %dms, want %dms (total %dms − child %dms) — the child owns its time",
			got, want, outer.TotalMS, inner.TotalMS)
	}
	// The partition property: with the child nested, the self times sum to the
	// outer span's wall clock. Nothing is counted twice.
	if got := sumSelf(threaded); got != outer.TotalMS {
		t.Errorf("threaded: self times sum to %dms, want the outer total %dms", got, outer.TotalMS)
	}

	// Wrong: inner span started from the ORIGINAL ctx, so it never becomes a
	// child and the outer span can't subtract it.
	ctx2, rec2 := timing.WithRecorder(context.Background())
	_, doneOuter2 := timing.Start(ctx2, timing.StageMTDBSave)
	_, doneInner2 := timing.Start(ctx2, timing.StageMTTagTransfer)
	time.Sleep(15 * time.Millisecond)
	doneInner2(nil)
	doneOuter2(nil)

	unthreaded := rec2.Stages()
	outer2, inner2 := unthreaded[timing.StageMTDBSave], unthreaded[timing.StageMTTagTransfer]
	// The hazard: with no parent to subtract from, the outer span keeps every
	// millisecond it spent — self == total instead of self == total − child.
	if outer2.SelfMS != outer2.TotalMS {
		t.Fatalf("unthreaded: db_save self = %dms, want its full total %dms (the documented hazard)",
			outer2.SelfMS, outer2.TotalMS)
	}
	// Which is exactly why threading matters: the shared sleep lands twice, so
	// the self times now sum past the outer span's wall clock by the inner
	// span's whole duration.
	if got, want := sumSelf(unthreaded), outer2.TotalMS+inner2.TotalMS; got != want {
		t.Errorf("unthreaded: self times sum to %dms, want %dms (both spans counted in full)", got, want)
	}
	if inner2.TotalMS <= 0 {
		t.Errorf("unthreaded: inner span recorded %dms, so the double-count is invisible", inner2.TotalMS)
	}
}

// TestSelfTime_ConcurrentChildrenClampAtZero covers the case where children run
// in parallel and their totals sum past the parent's wall clock. Self would go
// negative and corrupt the partition, so it clamps.
func TestSelfTime_ConcurrentChildrenClampAtZero(t *testing.T) {
	ctx, rec := timing.WithRecorder(context.Background())

	parentCtx, doneParent := timing.Start(ctx, timing.StageQASpellcheck)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, done := timing.Start(parentCtx, timing.StageAIPredictLLM)
			time.Sleep(20 * time.Millisecond)
			done(nil)
		}()
	}
	wg.Wait()
	doneParent(nil)

	stages := rec.Stages()
	if got := stages[timing.StageQASpellcheck].SelfMS; got < 0 {
		t.Errorf("parent self = %dms, must never go negative", got)
	}
	if n := stages[timing.StageAIPredictLLM].N; n != 4 {
		t.Errorf("recorded %d concurrent children, want 4", n)
	}
}

// TestSelfTime_CloserIsIdempotent — a span closed twice (a defer plus an
// explicit call on an error path, say) must not report its time to the parent
// twice, which would push the parent's self below its real value.
func TestSelfTime_CloserIsIdempotent(t *testing.T) {
	ctx, rec := timing.WithRecorder(context.Background())

	parentCtx, doneParent := timing.Start(ctx, timing.StageTaskTranslate)
	_, doneChild := timing.Start(parentCtx, timing.StageMTEngineCall)
	time.Sleep(10 * time.Millisecond)
	doneChild(errors.New("boom"))
	doneChild(nil) // second call — must be a no-op
	doneParent(nil)

	stages := rec.Stages()
	if n := stages[timing.StageMTEngineCall].N; n != 1 {
		t.Errorf("child recorded %d times, want 1 — the closer must be idempotent", n)
	}
	if e := stages[timing.StageMTEngineCall].Err; e != 1 {
		t.Errorf("child err = %d, want 1 (the first close carried the error)", e)
	}
}
