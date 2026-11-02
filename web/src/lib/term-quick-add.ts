import type { JobTermBases } from '../api/terminology'

/**
 * Why the term quick-add is closed for this user on this job, or null when
 * they may add: the list failed to load, the project does not let this user
 * add (managers always may), or no term base for the job's pair allows Write.
 * While the list is still loading the answer is '' — closed, nothing to say
 * yet. The editor shows the reason for the shortcut; the dialog shows it too.
 */
export function quickAddBlockedReason(
  list: JobTermBases | undefined,
  { failed = false, isManager }: { failed?: boolean; isManager: boolean },
): string | null {
  if (failed) return 'The term bases could not be loaded. Try again.'
  if (!list) return ''
  if (!list.can_add) return 'Adding terms is turned off for this project. Ask a project manager to turn on Linguists can add terms.'
  if (list.term_bases.length === 0) {
    return 'No term base on this project takes new terms for this language pair.'
      + (isManager ? ' Add one, or tick Write on one, in the project settings.' : ' Ask a project manager.')
  }
  return null
}
