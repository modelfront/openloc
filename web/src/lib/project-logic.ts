/**
 * Pure project-page logic: pipeline state and job-action eligibility.
 */

import type { ChipColor } from '@/components/ui/chip'

/**
 * pipeline_status='in_progress' covers two distinct states:
 * (a) pipeline workers actively chewing through segments, and (b) pipeline
 * finished, awaiting human Complete ("completed" is reserved for /complete).
 * We distinguish them by `current_step`: the workers write
 * labels ending in "complete" (e.g. "Translation complete", "Pipeline
 * complete", "tm-apply complete") when a phase finishes.
 *
 * Without this distinction the "Translating N/N" badge and the 2-second
 * job-detail polling would never stop after an upload.
 */
export function isPipelineRunning(j: { pipeline_status: string; current_step?: string | null }): boolean {
  if (j.pipeline_status !== 'queued' && j.pipeline_status !== 'in_progress') return false
  return !(j.current_step?.toLowerCase().endsWith('complete') ?? false)
}

/**
 * Editor lock: true while this job's AI step is running — i.e. the
 * pipeline is live AND current_step carries an AI-step label. The backend
 * writes "Queued for AI step" → "Running AI step" → "AI step complete"; the
 * finish label ends in "complete", so isPipelineRunning already excludes it.
 * Keep this matcher in sync with the backend labels in
 * internal/worker/enqueue.go + handler.go.
 *
 * Matches the mode-independent "AI step" marker the backend writes
 * (pipeline.AIStepMarker). The step's own mode — QE, APE or QE+APE — rides
 * along in parentheses and is NOT matched: the lock must hold for every mode,
 * so it cannot key on any one mode's name.
 *
 * "QE+APE" and "QE/APE" stay matched because labels are stored on the job row:
 * a job in flight across a deploy still carries the older wording, and dropping
 * it would unlock the editor mid-step.
 *
 * The AI step only — pre-translation (TM/MT) stays editable and does NOT trip
 * this.
 */
export function isAIStepRunning(j: { pipeline_status: string; current_step?: string | null }): boolean {
  return isPipelineRunning(j) && /AI step|QE[+/]APE/i.test(j.current_step ?? '')
}

/**
 * True while a stop is pending on a running pipeline: the backend keeps
 * reporting in_progress until the current chunk finishes, and flags the
 * pending stop via `stop_requested_at`.
 */
export function isStopRequested(
  j: { pipeline_status: string; current_step?: string | null; stop_requested_at?: string | null },
): boolean {
  return isPipelineRunning(j) && !!j.stop_requested_at
}

/**
 * The "Stopped — N / M segments translated; run Pre-translate again to resume"
 * note the backend writes into `current_step` when a run is stopped. Null for
 * every other pipeline state.
 */
export function stoppedNote(j: { pipeline_status: string; current_step?: string | null }): string | null {
  return j.pipeline_status === 'stopped' && j.current_step ? j.current_step : null
}

export interface PipelineBadgeInfo {
  label: string
  color: ChipColor
  /** whether to animate-pulse the badge (always true while running) */
  pulse: boolean
  /** What the counter counts in the running phase, for the badge's hover text. */
  detail?: string
}

/**
 * The counter is per phase: TM lookup counts matches among the segments it
 * looks up, MT counts segments done among those still empty after TM. The
 * phase is read from the `current_step` prefix the progress writers set
 * ("TM lookup: …" / "TM: …", "MT: …").
 */
function counterDetail(step: string | null | undefined, done: number, total: number): string | undefined {
  const n = done.toLocaleString(), m = total.toLocaleString()
  if (/^TM\b/i.test(step ?? '')) return `TM lookup: ${n} matches found in ${m} segments`
  if (/^MT\b/i.test(step ?? '')) return `Machine translation: ${n} of ${m} empty segments`
  return undefined
}

/**
 * The badge to show while a job's pipeline is running, DRY across ProjectDetail
 * rows and the CAT editor header. A pending stop is yellow — it needs someone
 * to wait, not a progress reading. Everything else is blue + pulsing, because
 * both are "in progress"; only the label differs. QE+APE names its
 * `current_step`; ordinary pre-translation shows the "Translating N/M" counter
 * with the phase it counts in `detail`, falling back to `current_step` when
 * counts aren't populated yet.
 * Returns null when nothing is running (caller falls back to status/step pills).
 */
