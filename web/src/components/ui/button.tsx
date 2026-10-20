import type { ReactNode } from "react"
import { Button as ButtonPrimitive } from "@base-ui/react/button"
import { cva, type VariantProps } from "class-variance-authority"
import { Loader2Icon } from "lucide-react"

import { cn } from "@/lib/utils"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

const buttonBaseClass =
  "group/button inline-flex shrink-0 items-center justify-center rounded-lg border border-transparent bg-clip-padding text-sm font-medium whitespace-nowrap transition-all outline-none select-none cursor-pointer focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 active:translate-y-px disabled:pointer-events-none disabled:opacity-50 aria-disabled:cursor-not-allowed aria-disabled:opacity-50 aria-disabled:active:translate-y-0 aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 dark:aria-invalid:border-destructive/50 dark:aria-invalid:ring-destructive/40 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4"

const buttonColorVariants = {
  default: "bg-primary text-primary-foreground hover:bg-primary/80",
  outline:
    "border-border bg-background hover:bg-muted hover:text-foreground aria-expanded:bg-muted aria-expanded:text-foreground dark:border-input dark:bg-input/30 dark:hover:bg-input/50",
  ghost:
    "hover:bg-muted hover:text-foreground aria-expanded:bg-muted aria-expanded:text-foreground dark:hover:bg-muted/50",
  destructive:
    "border-destructive text-destructive hover:bg-destructive/10 focus-visible:border-destructive focus-visible:ring-destructive/20 dark:hover:bg-destructive/20 dark:focus-visible:ring-destructive/40",
  // Workflow tones. `success` completes a routine step and `final` the last
  // step of a workflow; the outline tones mark a
  // completing or a stopping action in a row of outline buttons.
  success: "bg-green-600 text-white hover:bg-green-700",
  final: "bg-violet-600 text-white hover:bg-violet-700",
  "success-outline":
    "border-green-300 bg-background text-green-700 hover:bg-green-50 dark:border-green-800 dark:text-green-400 dark:hover:bg-green-950",
  "warning-outline":
    "border-amber-300 bg-background text-amber-700 hover:bg-amber-50 dark:border-amber-800 dark:text-amber-400 dark:hover:bg-amber-950",
} as const

const buttonVariants = cva(buttonBaseClass, {
  variants: {
    variant: buttonColorVariants,
    size: {
      default:
        "h-8 gap-1.5 px-2.5 has-data-[icon=inline-end]:pr-2 has-data-[icon=inline-start]:pl-2",
      xs: "h-6 gap-1 rounded-[min(var(--radius-md),10px)] px-2 text-xs in-data-[slot=button-group]:rounded-lg has-data-[icon=inline-end]:pr-1.5 has-data-[icon=inline-start]:pl-1.5 [&_svg:not([class*='size-'])]:size-3",
      sm: "h-7 gap-1 rounded-[min(var(--radius-md),12px)] px-2.5 text-[0.8rem] in-data-[slot=button-group]:rounded-lg has-data-[icon=inline-end]:pr-1.5 has-data-[icon=inline-start]:pl-1.5 [&_svg:not([class*='size-'])]:size-3.5",
      // Square, icon-only sizes — no padding, width locked to height.
      icon: "size-8",
      // The compact square for action rows on cards and list items.
      "icon-sm": "size-7",
      "icon-xs":
        "size-6 rounded-[min(var(--radius-md),10px)] in-data-[slot=button-group]:rounded-lg [&_svg:not([class*='size-'])]:size-3",
    },
  },
  defaultVariants: {
    variant: "default",
    size: "default",
  },
})

// Fixed text/icon color tokens (text only — background stays owned by the variant).
// `color` sets the resting color, `hoverColor` the on-hover color; combine them for
// transitions like default→red (delete).
type ButtonColor = "default" | "muted" | "primary" | "red"

const BUTTON_TEXT_COLOR: Record<ButtonColor, string> = {
  default: "",
  muted: "text-gray-400 dark:text-gray-500",
  primary: "text-primary",
  red: "text-red-600 dark:text-red-400",
}

const BUTTON_HOVER_TEXT_COLOR: Record<ButtonColor, string> = {
  default: "hover:text-foreground",
  muted: "hover:text-gray-600 dark:hover:text-gray-300",
  primary: "hover:text-primary",
  red: "hover:text-red-600 dark:hover:text-red-400",
}

// `startIcon` / `endIcon` render a decorative icon beside the label; the `data-icon`
// marker tightens padding on that side (see `has-data-[icon=…]` in the size classes).
// `loading` shows a spinner and disables the button. For the square icon sizes the
// spinner replaces the icon (no room for both); for text sizes it sits before the label.
// `color` / `hoverColor` override the text/icon color from the fixed token set.
// `disabledReason` says why a disabled button is disabled, as a tooltip on hover
// and keyboard focus, shown at once: the reason is what the user came to read.
// The button stays focusable and hoverable (aria-disabled) while its click
// stays blocked, so the reason is reachable.
function Button({
  className,
  variant = "default",
  size = "default",
  loading = false,
  startIcon,
  endIcon,
  color,
  hoverColor,
  disabled,
  disabledReason,
  children,
  ...props
}: ButtonPrimitive.Props &
  VariantProps<typeof buttonVariants> & {
    loading?: boolean
    startIcon?: ReactNode
    endIcon?: ReactNode
    color?: ButtonColor
    hoverColor?: ButtonColor
    disabledReason?: string
  }) {
  const iconOnly = size === "icon" || size === "icon-sm" || size === "icon-xs"
  const explain = !!disabled && !loading && !!disabledReason
  const button = (
    <ButtonPrimitive
      data-slot="button"
      className={cn(
        buttonVariants({ variant, size }),
        color && BUTTON_TEXT_COLOR[color],
        hoverColor && BUTTON_HOVER_TEXT_COLOR[hoverColor],
        className,
      )}
      {...props}
      disabled={disabled || loading}
      focusableWhenDisabled={explain || props.focusableWhenDisabled}
      aria-busy={loading || undefined}
    >
      {loading ? (
        <Loader2Icon className="animate-spin" aria-hidden="true" data-icon="inline-start" />
      ) : startIcon ? (
        <span data-icon="inline-start" aria-hidden="true" className="inline-flex shrink-0">
          {startIcon}
        </span>
      ) : null}
      {iconOnly && loading ? null : children}
      {!loading && endIcon ? (
        <span data-icon="inline-end" aria-hidden="true" className="inline-flex shrink-0">
          {endIcon}
        </span>
      ) : null}
    </ButtonPrimitive>
  )
  if (!explain) return button
  return (
    <Tooltip>
      <TooltipTrigger delay={0} render={button} />
      <TooltipContent>{disabledReason}</TooltipContent>
    </Tooltip>
  )
}

export { Button, buttonVariants }
