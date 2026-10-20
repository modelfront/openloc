import { cva, type VariantProps } from 'class-variance-authority'

import { cn } from '@/lib/utils'
import { controlFocusVisibleClasses } from '@/components/ui/control-box'

// Chip — the one small colored label in the app.
//
// Every status pill, provenance marker, role tag and count badge with a color
// goes through here. The API is deliberately short: a closed set of colors,
// three sizes (`small` / `default` / `large`), an optional leading icon. Call sites pick
// a color; this component owns the light/dark background + text + ring, so
// nobody hand-writes a six-class color triple.
//
// Distinct from the shadcn <Badge> primitive, which is the THEME-TOKEN lozenge
// for counts and inline actions — its variants tint from bg-primary /
// bg-secondary / bg-destructive, never from a Tailwind palette. A Badge
// carrying palette classes in its className is an ESLint error — see
// web/eslint.config.js.
//
// Extend the color map here, never at the call site. The `grey` / `yellow` /
// `green` / `red` prop names are the design vocabulary; the Tailwind palettes
// behind them (gray / amber / emerald / rose) are an implementation detail.
//
// TWO PALETTES. The freeform five (`ChipColor`) are what a call site may
// choose from and what every status/provenance map is typed against. The
// preset palette adds hues reserved for NAMED chips defined in
// `chip-presets.tsx` — a fixed thing like an AI workflow step, never a
// free choice. Keeping them out of `ChipColor` is what stops the freeform
// palette drifting back to a dozen hues: to use violet you have to name the
// chip, and naming it means every instance of that thing looks the same.
const chipVariants = cva(
  'inline-flex items-center gap-1 font-medium whitespace-nowrap ring-1 ring-inset [&_svg]:size-3 [&_svg]:shrink-0',
  {
    variants: {
      color: {
        // --- Freeform palette (ChipColor) ---
        grey: 'bg-gray-100 text-gray-700 ring-gray-200 dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-700',
        blue: 'bg-blue-100 text-blue-700 ring-blue-200 dark:bg-blue-950/40 dark:text-blue-300 dark:ring-blue-900',
        yellow: 'bg-amber-100 text-amber-700 ring-amber-200 dark:bg-amber-950/40 dark:text-amber-300 dark:ring-amber-900',
        green: 'bg-emerald-100 text-emerald-700 ring-emerald-200 dark:bg-emerald-950/40 dark:text-emerald-300 dark:ring-emerald-900',
        red: 'bg-rose-100 text-rose-700 ring-rose-200 dark:bg-rose-950/40 dark:text-rose-300 dark:ring-rose-900',
        // --- Preset palette: for named chips in chip-presets.tsx ---
        violet: 'bg-violet-100 text-violet-700 ring-violet-200 dark:bg-violet-950/40 dark:text-violet-300 dark:ring-violet-900',
        indigo: 'bg-indigo-100 text-indigo-700 ring-indigo-200 dark:bg-indigo-950/40 dark:text-indigo-300 dark:ring-indigo-900',
      },
      // `large` is the roomier list-row size (workflow step chips carry
      // annotations that must fit on one line). `small` is the editor
      // sidebar's, where a 220–400px column puts a chip next to a name and a
      // timestamp on one line. A named size belongs here, not spread across
      // call sites.
      size: {
        small: 'rounded-md px-1 py-0 text-[10px]',
        default: 'rounded-md px-2 py-0.5 text-xs',
        large: 'rounded-md px-2.5 py-1 text-sm',
      },
    },
    defaultVariants: { color: 'grey', size: 'default' },
  },
)

/** Every color the component can render, freeform + preset. */
export type ChipAnyColor = NonNullable<VariantProps<typeof chipVariants>['color']>

/** Preset-only hues. Reserved for named chips in `chip-presets.tsx`. */
export const CHIP_PRESET_COLORS = ['violet', 'indigo'] as const

/**
 * The freeform palette — the only colors a call site may pick, and the type
 * every status/provenance/role map is written against. Derived by subtraction
 * so adding a preset hue above cannot silently widen it.
 */
export type ChipColor = Exclude<ChipAnyColor, (typeof CHIP_PRESET_COLORS)[number]>

// Text-only counterpart of each color, for an annotation that belongs to a
// chip but sits beside it rather than inside it (a step's engine name next
// to its type marker). Same palette words as the variants above, so a palette
// change moves both. Keyed by ChipAnyColor so preset chips get one too.
const CHIP_TEXT_CLASS: Record<ChipAnyColor, string> = {
  grey: 'text-gray-700 dark:text-gray-300',
  blue: 'text-blue-700 dark:text-blue-300',
  yellow: 'text-amber-700 dark:text-amber-300',
  green: 'text-emerald-700 dark:text-emerald-300',
  red: 'text-rose-700 dark:text-rose-300',
  violet: 'text-violet-700 dark:text-violet-300',
  indigo: 'text-indigo-700 dark:text-indigo-300',
}

export function chipTextClass(color: ChipAnyColor): string {
  return CHIP_TEXT_CLASS[color]
}

