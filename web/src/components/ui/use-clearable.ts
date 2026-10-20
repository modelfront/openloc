import { useCallback, useEffect, useImperativeHandle, useRef, useState, type ChangeEvent, type Ref } from 'react'

/**
 * Input types that get a clear button. An allow-list rather than a block-list:
 * a new exotic type should default to "no X" until someone decides otherwise.
 * `password` and `number` are the notable exclusions — an X on a password is
 * unusual, and a number field's spinners already occupy the right edge.
 */
const CLEARABLE_TYPES = new Set([undefined, 'text', 'search', 'email', 'url', 'tel'])

/**
 * Write a value into the field the way the browser does, so React's `onChange`
 * fires. React binds `onChange` to the native `input` event, which means this
 * one path serves both controlled fields and uncontrolled ones bound with
 * react-hook-form's `register()` — assigning `el.value` directly would update
 * the DOM but notify neither.
 */
function setNativeValue(el: HTMLInputElement, value: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(el, value)
  el.dispatchEvent(new Event('input', { bubbles: true }))
}

export function useClearable({
  ref,
  value,
  defaultValue,
  onChange,
  onClear,
  clearable,
  type,
  disabled,
  readOnly,
}: {
  ref?: Ref<HTMLInputElement>
  value?: unknown
  defaultValue?: unknown
  onChange?: (e: ChangeEvent<HTMLInputElement>) => void
  onClear?: () => void
  clearable?: boolean
  type?: string
  disabled?: boolean
  readOnly?: boolean
}) {
  const innerRef = useRef<HTMLInputElement | null>(null)
  const controlled = value !== undefined

  // Uncontrolled fields (`{...register('name')}`) expose no value prop, so the
  // "is there text to clear?" answer has to come from the DOM.
  const [typedText, setTypedText] = useState(() => String(defaultValue ?? '') !== '')
  const hasText = controlled ? String(value ?? '') !== '' : typedText

  // react-hook-form seeds and resets fields through the ref rather than through
  // props, so neither `defaultValue` nor an onChange ever reports that text
  // arrived. Re-reading the node after each render is the only way to see it;
  // an unchanged boolean bails out of the re-render.
  // eslint-disable-next-line react-hooks/exhaustive-deps -- deliberately dep-less: the value arrives through the DOM, so it has to be re-read every render; an unchanged boolean bails out
  useEffect(() => {
    if (!controlled && innerRef.current) setTypedText(innerRef.current.value !== '')
  })

  // The hook needs the node for `clear()` and the uncontrolled read, and the
  // caller needs its own ref honoured. `useImperativeHandle` hands the same node
  // to the caller's ref — callback or object — without the hook writing to it.
  const setRef = useCallback((node: HTMLInputElement | null) => {
    innerRef.current = node
  }, [])
  // Empty deps: without them React re-attaches on every render, and a callback
  // ref like `focusOnMount` then re-fires `focus()` on every keystroke, yanking
  // the caret out of whatever the user moved to.
  useImperativeHandle(ref, () => innerRef.current as HTMLInputElement, [])

  const handleChange = useCallback(
    (e: ChangeEvent<HTMLInputElement>) => {
      if (!controlled) setTypedText(e.target.value !== '')
      onChange?.(e)
    },
    [controlled, onChange],
  )

  const clear = useCallback(() => {
    const el = innerRef.current
    if (onClear) onClear()
    else if (el) setNativeValue(el, '')
    el?.focus()
  }, [onClear])

  const allowed = clearable ?? (CLEARABLE_TYPES.has(type) && !disabled && !readOnly)

  return { setRef, handleChange, clear, showClear: allowed && hasText }
}
