package worker

// Pipeline and QE+APE tasks run for minutes to hours. They go to their own
// queue, drained by their own pool of workers, so a large batch of them never
// takes the workers that run the short tasks (analyses, notifications,
// sweeps, exports).

// longQueueSuffix names a deployment's long-task queue: "<queue>-long".
const longQueueSuffix = "-long"

// LongQueue is the queue for a deployment's long tasks.
func LongQueue(queue string) string {
	return QueueOrDefault(queue) + longQueueSuffix
}

// ServerSpec is one asynq server of the worker process: the queues it drains
// (name → priority weight) and how many tasks it runs at once.
type ServerSpec struct {
	Name        string
	Queues      map[string]int
	Concurrency int
}

// ServerSpecs is the worker process's two pools. The short pool drains the
// deployment's queue and, at low priority, asynq's "default" queue, so tasks
// from producers that predate the namespacing still run. Every pool registers
// every handler, so a long task already waiting on the short queue still runs.
func ServerSpecs(queue string, concurrency, longConcurrency int) []ServerSpec {
	q := QueueOrDefault(queue)
	short := map[string]int{q: 6}
	if q != "default" {
		short["default"] = 1
	}
	return []ServerSpec{
		{Name: "short", Queues: short, Concurrency: max(concurrency, 1)},
		{Name: "long", Queues: map[string]int{LongQueue(q): 1}, Concurrency: max(longConcurrency, 1)},
	}
}
