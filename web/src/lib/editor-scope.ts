// The CAT editor shows a scope: one job, or jobs of one project in one target
// language opened together (internal/editor/scope.go). Its caches and its
// server routes are keyed by an editor id: the job id for one job, a
// "<project>:<job>,<job>…" string for several. A job's own cache helpers
// (query-invalidation) also reach every open scope that holds the job, so a
// segment row patches the grid it sits in whichever kind it is.

import type { QueryClient } from '@tanstack/react-query'
import { MaxScopeJobs } from '@/types/editor'

/** Files one multi-file request works on (Complete step, a QA run): a large
 *  editor goes in several requests, each short enough to answer in time. */
export const SCOPE_JOBS_PER_REQUEST = 25

/** A job id, or a multi-file scope's "<project>:<job>,<job>…". */
export type EditorId = number | string

/** The editor id of these jobs of a project: the job itself when only one. */
export function scopeEditorId(projectId: number, jobIds: readonly number[]): EditorId {
  return jobIds.length === 1 ? jobIds[0] : `${projectId}:${jobIds.join(',')}`
}

/** The jobs an editor id stands for. */
export function editorJobIds(id: EditorId): number[] {
  if (typeof id === 'number') return [id]
  return id.slice(id.indexOf(':') + 1).split(',').map(Number)
}

/**
 * The API path of an editor route for this editor id: the job's own
 * `/jobs/{id}/editor/…`, or the project's `/projects/{id}/editor/…?jobs=`.
 * `query` is added as more parameters.
 */
export function editorApiPath(id: EditorId, route: string, query?: URLSearchParams | string): string {
  const extra = query ? String(query) : ''
  if (typeof id === 'number') return `/jobs/${id}/editor/${route}${extra ? `?${extra}` : ''}`
  const [projectId, jobs] = id.split(':')
  return `/projects/${projectId}/editor/${route}?jobs=${jobs}${extra ? `&${extra}` : ''}`
}

/** The editor caches open on this job: its own and every scope holding it. */
export function editorIdsHolding(queryClient: QueryClient, jobId: number): EditorId[] {
  const ids = new Set<EditorId>([jobId])
  for (const q of queryClient.getQueryCache().findAll({ queryKey: ['job-translations'] })) {
    const id = q.queryKey[1]
    if (typeof id === 'string' && editorJobIds(id).includes(jobId)) ids.add(id)
  }
  return [...ids]
}

/** A translation's job, and whether the caller may edit its file now. */
export interface TranslationJob {
  jobId: number
  canEdit: boolean
}

/**
 * The translations by job, in the order given, leaving out unknown ids and
 * those of files the caller may not edit, which are counted. Each job's own
 * endpoint then takes its part.
 */
export function groupByJob(ids: readonly number[], jobOf: (translationId: number) => TranslationJob | undefined) {
  const byJob = new Map<number, number[]>()
  let readOnly = 0
  for (const id of ids) {
    const file = jobOf(id)
    if (!file) continue
    if (!file.canEdit) {
      readOnly++
      continue
    }
    const list = byJob.get(file.jobId)
    if (list) list.push(id)
    else byJob.set(file.jobId, [id])
  }
  return { byJob, readOnly }
}

export interface ScopeCandidate {
  project_id: number
  target_language: string
}

/**
 * Why these jobs cannot open in one editor, or null when they can. The server
 * checks the same rule; this only decides whether "Open in editor" is offered.
 */
export function openTogetherBlock(jobs: readonly ScopeCandidate[]): string | null {
  if (jobs.length === 0) return 'Select the files to open'
  if (jobs.length > MaxScopeJobs) return `Open at most ${MaxScopeJobs} files together`
  if (new Set(jobs.map(j => j.project_id)).size > 1) return 'Select files of one project'
  if (new Set(jobs.map(j => j.target_language)).size > 1) return 'Select files of one target language'
  return null
}
