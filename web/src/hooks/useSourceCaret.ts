import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ComponentPropsWithRef } from 'react'
import { sourceOffsetAt, sourcePointAt } from '../lib/source-caret'

/**
 * Lets the user put a caret in the active row's source text so Split can cut
 * there. `fieldProps` go on the element that holds the source: the browser
 * draws a caret only in an editable element, so it is contentEditable, and a
 * native `beforeinput` listener cancels every edit before it lands (React's
 * onBeforeInput covers typed characters only, not deletions). The few edits a
 * browser will not cancel (IME composition) bump `fieldKey`, which remounts
 * the element from the source the row renders, with the caret put back.
 *
 * `caretRef` remembers the last caret position in the source, as an offset in
 * its plain source (lib/source-caret.ts), so the Alt+Shift+T shortcut works
 * after a click into the translation too; null until the user clicks in the
 * source (with focus there), and reset when the row becomes active again.
 * `hasCaret` says whether there is one yet.
 */
export function useSourceCaret(enabled: boolean) {
  const caretRef = useRef<number | null>(null)
  const fieldRef = useRef<HTMLDivElement | null>(null)
  const [hasCaret, setHasCaret] = useState(false)
  const [fieldKey, setFieldKey] = useState(0)

  useEffect(() => {
    caretRef.current = null
    if (!enabled) return
    const onSelectionChange = () => {
      // Only a caret the user put in the source counts: the click that
      // opens the row selects source text before the translation takes focus.
      const field = fieldRef.current
      const sel = document.getSelection()
      if (!field || !sel?.focusNode || !field.contains(document.activeElement)) return
      const offset = sourceOffsetAt(field, sel.focusNode, sel.focusOffset)
      if (offset === null) return
      caretRef.current = offset
      setHasCaret(true)
    }
    document.addEventListener('selectionchange', onSelectionChange)
    return () => {
      document.removeEventListener('selectionchange', onSelectionChange)
      setHasCaret(false)
    }
  }, [enabled])

  useLayoutEffect(() => {
    const field = fieldRef.current
    const at = caretRef.current
    if (fieldKey === 0 || !field || at === null) return
    const point = sourcePointAt(field, at)
    if (!point) return
    field.focus()
    document.getSelection()?.collapse(point.node, point.offset)
  }, [fieldKey])

  const attach = useCallback((el: HTMLDivElement | null) => {
    if (!el) return
    fieldRef.current = el
    const cancel = (e: Event) => e.preventDefault()
    const remount = (e: Event) => {
      if (!(e as InputEvent).isComposing) setFieldKey(k => k + 1)
    }
    el.addEventListener('beforeinput', cancel)
    el.addEventListener('input', remount)
    el.addEventListener('compositionend', remount)
    return () => {
      el.removeEventListener('beforeinput', cancel)
      el.removeEventListener('input', remount)
      el.removeEventListener('compositionend', remount)
      if (fieldRef.current === el) fieldRef.current = null
    }
  }, [])

  const fieldProps: ComponentPropsWithRef<'div'> = enabled ? {
    ref: attach,
    contentEditable: true,
    suppressContentEditableWarning: true,
    spellCheck: false,
    role: 'textbox',
    'aria-readonly': true,
    'aria-label': 'Source text',
  } : {}

  return { caretRef, hasCaret: enabled && hasCaret, fieldProps, fieldKey }
}
