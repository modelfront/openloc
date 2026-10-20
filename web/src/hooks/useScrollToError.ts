import { useEffect } from 'react'
import type { FieldErrors } from 'react-hook-form'
import { drawAttention } from '@/lib/attention'

/**
 * Takes the user to the first validation error when form submission fails:
 * scrolls the field into view, rings it and focuses it. Finds the element by
 * `name` attribute (for registered inputs) or `data-field` attribute (for
 * Controller-wrapped custom components, whose first control takes the focus).
 */
export function useScrollToError(errors: FieldErrors) {
  useEffect(() => {
    const key = Object.keys(errors)[0]
    if (!key) return
    const wrapper = `[data-field="${key}"]`
    drawAttention({
      target: `[name="${key}"], ${wrapper}`,
      focus: `[name="${key}"], ${wrapper} input, ${wrapper} textarea, ${wrapper} select, ${wrapper} button`,
    })
  }, [errors])
}
