import { useEffect, useRef } from 'react'

/**
 * Keeps a create form's name field in sync with a suggestion derived from the
 * user's other choices — until the user names the thing themselves, at which
 * point it never touches the field again.
 *
 * Ownership is tracked by remembering the last value written: the field is
 * "still ours" only while it holds that value (or a placeholder we were given,
 * or nothing). Anything else means the user typed, so the suggestion stops
 * applying even though it keeps changing underneath.
 *
 * This is the same rule the segment-prompt default follows:
 * a follow-on default may replace a default, never a
 * choice. Deliberately NOT react-hook-form's `dirtyFields`, which is measured
 * against `defaultValues` — typing the placeholder back would clear the flag
 * and resume overwriting.
 *
 * `enabled` should be false when editing an existing entity: a saved name is
 * always the user's, whoever originally generated it.
 */
export function useAutoName({
  enabled, current, suggestion, placeholders, onChange,
}: {
  enabled: boolean
  /** The field's current value (watched). */
  current: string
  /** The name to apply; falsy means "nothing to suggest yet", so leave it be. */
  suggestion: string
  /** Initial values that count as unclaimed, e.g. 'New provider'. */
  placeholders: string[]
  onChange: (name: string) => void
}) {
  const lastWritten = useRef<string | null>(null)

  useEffect(() => {
    if (!enabled || !suggestion) return
    const value = (current ?? '').trim()
    const ours = value === '' || value === lastWritten.current || placeholders.includes(value)
    if (!ours || value === suggestion) return
    lastWritten.current = suggestion
    onChange(suggestion)
    // `current` is intentionally read but not depended on: reacting to it would
    // re-fire on every keystroke, and the guard above already needs only the
    // value at suggestion-change time.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, suggestion])
}
