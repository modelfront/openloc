// Bounded-concurrency fan-out for client-side batch operations.
//
// Used by bulk job actions (Apply TM/MT/Predict, Assign, Set deadline) where
// the UI loops over N selected jobs and calls a per-job endpoint for each.
// The apply endpoints just enqueue an async pipeline task, so firing them off
// concurrently is fine — but firing *all* of them at once against a large
// project would hammer the server, so we cap the in-flight count.
//
// Never rejects: each item's outcome (ok/error) is captured so callers can
// show a partial-failure summary ("applied 8, 2 failed") instead of aborting
// the whole batch on the first error.

export interface SettledResult<T> {
  item: T
  ok: boolean
  error?: Error
}

/**
 * Run `worker` over `items` with at most `limit` running at once, preserving
 * input order in the returned results. A worker that throws is recorded as a
 * failed result rather than rejecting the whole run.
 */
export async function mapWithConcurrency<T>(
  items: T[],
  limit: number,
  worker: (item: T) => Promise<void>,
): Promise<SettledResult<T>[]> {
  const results = new Array<SettledResult<T>>(items.length)
  let cursor = 0

  const runOne = async (): Promise<void> => {
    for (;;) {
      const i = cursor++
      if (i >= items.length) return
      try {
        await worker(items[i])
        results[i] = { item: items[i], ok: true }
      } catch (e) {
        results[i] = { item: items[i], ok: false, error: e instanceof Error ? e : new Error(String(e)) }
      }
    }
  }

  const workers = Array.from({ length: Math.max(1, Math.min(limit, items.length)) }, runOne)
  await Promise.all(workers)
  return results
}
