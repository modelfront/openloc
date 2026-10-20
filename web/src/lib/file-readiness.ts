// The Files tab's model: one summary per file, built from its jobs (one per
// target language), with a delivery readiness — is the file's translation
// done, and if not, where is it?
//
//   failed      a language's pipeline failed
//   processing  a language is being parsed, pre-translated or QE+APE-checked
//   no_mt       a language's pre-translation found no MT engine for it
//   ready       every language finished its workflow: downloadable
//   stopped     a language's run was stopped before it finished
//   in_progress anything else — people are working
//
// Filters live in the URL: `q` (shared with the Tasks tab — a file name
// search means the same there) and `file_status`.

import type { Job } from '../api'
import type { ChipColor } from '@/components/ui/chip'
import { isPipelineRunning } from './project-logic'
import { TASK_STATE_DISPLAY } from './task-matrix'

export const FILE_READINESS = ['processing', 'in_progress', 'no_mt', 'stopped', 'ready', 'failed'] as const
export type FileReadiness = (typeof FILE_READINESS)[number]

export const FILE_READINESS_DISPLAY = {
  processing: { label: 'Processing', color: 'blue', hint: 'Being parsed, pre-translated or checked by a QE+APE step' },
  in_progress: { label: 'In progress', color: 'yellow', hint: 'Translation or review is under way' },
  no_mt: { label: 'No MT', color: 'yellow', hint: 'The MT router has no engine for a target language, so it was not machine translated' },
  // Nothing moves until someone starts the run again, so it reads like a
  // task that has not started.
  stopped: { label: 'Stopped', color: TASK_STATE_DISPLAY.not_started.color, hint: 'A run was stopped before it finished; start it again to go on' },
  ready: { label: 'Ready', color: 'green', hint: 'Every target language has finished its workflow' },
  failed: { label: 'Failed', color: 'red', hint: 'A target language failed to process' },
} as const satisfies Record<FileReadiness, { label: string; color: ChipColor; hint: string }>

export function jobReadiness(job: Job): FileReadiness {
  if (job.pipeline_status === 'failed') return 'failed'
  if (isPipelineRunning(job)) return 'processing'
  if (job.pipeline_status === 'completed') return 'ready'
  const step = job.workflow_step
  if (step?.is_last_step && step.status === 'completed') return 'ready'
  // A finished job is ready whether or not it was machine translated.
  if (job.mt_skip_reason) return 'no_mt'
  if (job.pipeline_status === 'stopped') return 'stopped'
  return 'in_progress'
}

export interface FileSummary {
  fileId: number
  name: string
  format: string
  uploadedAt: string
  jobs: Job[]
  segments: number
  words: number
  readiness: FileReadiness
  /** Target language codes by readiness, sorted. */
  languages: Record<FileReadiness, string[]>
  readyCount: number
  languageCount: number
}

function fileReadiness(states: FileReadiness[]): FileReadiness {
  if (states.includes('failed')) return 'failed'
  if (states.includes('processing')) return 'processing'
  if (states.includes('no_mt')) return 'no_mt'
  if (states.length > 0 && states.every(s => s === 'ready')) return 'ready'
  if (states.includes('stopped')) return 'stopped'
  return 'in_progress'
}

/** One summary per file, in the order the jobs arrive (newest upload first). */
export function summarizeFiles(jobs: Job[]): FileSummary[] {
  const byFile = new Map<number, Job[]>()
  for (const j of jobs) byFile.set(j.file_id, [...(byFile.get(j.file_id) ?? []), j])

  return [...byFile.entries()].map(([fileId, fileJobs]) => {
    const first = fileJobs[0]
    const languages: FileSummary['languages'] = { processing: [], in_progress: [], no_mt: [], stopped: [], ready: [], failed: [] }
    for (const j of fileJobs) {
      const r = jobReadiness(j)
      languages[r].push(j.target_language)
    }
    for (const list of Object.values(languages)) list.sort()
    const states = fileJobs.map(jobReadiness)
    return {
      fileId,
      name: first.file_name,
      format: first.file_format,
      uploadedAt: first.file_created_at,
      jobs: fileJobs,
      segments: first.segment_count,
      words: first.word_count,
      readiness: fileReadiness(states),
      languages,
      readyCount: languages.ready.length,
      languageCount: fileJobs.length,
    }
  })
}

// --- Filters (URL) -----------------------------------------------------------

export interface FileFilters {
  q?: string
  readiness?: FileReadiness
}

export const FILE_FILTER_PARAM_KEYS = ['q', 'file_status'] as const

export function parseFileFilters(params: URLSearchParams): FileFilters {
  const f: FileFilters = {}
  const q = params.get('q')?.trim()
  if (q) f.q = q
  const r = params.get('file_status')
  if (r && (FILE_READINESS as readonly string[]).includes(r)) f.readiness = r as FileReadiness
  return f
}

export function fileMatchesFilters(file: FileSummary, f: FileFilters): boolean {
  if (f.q && !file.name.toLowerCase().includes(f.q.toLowerCase())) return false
  if (f.readiness && file.readiness !== f.readiness) return false
  return true
}
