import type { ReactNode } from 'react'
import type { ProjectEvent } from '@/api/project-events'

// Helpers shared by the project history renderers (project-event-render.tsx
// and project-event-render-workflow.tsx). details is free-form jsonb, so every
// read is defensive.

/** Renders the sentence AFTER the actor name ("<actor> <sentence>"). */
export type Renderer = (ev: ProjectEvent) => ReactNode

export function str(v: unknown, fallback = ''): string {
  if (v === null || v === undefined) return fallback
  if (typeof v === 'string') return v
  if (typeof v === 'number' || typeof v === 'boolean') return String(v)
  return fallback
}

export function num(v: unknown): number | null {
  return typeof v === 'number' ? v : null
}

/** The non-empty strings of a list; [] when v is not a list. */
export function strArray(v: unknown): string[] {
  return Array.isArray(v) ? v.map((item) => str(item)).filter(Boolean) : []
}

/** A list of strings, joined "a, b, c"; '' when v is not a list or is empty. */
export function strList(v: unknown): string {
  return strArray(v).join(', ')
}

/** "a", "a and b", "a, b and c". */
export function joinAnd(items: string[]): string {
  if (items.length <= 1) return items.join('')
  return `${items.slice(0, -1).join(', ')} and ${items[items.length - 1]}`
}

/** The person a detail names: the snapshotted display name (`<key>_name`),
 *  else the email older events stored (`<key>_email`); '' when neither. */
export function personName(ev: ProjectEvent, key: string): string {
  return str(ev.details[`${key}_name`]) || str(ev.details[`${key}_email`])
}

/** "file.docx (de)" suffix shared by every job/file-scoped sentence. */
export function fileRef(ev: ProjectEvent): string {
  const name = str(ev.details['file_name'])
  const lang = str(ev.details['target_language'])
  if (!name) return ''
  return lang ? `${name} (${lang})` : name
}
