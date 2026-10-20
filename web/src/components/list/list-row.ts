/**
 * Shared shell for an editable list row — the clickable card that holds a name,
 * some chips, and trailing actions. Row height and chrome are tuned here, in
 * one place.
 *
 * Deliberately slim: `text-sm` + a 40px floor keeps a list of ten rows
 * scannable without scrolling.
 *
 * The height comes from `min-h-10`, not from padding, because a row's real
 * height is set by whatever is tallest inside it — usually a `size="icon-sm"`
 * action button (28px). Padding alone therefore gives rows-with-actions and
 * rows-without different heights, and leaves `AddItemButton` (text only)
 * shorter than both. `LIST_ROW_HEIGHT` is the single value all of them clamp
 * to; `AddItemButton` imports it for exactly that reason.
 *
 * Compose with `cn()` at the call site for state classes (cursor/hover when the
 * row is clickable, `border border-dashed` for a read-only built-in) —
 * tailwind-merge resolves the border override against the base.
 */
export const LIST_ROW_HEIGHT = 'min-h-10'

/**
 * Shell for a roomier list card — one bordered card per item, the whole card
 * clickable (Projects, Tasks, Translation memories, Terminology, Templates).
 * Add `cursor-pointer` or a disabled state at the call site; `group` lets
 * children react to the hover.
 */
export const listCardClass =
  'block p-4 bg-card rounded-lg border border-gray-200 dark:border-gray-800 hover:border-primary transition-colors group'

export const listRowClass =
  `px-3 py-1 ${LIST_ROW_HEIGHT} text-sm bg-card rounded-md border border-gray-200 dark:border-gray-800 flex items-center justify-between gap-4`

/**
 * Top line of a `listCardClass` card: the title cluster on the left, dates and
 * actions on the right. On a phone the right side wraps under the title.
 */
export const listCardHeaderClass = 'flex max-md:flex-wrap items-center justify-between gap-y-1'

/**
 * The title cluster inside `listCardHeaderClass`; the call site adds its gap.
 * On a phone it keeps 12rem, so the right side wraps before the name is cut to
 * nothing.
 */
export const listCardTitleClass = 'flex max-md:flex-wrap items-center gap-y-1 min-w-0 max-md:min-w-48 flex-1'