export function pipelineBadge(
  j: {
    pipeline_status: string
    current_step?: string | null
    translated_count?: number | null
    total_count?: number | null
    stop_requested_at?: string | null
  },
): PipelineBadgeInfo | null {
  if (!isPipelineRunning(j)) return null
  if (isStopRequested(j)) {
    return { label: 'Stopping…', color: 'yellow', pulse: true }
  }
  if (isAIStepRunning(j)) return { label: j.current_step || 'AI step', color: 'blue', pulse: true }
  if (j.translated_count != null && j.total_count != null) {
    return {
      label: `Translating ${j.translated_count.toLocaleString()} / ${j.total_count.toLocaleString()}`,
      color: 'blue',
      pulse: true,
      detail: counterDetail(j.current_step, j.translated_count, j.total_count),
    }
  }
  return { label: j.current_step || 'Processing...', color: 'blue', pulse: true }
}

// --- Bulk job-action eligibility ------------------------------------------
// These mirror the per-row gating in JobPipelineActions so the
// bulk bar acts on exactly the jobs whose individual buttons would be live.
// Kept pure + structurally typed so they're unit-testable without the full
// api `Job` shape.

/** First workflow step ⇔ no workflow OR the active step has step_order 0. */
export function jobInFirstStep(j: { workflow_step?: { step_order: number } | null }): boolean {
  return !j.workflow_step || j.workflow_step.step_order === 0
}

/**
 * Clear is allowed up to and including the workflow's first human or hybrid
 * step; automated steps before it (Pre-translation, AI) don't count. The
 * server computes where that ends (`last_clear_step_order`) and enforces it.
 */
export function inClearWindow(
  step: { step_order: number } | null | undefined,
  lastClearStepOrder: number,
): boolean {
  return !step || step.step_order <= lastClearStepOrder
}

/** `inClearWindow` for a job row, which carries only its active step. */
export function jobInClearWindow(
  j: { workflow_step?: { step_order: number } | null; last_clear_step_order: number },
): boolean {
  return inClearWindow(j.workflow_step, j.last_clear_step_order)
}

/**
 * Apply TM / Apply MT are live only in the first step and while no pipeline
 * task is queued/running on the job (stacking runs against one job is blocked
 * per-row; the bulk bar honours the same rule by skipping ineligible jobs).
 */
export function canApplyTMorMT(
  j: { pipeline_status: string; current_step?: string | null; workflow_step?: { step_order: number } | null },
): boolean {
  return jobInFirstStep(j) && !isPipelineRunning(j)
}

/**
 * Clear empties a job's translations — live only up to the first human or
 * hybrid step, while not running, and only when there's something to clear
 * (`clearable_all`, the clear's own count: text that is not locked). The
 * manager check is applied by the caller.
 */
export function canClearJob(
  j: {
    pipeline_status: string
    current_step?: string | null
    workflow_step?: { step_order: number } | null
    last_clear_step_order: number
    counts: { clearable_all: number }
  },
): boolean {
  return jobInClearWindow(j) && !isPipelineRunning(j) && j.counts.clearable_all > 0
}

/**
 * Complete-step is live when the job has an active workflow step, isn't already
 * completed, and no pipeline task is in flight. Mirrors the per-row
 * `showComplete` gate; the backend re-checks assignee/role on advance.
 */
export function canCompleteJob(
  j: {
    pipeline_status: string
    current_step?: string | null
    workflow_step?: { step_order: number } | null
  },
): boolean {
  return j.workflow_step != null && j.pipeline_status !== 'completed' && !isPipelineRunning(j)
}

/**
 * Stop is live while a pipeline task is running and no stop is pending yet.
 * The inverse of the Apply gates: it's the one action for a job mid-run.
 */
export function canStopJob(
  j: { pipeline_status: string; current_step?: string | null; stop_requested_at?: string | null },
): boolean {
  return isPipelineRunning(j) && !j.stop_requested_at
}
