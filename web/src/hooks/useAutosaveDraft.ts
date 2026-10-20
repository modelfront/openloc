import { useEffect, useRef, useState } from 'react'
import { useDebouncedCallback } from './useDebouncedCallback'
import { DRAFT_SAVE_DELAY_MS } from './useProjectDraft'

interface Options<T> {
  /** The saved value, as the field shows it. The draft follows it when it changes. */
  stored: string
  /** The value to save, or null when the text can't be saved (empty, invalid). */
  parse: (raw: string) => T | null
  /** How a parsed value reads in the field; also what is compared to skip a repeat save. */
  format: (value: T) => string
  /**
   * Saves the value. May return a promise: resolving to `false`, or
   * rejecting, means the save failed — the field shows the stored value again
   * (unless it was edited since) and the same value can be saved again. While
   * a promise is pending, later saves wait, and only the newest one is sent.
   */
  onSave: (value: T) => unknown
  /**
   * Save on every keystroke instead of after a pause — for a form that only
   * collects values (nothing goes to the server until it is submitted).
   */
  instant?: boolean
}

/**
 * A text field that saves itself: a valid value saves `DRAFT_SAVE_DELAY_MS`
 * after the last keystroke while the field still has focus, and at once on
 * blur. Typing "25" never saves a 2 on the way. A value that equals the last
 * saved one is not saved again, so a blur right after the pause adds no second
 * save. One save is on its way at a time, so saves reach the server in order.
 * Blur with text that can't be saved puts the stored value back. A save
 * still waiting when the field unmounts goes through. A new stored value shows
 * in the field unless the field holds edits not saved yet.
 *
 * The draft is a string so the field can be emptied while typing.
 */
export function useAutosaveDraft<T>({ stored, parse, format, onSave, instant = false }: Options<T>) {
  // draft: what the field shows. saved: the last value saved or loaded, as
  // text — a save of the same text is skipped. seen: the newest stored value,
  // which a refused save falls back to however late its answer comes.
  const [state, setState] = useState({ draft: stored, saved: stored, seen: stored })
  const { draft, saved } = state
  // True while a save is on its way or waiting for one.
  const [busy, setBusy] = useState(false)
  // Follow the stored value when it changes, unless the field holds edits not
  // saved yet, or a save of this field is still pending: the answer to an
  // earlier save must not pull back what was typed since. Adjusted during
  // render, React's pattern for state that tracks a prop.
  if (stored !== state.seen) {
    setState(busy || draft !== saved
      ? { ...state, seen: stored, saved: busy ? saved : stored }
      : { draft: stored, saved: stored, seen: stored })
  }

  // The latest onSave: a queued save runs after a later render, and must not
  // use a callback that closed over older state.
  const onSaveRef = useRef(onSave)
  useEffect(() => { onSaveRef.current = onSave })
  const inFlight = useRef(false)
  const queued = useRef<{ value: T; text: string } | null>(null)

  // The server did not take `text`: forget it was saved so it can be tried
  // again, and show the newest stored value unless the field has moved on.
  const refused = (text: string) => setState(s => ({
    ...s,
    saved: s.saved === text ? s.seen : s.saved,
    draft: s.draft === text ? s.seen : s.draft,
  }))

  const send = (value: T, text: string) => {
    const result = onSaveRef.current(value)
    if (!(result instanceof Promise)) return
    inFlight.current = true
    setBusy(true)
    result
      .then(ok => { if (ok === false) refused(text) }, () => refused(text))
      .finally(() => {
        inFlight.current = false
        const next = queued.current
        queued.current = null
        if (next) send(next.value, next.text)
        else setBusy(false)
      })
  }

  const save = (raw: string): T | null => {
    const value = parse(raw)
    if (value == null) return null
    const text = format(value)
    if (text !== saved) {
      setState(s => ({ ...s, saved: text }))
      if (inFlight.current) queued.current = { value, text }
      else send(value, text)
    }
    return value
  }

  const debouncedSave = useDebouncedCallback((raw: string) => { save(raw) }, DRAFT_SAVE_DELAY_MS)
  useEffect(() => () => debouncedSave.flush(), [debouncedSave])

  const onChange = (raw: string) => {
    setState(s => ({ ...s, draft: raw }))
    if (instant) save(raw)
    else debouncedSave(raw)
  }

  const onBlur = () => {
    debouncedSave.cancel()
    const value = save(draft)
    const shown = value == null ? stored : format(value)
    setState(s => ({ ...s, draft: shown }))
  }

  return { value: draft, onChange, onBlur }
}
