import type { Label } from '../../api'

export function projectsCount(n: number) {
  return `${n} project${n === 1 ? '' : 's'}`
}

const MAX_LISTED = 3

/**
 * Why the label's bin is disabled, or null when it can be deleted. Built-ins
 * are not covered: they have no bin at all.
 */
export function labelDeleteBlocker(label: Label): string | null {
  const used = label.values.filter(v => v.in_use)
  if (used.length === 0) return null
  const listed = used.slice(0, MAX_LISTED).map(v => `${v.value} (${projectsCount(v.project_count)})`)
  if (used.length > MAX_LISTED) listed.push(`${used.length - MAX_LISTED} more`)
  const names = listed.length === 1 ? listed[0] : `${listed.slice(0, -1).join(', ')} and ${listed[listed.length - 1]}`
  const [verb, those] = used.length === 1 ? ['is', 'that value'] : ['are', 'those values']
  return `Can't delete yet: ${names} ${verb} still in use. Delete ${those} first, or change the projects.`
}
