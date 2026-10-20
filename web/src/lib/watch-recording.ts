// Watch mode: while the user repeats the steps that cause a problem, the
// browser records the same fixed set every time — the trail's events, what
// the user types (never a password or card field, nor anything marked
// data-private), the first 2 KB of each failed API response, and one
// screenshot at the first error. The user starts and stops it; it stops by
// itself after 3 minutes. The recording goes with the report to the support
// team alone: the assistant only learns that it exists.
import type { IssueRecording, RecordingEvent } from '@/api'
import { cap, onApiFailure, onTrailEvent } from './action-trail'
import { onSupportPage } from './issue-context'
import { capturePage } from './screenshot'

export const RECORDING_MAX_MS = 3 * 60_000
/** The server keeps the same caps. */
const RECORDING_MAX_EVENTS = 200
const MAX_VALUE = 500
const MAX_BODY = 2048
const MAX_TARGET = 60

/**
 * A finished recording, held until a report takes it. The screenshot taken at
 * the first error may still be rendering when the recording stops.
 */
export interface HeldRecording {
  recording: IssueRecording
  screenshot: Promise<File | undefined>
}

let held: HeldRecording | undefined

/** The last finished recording, until a report takes it. */
export const heldRecording = () => held

export const clearHeldRecording = () => { held = undefined }

const CARD_FIELD = /card|cvc|cvv|iban|security.?code/i
const SECRET_AUTOCOMPLETE = new Set(['current-password', 'new-password', 'one-time-code'])

/** Whether what the user types in el may be recorded. */
export function recordableField(el: Element): boolean {
  if (el.closest('[data-private]')) return false
  if (el instanceof HTMLInputElement) {
    if (['password', 'hidden', 'file', 'checkbox', 'radio', 'submit', 'button'].includes(el.type)) return false
    if (el.autocomplete.startsWith('cc-') || SECRET_AUTOCOMPLETE.has(el.autocomplete)) return false
    return !CARD_FIELD.test(`${el.name} ${el.id} ${el.getAttribute('aria-label') ?? ''} ${el.placeholder}`)
  }
  return el instanceof HTMLTextAreaElement || (el instanceof HTMLElement && el.isContentEditable)
}

/** What a field is called: its label, else its placeholder, name or data-guide id. */
function fieldLabel(el: Element): string {
  const labelled = el.getAttribute('aria-label')
    || (el.id && document.querySelector(`label[for="${CSS.escape(el.id)}"]`)?.textContent)
    || el.closest('label')?.textContent
    || el.getAttribute('placeholder')
    || el.getAttribute('name')
    || el.closest('[data-guide]')?.getAttribute('data-guide')
    || 'a field'
  return cap(labelled, MAX_TARGET)
}

const fieldValue = (el: Element) =>
  el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement ? el.value : (el.textContent ?? '')

/** The field typing goes into: the element itself, or the editable region around it. */
const typedField = (el: Element) => (el instanceof HTMLElement && el.isContentEditable && !(el instanceof HTMLInputElement)
  ? el.closest('[contenteditable="true"]') ?? el : el)

/**
 * Starts recording; returns the stop function. onStop gets the recording,
 * which is also held for the report.
 */
export function startRecording(onStop: (h: HeldRecording) => void): () => void {
  const startedAt = new Date().toISOString()
  const events: RecordingEvent[] = []
  const push = (e: RecordingEvent) => { if (events.length < RECORDING_MAX_EVENTS) events.push(e) }
  let screenshot: Promise<File | undefined> | undefined
  const firstError = () => { screenshot ??= capturePage().catch(() => undefined) }

  const offTrail = onTrailEvent(e => {
    if (e.kind === 'api') return // recorded below, with the response
    push({ ...e })
    if (e.kind === 'console') firstError()
  })
  // Trail events already skip the support team's pages; the recording's own
  // API and typing events skip them here.
  const offApi = onApiFailure(({ method, path, response }) => {
    if (onSupportPage()) return
    const event: RecordingEvent = { at: new Date().toISOString(), kind: 'api', method, path, status: response?.status ?? 0 }
    push(event)
    firstError()
    response?.clone().text()
      .then(text => { if (text) event.body = text.slice(0, MAX_BODY) })
      .catch(() => {})
  })
  let lastField: Element | null = null
  const onInput = (e: Event) => {
    if (!(e.target instanceof Element) || onSupportPage()) return
    const field = typedField(e.target)
    if (!recordableField(field)) return
    const value = cap(fieldValue(field), MAX_VALUE)
    const last = events.at(-1)
    // Keystrokes in one field make one event with the field's latest value.
    if (last?.kind === 'input' && lastField === field) last.value = value
    else push({ at: new Date().toISOString(), kind: 'input', target: fieldLabel(field), value })
    lastField = field
  }
  document.addEventListener('input', onInput, true)

  let stopped = false
  const stop = () => {
    if (stopped) return
    stopped = true
    clearTimeout(timer)
    offTrail()
    offApi()
    document.removeEventListener('input', onInput, true)
    held = {
      recording: { started_at: startedAt, ended_at: new Date().toISOString(), events },
      screenshot: (screenshot ?? Promise.resolve(undefined))
        .then(shot => shot && new File([shot], 'recording-first-error.jpg', { type: shot.type })),
    }
    onStop(held)
  }
  const timer = setTimeout(stop, RECORDING_MAX_MS)
  return stop
}
