/**
 * Position the inline spell-check fix menu next to the word it belongs to.
 *
 * A pure function because layout isn't meaningfully assertable in jsdom, but
 * this arithmetic is.
 *
 * Callers MUST render the menu in a portal to `document.body`. The segment grid
 * is virtualized, so rows carry `transform: translateY(...)` — and a transformed
 * ancestor becomes the containing block for `position: fixed`, which silently
 * reinterprets these viewport coordinates as offsets from that ancestor.
 */

/**
 * Panel width. Wider than a plain context menu because the panel mirrors the
 * spell check dialog's content — source text, the flagged target, the message,
 * suggestions and the scope rows — rather than just listing suggestions.
 */
export const FIX_MENU_WIDTH = 360

/** Gap between the underlined word and the menu, and from any viewport edge. */
const GAP = 4
const EDGE_PADDING = 8

/**
 * Rough heights used to decide whether the panel fits below the word. Estimated
 * rather than measured so there's no reflow flash on open; the flip and the
 * clamp only need to be approximately right, and both ends are bounded by
 * `positionFixMenu` regardless.
 */
const ROW_HEIGHT = 28
// Header + source + flagged target + message + "Change to" input + three scope
// rows + two ignore rows.
const FOOTER_HEIGHT = 340

export interface FixMenuAnchor {
  top: number
  left: number
  bottom: number
}

export interface FixMenuPosition {
  top: number
  left: number
  /** Which side of the word the menu ended up on — useful for a11y and tests. */
  placement: 'below' | 'above'
}

export function estimateFixMenuHeight(suggestionCount: number): number {
  // At least one row's worth so a no-suggestions menu still reserves space.
  return Math.max(1, suggestionCount) * ROW_HEIGHT + FOOTER_HEIGHT
}

/**
 * Resolve the menu's fixed-position coordinates.
 *
 * Flips above the word when there isn't room below, and clamps horizontally so
 * a word near the right edge doesn't push the menu out of view.
 */
export function positionFixMenu(
  anchor: FixMenuAnchor,
  suggestionCount: number,
  viewport: { width: number; height: number },
): FixMenuPosition {
  const height = estimateFixMenuHeight(suggestionCount)

  const maxLeft = viewport.width - FIX_MENU_WIDTH - EDGE_PADDING
  // When the viewport is narrower than the menu, maxLeft goes negative; the
  // outer Math.max pins the menu to the left edge rather than off-screen left.
  const left = Math.max(EDGE_PADDING, Math.min(anchor.left, maxLeft))

  const fitsBelow = anchor.bottom + GAP + height <= viewport.height - EDGE_PADDING
  if (fitsBelow) {
    return { top: anchor.bottom + GAP, left, placement: 'below' }
  }

  // Flip above. If it doesn't fit there either (a tall menu in a short
  // viewport), clamp to the top edge — visible and scrolled-into beats offscreen.
  const above = anchor.top - GAP - height
  return { top: Math.max(EDGE_PADDING, above), left, placement: 'above' }
}