// Background-only counterpart of each freeform color, for a surface larger
// than a chip that carries the same state — a task-matrix cell. The chip's own
// shade: a chip on top still reads through its ring. Dark yellow is the
// exception: amber-950 is a brown that reads as the red surface beside it,
// so it is a wash of yellow-500 instead.
const CHIP_SURFACE_CLASS: Record<ChipColor, string> = {
  grey: 'bg-gray-100 dark:bg-gray-800/70',
  blue: 'bg-blue-100 dark:bg-blue-950/60',
  yellow: 'bg-amber-100 dark:bg-yellow-500/20',
  green: 'bg-emerald-100 dark:bg-emerald-950/60',
  red: 'bg-rose-100 dark:bg-rose-950/60',
}

export function chipSurfaceClass(color: ChipColor): string {
  return CHIP_SURFACE_CLASS[color]
}

// The same surfaces one step deeper, for a tinted row's hover: the row stays
// its state's colour under the pointer instead of going grey.
const CHIP_SURFACE_HOVER_CLASS: Record<ChipColor, string> = {
  grey: 'hover:bg-gray-200 dark:hover:bg-gray-800',
  blue: 'hover:bg-blue-200/70 dark:hover:bg-blue-950/90',
  yellow: 'hover:bg-amber-200/70 dark:hover:bg-yellow-500/30',
  green: 'hover:bg-emerald-200/70 dark:hover:bg-emerald-950/90',
  red: 'hover:bg-rose-200/70 dark:hover:bg-rose-950/90',
}

export function chipSurfaceHoverClass(color: ChipColor): string {
  return CHIP_SURFACE_HOVER_CLASS[color]
}

// The state's colour at full strength, as a `before:` background: the surface
// draws it as a stripe on its edge (the geometry is the surface's), so the
// state reads at a glance even under a selection tint.
const CHIP_STRIPE_CLASS: Record<ChipColor, string> = {
  grey: 'before:bg-gray-400 dark:before:bg-gray-500',
  blue: 'before:bg-blue-500 dark:before:bg-blue-400',
  yellow: 'before:bg-amber-500 dark:before:bg-yellow-400',
  green: 'before:bg-emerald-500 dark:before:bg-emerald-400',
  red: 'before:bg-rose-500 dark:before:bg-rose-400',
}

export function chipStripeClass(color: ChipColor): string {
  return CHIP_STRIPE_CLASS[color]
}

// The stripe's shades as a plain background, for a small filled shape that
// carries a state on its own — a QA severity dot. Kept a literal map beside
// the stripe one because Tailwind only generates classes it finds spelled out.
const CHIP_DOT_CLASS: Record<ChipColor, string> = {
  grey: 'bg-gray-400 dark:bg-gray-500',
  blue: 'bg-blue-500 dark:bg-blue-400',
  yellow: 'bg-amber-500 dark:bg-yellow-400',
  green: 'bg-emerald-500 dark:bg-emerald-400',
  red: 'bg-rose-500 dark:bg-rose-400',
}

export function chipDotClass(color: ChipColor): string {
  return CHIP_DOT_CLASS[color]
}

export interface ChipProps
  // `color` is omitted because HTMLAttributes still carries the deprecated
  // presentational `color` attribute, typed as plain string — it would widen
  // the prop and let any value through.
  extends Omit<React.ComponentProps<'span'>, 'color'>,
    VariantProps<typeof chipVariants> {
  /**
   * Leading icon. lucide icons are sized by the root's `[&_svg]:size-3` and
   * need no className. A non-SVG icon (ProviderIcon renders a monogram span)
   * sizes itself — pass `className="size-3.5"` there.
   */
  icon?: React.ReactNode
}

// A real inline-flex box rather than `display: contents`: assistive tech has
// historically dropped `contents` elements from the accessibility tree, which
// would also drop the aria-hidden.
function ChipIcon({ icon }: { icon: React.ReactNode }) {
  if (icon == null) return null
  return (
    <span aria-hidden="true" className="inline-flex shrink-0 items-center">
      {icon}
    </span>
  )
}

export function Chip({ className, color, size, icon, children, ...props }: ChipProps) {
  return (
    <span
      data-slot="chip"
      className={cn(chipVariants({ color, size }), className)}
      {...props}
    >
      <ChipIcon icon={icon} />
      {children}
    </span>
  )
}

export interface ChipButtonProps
  extends Omit<React.ComponentProps<'button'>, 'color'>,
    VariantProps<typeof chipVariants> {
  icon?: React.ReactNode
  /** Makes it a toggle: `aria-pressed`, and a thicker ring in the text colour while on. */
  pressed?: boolean
}

/**
 * A chip that is also the click target — a legend entry that filters, a
 * segmented choice. Same colors and sizes as `Chip`, plus a pointer, a hover
 * fade and the shared focus ring. Never nest a `Chip` inside a `<button>`.
 */
export function ChipButton({ className, color, size, icon, pressed, children, type = 'button', ...props }: ChipButtonProps) {
  return (
    // eslint-disable-next-line no-restricted-syntax -- the chip's own click-target form, not an action button
    <button
      type={type}
      data-slot="chip"
      aria-pressed={pressed}
      className={cn(
        chipVariants({ color, size }),
        'cursor-pointer transition-opacity hover:opacity-80',
        pressed && 'ring-2 ring-current dark:ring-current',
        controlFocusVisibleClasses,
        className,
      )}
      {...props}
    >
      <ChipIcon icon={icon} />
      {children}
    </button>
  )
}

export { chipVariants }
