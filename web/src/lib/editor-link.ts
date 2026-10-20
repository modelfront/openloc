// The CAT editor opens in its own tab, so the list the user came from stays
// where they left it.

/** Route of the CAT editor for one job. */
export function editorPath(projectId: number, jobId: number): string {
  return `/projects/${projectId}/jobs/${jobId}/edit`
}

/** Spread onto a router `<Link>` to the editor. */
export const EDITOR_LINK_TARGET = { target: '_blank', rel: 'noopener noreferrer' } as const

/**
 * Open the editor in a new tab from a non-link click (a table row). Falls back
 * to `navigate` when the popup is blocked. No 'noopener' feature here: it makes
 * `window.open` return null even on success, which would read as "blocked".
 */
export function openEditorTab(path: string, navigate: (path: string) => void): void {
  if (!window.open(path, '_blank')) navigate(path)
}
