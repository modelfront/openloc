// The example question an empty conversation offers. A linguist gets one
// about their tasks; a manager in an organization with no projects yet gets a
// setup question, otherwise one about the work in progress.
import { isLinguistRole } from '@/lib/roles'

export const LINGUIST_STARTER = 'What are my open tasks?'
export const SETUP_STARTER = 'Help me create my first project'
export const WORK_STARTER = 'What needs my attention today?'

/** `hasProjects` is undefined while it is still loading; so is the result. */
export function chatStarter(role: string | undefined, hasProjects: boolean | undefined): string | undefined {
  if (isLinguistRole(role)) return LINGUIST_STARTER
  if (hasProjects === undefined) return undefined
  return hasProjects ? WORK_STARTER : SETUP_STARTER
}
