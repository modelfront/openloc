/**
 * The box style shared by every form control that renders as a bordered field:
 * `Input`, `Textarea` and the `Combobox` trigger. A text field and a dropdown
 * sitting next to each other have to be indistinguishable, so the classes live
 * here once instead of being copy-pasted per component.
 *
 * Size is deliberately absent. Width is each control's own (`Input` fills its
 * parent, `Combobox` is sized by the caller), and height splits two ways —
 * single-line controls take `controlHeightClass`, `Textarea` grows with its
 * content.
 */
export const controlBoxClasses =
  'px-2.5 text-sm rounded-lg border border-input bg-transparent transition-colors hover:border-ring/60 dark:bg-input/30'

/** The 30px single-line control height (`--spacing-input`, index.css). */
export const controlHeightClass = 'h-input'

/**
 * Type size for controls the user types into. 16px on phones because iOS
 * Safari zooms the page when a focused field is smaller; identical to the rest
 * of the controls from `md` up.
 */
export const controlTextClasses = 'text-base md:text-sm'

export const controlFocusWithinClasses =
  'focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50'

/** The same ring for a control that IS the box — `Combobox`. */
export const controlFocusVisibleClasses =
  'outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50'

/** For a box that WRAPS the control — `has-[…]` needs an `aria-invalid` descendant. */
export const controlInvalidClasses =
  'has-[[aria-invalid=true]]:border-destructive has-[[aria-invalid=true]]:ring-3 has-[[aria-invalid=true]]:ring-destructive/20 dark:has-[[aria-invalid=true]]:ring-destructive/40'

/**
 * The same red frame for a control that IS the box and carries `aria-invalid`
 * itself — the `Combobox` trigger. Keep in step with `controlInvalidClasses`.
 */
export const controlInvalidSelfClasses =
  'aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40'

/** For a box that WRAPS the control — `has-disabled:` needs a disabled descendant. */
export const controlDisabledClasses =
  'has-disabled:cursor-not-allowed has-disabled:bg-input/50 has-disabled:opacity-50 has-disabled:hover:border-input dark:has-disabled:bg-input/80'

/**
 * The same treatment for a control that IS the box, with no wrapper to match
 * against — the `Combobox` trigger. Keep in step with `controlDisabledClasses`.
 */
export const controlDisabledSelfClasses =
  'disabled:cursor-not-allowed disabled:bg-input/50 disabled:opacity-50 disabled:hover:border-input dark:disabled:bg-input/80'

/**
 * Every state a box that WRAPS its control needs: the border, the focus ring,
 * invalid and disabled. Reach for this rather than the parts — picking the
 * parts by hand is how a frame ends up looking editable while the control
 * inside it is disabled. Typography is left out on purpose: a field the user
 * types into adds `controlTextClasses`, a frame around other content does not.
 */
export const controlFrameClasses = [
  controlBoxClasses,
  controlFocusWithinClasses,
  controlInvalidClasses,
  controlDisabledClasses,
].join(' ')
