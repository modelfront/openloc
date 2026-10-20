// Per-job processing-time API client. Backs the project's
// "Processing time" panel: a project-wide rollup plus each job's own stage
// breakdown, read from the accumulating job_timings row.
//
// Manager-gated server-side (same group as analysis/productivity) — it exposes
// per-engine and per-model latency, which is operational data.
import { request } from './request'

/** One stage's aggregate. `n` counts operations, and the unit differs by
 *  stage on purpose — engine round trips for `mt.engine_call`, batched
 *  queries for `tm.exact_lookup`. See internal/timing/stages.go. */
export interface StageEntry {
  stage: string
  /** Slug prefix: 'ingest' | 'tm' | 'mt' | 'ai' | 'feedback' | 'qa' | 'task'. */
  group: string
  n: number
  /**
   * Exclusive time: this stage minus whatever its nested children accounted
   * for. THE number to sum or chart — spans nest (mt.tag_transfer runs inside
   * mt.db_save; a task.* span wraps the whole pipeline), and self_ms is what
   * makes summing them count each millisecond exactly once.
   */
  self_ms: number
  /**
   * Inclusive time, children included. Equal to self_ms for a leaf, larger for
   * a span containing others. Useful to display ("the whole save took 2.2s"),
   * never to sum.
   */
  total_ms: number
  /** Slowest single occurrence, measured inclusively. */
  max_ms: number
  /** Mean inclusive duration per occurrence. */
  avg_ms: number
  err: number
  /** Engine/model/format tags. 'mixed' when jobs in the rollup disagree. */
  meta?: Record<string, string>
  /** 0–1 slice of total self time. Sums to 1 across the list. */
  share: number
}

export interface JobTimingEntry {
  job_id: number
  file_id: number
  file_name: string
  target_language: string
  job_status: string
  word_count: number
  compute_ms: number
  queue_wait_ms: number
  runs: number
  first_started_at: string | null
  last_finished_at: string | null
  stages: StageEntry[]
}

export interface ProjectTimings {
  total_compute_ms: number
  total_queue_wait_ms: number
  total_runs: number
  job_count: number
  total_words: number
  /**
   * Rolled up across every job, ordered by self time descending. Includes the
   * task.* spans: self-time accounting means a span that contains others still
   * only reports what it alone is responsible for, so they can share one list
   * without double-counting. A task span's self time is the uninstrumented
   * overhead inside that task.
   */
  stages: StageEntry[]
  jobs: JobTimingEntry[]
}

export const getProjectTimings = (projectId: number) =>
  request<{ data: ProjectTimings }>(`/projects/${projectId}/timings`).then(r => r.data)
