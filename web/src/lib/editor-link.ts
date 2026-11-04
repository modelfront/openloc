// The CAT editor opens in its own tab, so the list the user came from stays
// where they left it.

/** Route of the CAT editor for one job. */
export function editorPath(projectId: number, jobId: number): string {
  return `/projects/${projectId}/jobs/${jobId}/edit`
}

/**
 * Route of the CAT editor for jobs of one project opened together, in the
 * order given. One job is the single-file editor's route.
 */
export function editorScopePath(projectId: number, jobIds: readonly number[]): string {
  if (jobIds.length === 1) return editorPath(projectId, jobIds[0])
  return `/projects/${projectId}/edit?jobs=${jobIds.join(',')}`
}

/**
 * The job ids of a multi-file editor URL's `jobs` parameter, or null when it
 * is missing or not a list of ids.
 */
export function parseScopeJobs(param: string | null): number[] | null {
  if (!param) return null
  const ids = param.split(',').map(Number)
  return ids.every(id => Number.isSafeInteger(id) && id > 0) ? ids : null
}

/**
 * Open the editor in a new tab from a non-link click (a table row). Falls back
 * to `navigate` when the popup is blocked. No 'noopener' feature here: it makes
 * `window.open` return null even on success, which would read as "blocked".
 */
export function openEditorTab(path: string, navigate: (path: string) => void): void {
  if (!window.open(path, '_blank')) navigate(path)
}
