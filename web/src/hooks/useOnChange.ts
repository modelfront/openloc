import { useState } from 'react'

/**
 * Calls `onChange` during render when any of `deps` changes (compared with
 * Object.is, like an effect's deps). By default not on the first render, so
 * the state it adjusts starts from its `useState` initial value; pass
 * `{ onMount: true }` to run it on the first render too, as an effect would.
 *
 * React's way to adjust state when a prop changes
 * (https://react.dev/learn/you-might-not-need-an-effect#adjusting-some-state-when-a-prop-changes):
 * the adjusted state lands in this render, not in a second render after the
 * stale one has painted. `onChange` may set only this component's own state.
 */
export function useOnChange(deps: readonly unknown[], onChange: () => void, { onMount = false } = {}): void {
  const [prev, setPrev] = useState<readonly unknown[] | null>(onMount ? null : deps)
  if (prev === null || deps.length !== prev.length || deps.some((d, i) => !Object.is(d, prev[i]))) {
    setPrev(deps)
    onChange()
  }
}
