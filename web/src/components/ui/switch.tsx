import { Switch as SwitchPrimitive } from '@base-ui/react/switch'

import { cn } from '@/lib/utils'

/**
 * A binary on/off control that commits immediately — no form, no save button.
 *
 * Use it where the change IS the action (enabling an MT engine). Where the
 * value is one field among several that get saved together, use
 * `CheckboxField` instead: a switch that only takes effect on submit reads as
 * already-applied and is how people end up believing they turned something off.
 */
export function Switch({
  checked,
  onCheckedChange,
  disabled,
  className,
  'aria-label': ariaLabel,
  'data-testid': testId,
}: {
  checked: boolean
  onCheckedChange: (checked: boolean) => void
  disabled?: boolean
  className?: string
  /** Required in practice: the control carries no visible text of its own. */
  'aria-label': string
  'data-testid'?: string
}) {
  return (
    <SwitchPrimitive.Root
      checked={checked}
      onCheckedChange={onCheckedChange}
      disabled={disabled}
      aria-label={ariaLabel}
      data-testid={testId}
      className={cn(
        'relative inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full',
        'border border-transparent transition-colors',
        // Both dark variants of the checked state are spelled out: with only
        // `data-[checked]:bg-primary`, Tailwind emits `dark:bg-gray-700` later
        // in the sheet and an ON switch renders grey in dark mode.
        'bg-gray-300 dark:bg-gray-700',
        'data-[checked]:bg-primary dark:data-[checked]:bg-primary',
        'outline-hidden focus-visible:ring-2 focus-visible:ring-ring/60 focus-visible:ring-offset-2 focus-visible:ring-offset-background',
        'disabled:cursor-not-allowed disabled:opacity-50',
        className,
      )}
    >
      <SwitchPrimitive.Thumb
        className={cn(
          'block size-4 rounded-full bg-white shadow-sm transition-transform',
          'translate-x-0.5 data-[checked]:translate-x-4',
        )}
      />
    </SwitchPrimitive.Root>
  )
}
