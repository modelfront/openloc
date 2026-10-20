// The widths a user dragged the Tasks tab's columns to, remembered per
// browser. A column reset by double-click leaves the map.

import type { ColumnSizingState } from '@tanstack/react-table'
import { useStoredState } from '@/hooks/useStoredState'

function parseSizing(stored: unknown): ColumnSizingState {
  if (!stored || typeof stored !== 'object') return {}
  return Object.fromEntries(Object.entries(stored).filter(([, v]) => typeof v === 'number' && v > 0))
}

// The key names the full-screen view, where the matrix lives, so widths saved
// there are kept.
export function useColumnSizing() {
  return useStoredState('task-matrix-column-widths:fullScreen', parseSizing)
}

/** The step tables' widths: one set for every step, so the tables line up. */
export function useStepColumnSizing() {
  return useStoredState('task-step-column-widths', parseSizing)
}
