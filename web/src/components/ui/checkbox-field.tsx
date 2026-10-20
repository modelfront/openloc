import { type ReactNode } from 'react'
import { cn } from '@/lib/utils'

/**
 * A labeled checkbox row: a native checkbox + a title (and optional description),
 * the whole strip clickable. Hover feedback is a subtle full-row background tint
 * (`hover:bg-accent`) rather than recoloring the text/checkbox — the accent color
 * stays reserved for the checked state. The checkbox is vertically centered
 * against the text block.
 *
 * `size="compact"` is the flag beside a search or find field ("Aa", "Exact"):
 * small grey text on one line, no row padding or tint, no description.
 */
export function CheckboxField({
  checked, onChange, label, description, disabled = false, size = 'default', className, id, 'data-testid': testId,
}: {
  checked: boolean
  onChange: (checked: boolean) => void
  label: ReactNode
  description?: ReactNode
  disabled?: boolean
  size?: 'default' | 'compact'
  className?: string
  id?: string
  'data-testid'?: string
}) {
  const input = (
    <input
      type="checkbox"
      checked={checked}
      onChange={e => onChange(e.target.checked)}
      disabled={disabled}
      id={id}
      data-testid={testId}
      className={size === 'compact'
        ? 'accent-primary'
        : 'h-4 w-4 shrink-0 rounded border-gray-300 dark:border-gray-600 focus:ring-ring accent-primary'}
    />
  )
  if (size === 'compact') {
    return (
      <label
        className={cn(
          'flex items-center gap-1 text-xs text-gray-500 dark:text-gray-400 whitespace-nowrap',
          disabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer',
          className,
        )}
      >
        {input}
        {label}
      </label>
    )
  }
  return (
    <label
      className={cn(
        'flex items-center gap-3 rounded-md -mx-2 px-2 py-1.5 transition-colors',
        disabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer hover:bg-accent',
        className,
      )}
    >
      {input}
      <div className="min-w-0">
        <span className="text-sm font-medium text-gray-900 dark:text-gray-100">{label}</span>
        {description && <p className="text-xs text-muted-foreground">{description}</p>}
      </div>
    </label>
  )
}
