package worker

import (
	"testing"

	"github.com/hibiken/asynq"

	"openloc/internal/pipeline"
)

// Pipeline and QE+APE tasks go to a queue of their own, so the short tasks
// (an analysis here) never wait behind them for a worker.
func TestEnqueuer_LongTasksHaveTheirOwnQueue(t *testing.T) {
	stub := &stubAsynqClient{}
	e := &Enqueuer{client: stub, queries: &workerMockQuerier{}, queue: QueueOrDefault("deploy-a")}

	if err := e.EnqueuePipeline(1, "en", "de", 0, 3, pipeline.FullPipeline, nil); err != nil {
		t.Fatalf("EnqueuePipeline: %v", err)
	}
	if err := e.EnqueueAIStep(2, 3, nil); err != nil {
		t.Fatalf("EnqueueAIStep: %v", err)
	}
	if err := e.EnqueueAnalysis(4, 3); err != nil {
		t.Fatalf("EnqueueAnalysis: %v", err)
	}
	if err := e.EnqueueLongTask(asynq.NewTask("long:test", nil)); err != nil {
		t.Fatalf("EnqueueLongTask: %v", err)
	}
	if got := queueOf(t, stub.opts[3]); got != "deploy-a-long" {
		t.Errorf("EnqueueLongTask queue = %q, want deploy-a-long", got)
	}
	pipelineQ, aiStepQ, analysisQ := queueOf(t, stub.opts[0]), queueOf(t, stub.opts[1]), queueOf(t, stub.opts[2])
	if pipelineQ == analysisQ || aiStepQ == analysisQ {
		t.Fatalf("long tasks share the short tasks' queue: pipeline %q, QE+APE %q, analysis %q", pipelineQ, aiStepQ, analysisQ)
	}
	if pipelineQ != "deploy-a-long" || aiStepQ != "deploy-a-long" || analysisQ != "deploy-a" {
		t.Errorf("queues = pipeline %q, QE+APE %q, analysis %q; want deploy-a-long, deploy-a-long, deploy-a", pipelineQ, aiStepQ, analysisQ)
	}
}

// Each queue has its own pool: the long queue is drained only by the long
// pool, so long tasks can never hold every worker.
func TestServerSpecs_SeparatePools(t *testing.T) {
	specs := ServerSpecs("deploy-a", 3, 5)
	if len(specs) != 2 {
		t.Fatalf("want 2 pools, got %d", len(specs))
	}
	short, long := specs[0], specs[1]
	if _, ok := short.Queues[LongQueue("deploy-a")]; ok {
		t.Errorf("short pool drains the long queue: %v", short.Queues)
	}
	if short.Queues["deploy-a"] == 0 || short.Queues["default"] == 0 {
		t.Errorf("short pool queues = %v, want deploy-a and default", short.Queues)
	}
	if len(long.Queues) != 1 || long.Queues["deploy-a-long"] == 0 {
		t.Errorf("long pool queues = %v, want only deploy-a-long", long.Queues)
	}
	if short.Concurrency != 3 || long.Concurrency != 5 {
		t.Errorf("concurrency = short %d, long %d; want 3, 5", short.Concurrency, long.Concurrency)
	}
	if got := ServerSpecs("", 0, -1); got[0].Concurrency != 1 || got[1].Concurrency != 1 {
		t.Errorf("a pool must keep at least one worker, got %d and %d", got[0].Concurrency, got[1].Concurrency)
	}
	if got := ServerSpecs("", 1, 1)[0].Queues; len(got) != 1 || got["default"] == 0 {
		t.Errorf("an un-namespaced deployment's short pool = %v, want only default", got)
	}
}
