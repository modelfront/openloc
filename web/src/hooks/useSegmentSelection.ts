import { useCallback, useLayoutEffect, useMemo, useRef, useState } from 'react'

import { clickSelect, headerState, setMany, visibleSelection } from '../lib/segment-selection'

/**
 * The CAT editor's segment selection. `ordered` is the translation ids of the
 * filtered rows, in display order.
 *
 * `onCheckboxClick` and `toggle` are identity-stable and read the latest state
 * through refs: memoized rows that did not re-render still call them safely.
 */
export function useSegmentSelection(ordered: readonly number[]) {
  const [selected, setSelected] = useState<ReadonlySet<number>>(() => new Set())
  const orderedRef = useRef(ordered)
  useLayoutEffect(() => { orderedRef.current = ordered }, [ordered])
  // The last row clicked without Shift: where a Shift+click range starts.
  const anchorRef = useRef<number | null>(null)

  const onCheckboxClick = useCallback((id: number, shift: boolean) => {
    setSelected(prev => clickSelect(prev, orderedRef.current, id, anchorRef.current, shift))
    if (!shift) anchorRef.current = id
  }, [])

  const toggle = useCallback((id: number) => {
    setSelected(prev => setMany(prev, [id], !prev.has(id)))
    anchorRef.current = id
  }, [])

  const toggleAll = useCallback(() => {
    setSelected(prev => setMany(prev, orderedRef.current, headerState(prev, orderedRef.current) !== 'all'))
  }, [])

  const clear = useCallback(() => {
    setSelected(new Set())
    anchorRef.current = null
  }, [])

  const picked = useMemo(() => visibleSelection(selected, ordered), [selected, ordered])
  const header = useMemo(() => headerState(selected, ordered), [selected, ordered])

  return { selected, picked, header, onCheckboxClick, toggle, toggleAll, clear }
}
