import { Fragment, type ComponentType, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

export interface ChoiceToggleOption<V extends string> {
  value: V
  label: ReactNode
  /** Optional leading icon (a lucide component). */
  icon?: ComponentType<{ className?: string }>
  /** A few quiet words after the label, say what the option means. */
  caption?: string
  disabled?: boolean
}

interface Props<V extends string> {
  /** The checked option; `null` means nothing is chosen yet. */
  value: V | null
  onChange: (value: V) => void
  options: ChoiceToggleOption<V>[]
  /** Accessible name for the group. */
  'aria-label': string
  /** `sm` fits a row of small controls, such as the search options. */
  size?: 'default' | 'sm'
  /** `primary` fills the checked option with the primary colour — for a
   *  choice that heads a card and must read from across the page. */
  tone?: 'default' | 'primary'
  className?: string
  'data-testid'?: string
}

/**
 * A segmented single-choice control: a few large options in one pill, one of
 * them checked. Radio semantics (`role="radiogroup"` / `role="radio"`), so it
 * answers "which of these?" — unlike `Tabs`, which switches visible panels.
 * Each option gets `data-testid="<group testid>-<value>"`.
 */
export function ChoiceToggle<V extends string>({ value, onChange, options, size = 'default', tone = 'default', className, 'aria-label': ariaLabel, 'data-testid': testId }: Props<V>) {
  const sm = size === 'sm'
  const primary = tone === 'primary'
  return (
    <div
      role="radiogroup"
      aria-label={ariaLabel}
      data-testid={testId}
      className={cn('inline-flex max-md:max-w-full items-center gap-0.5 max-md:overflow-x-auto bg-muted text-muted-foreground', sm ? 'rounded-md p-0.5' : 'rounded-lg p-1', className)}
    >
      {options.map((opt, i) => {
        const checked = opt.value === value
        const Icon = opt.icon
        // Hairline between options; hidden next to the checked one, whose
        // raised background already separates it.
        const dividerHidden = checked || options[i - 1]?.value === value
        return (
          <Fragment key={opt.value}>
          {i > 0 && (
            <span
              aria-hidden="true"
              className={cn('w-px shrink-0 bg-foreground/15 transition-opacity', sm ? 'h-3.5' : 'h-5', dividerHidden && 'opacity-0')}
            />
          )}
          {/* eslint-disable-next-line no-restricted-syntax -- a radio in a segmented toggle, not an action button */}
          <button
            type="button"
            role="radio"
            aria-checked={checked}
            data-state={checked ? 'checked' : 'unchecked'}
            data-testid={testId ? `${testId}-${opt.value}` : undefined}
            disabled={opt.disabled}
            onClick={() => onChange(opt.value)}
            className={cn(
              'inline-flex items-center justify-center border border-transparent font-medium whitespace-nowrap transition-all',
              sm ? 'h-6 gap-1 rounded-sm px-2 text-xs' : 'h-9 gap-2 rounded-md px-4 text-sm',
              'focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none',
              'disabled:pointer-events-none disabled:opacity-50',
              checked
                ? primary
                  ? 'bg-primary text-primary-foreground shadow-sm'
                  : 'bg-background text-foreground shadow-sm dark:border-input dark:bg-input/30'
                : 'text-foreground/60 hover:text-foreground',
            )}
          >
            {Icon && <Icon className={cn(sm ? 'size-3.5' : 'size-4', 'shrink-0')} />}
            {opt.label}
            {/* A space so the accessible name reads "Workflow Human and AI
                steps", not "WorkflowHuman…"; a flex item, it takes no room. */}
            {opt.caption && ' '}
            {opt.caption && (
              <span className={cn('font-normal', sm ? 'text-[11px]' : 'text-xs', checked && primary ? 'text-primary-foreground/80' : 'text-muted-foreground')}>
                {opt.caption}
              </span>
            )}
          </button>
          </Fragment>
        )
      })}
    </div>
  )
}
