import { cn } from '@/lib/utils'

/**
 * Intent presets for a muted row action button.
 *
 * - `muted` — neutral gray hover (rename, edit).
 * - `danger` — red hover (delete).
 * - `accent` — default (blue) hover (inline edit).
 */
export type HoverActionIntent = 'muted' | 'danger' | 'accent'

// Always visible. Kept muted so it doesn't
// compete with the row's primary content; the intent classes darken it on hover.
const SHARED = 'transition-colors text-gray-400 dark:text-gray-500'

const INTENT_CLASSES: Record<HoverActionIntent, string> = {
  muted: 'hover:text-gray-600 dark:hover:text-gray-300',
  danger: 'hover:text-red-600 dark:hover:text-red-400',
  accent: 'hover:text-blue-600 dark:hover:text-blue-400',
}

/**
 * Returns the Tailwind classes for a muted row-action button (rename, delete,
 * edit). Always visible — the muted gray keeps it from competing with the row's
 * primary content, and the intent color darkens it on hover.
 *
 * Works with the existing `<Button variant="ghost" size="xs" />` pattern — just
 * replace the inline `className` with `hoverActionClass('danger')`. The helper
 * approach (vs a wrapper component) makes it drop into Tooltip's
 * `<TooltipTrigger render={<Button className={...} />} />` slot without
 * sacrificing ref forwarding.
 *
 * Accepts extra classes for one-off overrides.
 */
export function hoverActionClass(intent: HoverActionIntent = 'muted', extra?: string): string {
  return cn(SHARED, INTENT_CLASSES[intent], extra)
}
