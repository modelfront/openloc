import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

/**
 * `caps` is the small upper-case micro-label used inside dense panels — the
 * spell-fix block, where four of them stack in a popover and the normal label
 * would out-shout the text it describes.
 */
const LABEL_VARIANTS = {
  default: 'text-xs text-gray-500 dark:text-gray-400 mb-1',
  caps: 'text-[11px] uppercase tracking-wide text-gray-400 dark:text-gray-500 mb-0.5',
} as const

interface FormFieldProps {
  label: string
  error?: string
  required?: boolean
  children: ReactNode
  className?: string
  labelVariant?: keyof typeof LABEL_VARIANTS
  /** A small action at the right end of the label row, e.g. a `TextLink`
   *  "Select all". */
  action?: ReactNode
}

/**
 * Form field wrapper with label and inline error message.
 * Shows red border indicator + error text when error is present.
 */
export function FormField({ label, error, required, children, className = '', labelVariant = 'default', action }: FormFieldProps) {
  const labelEl = (
    <label className={cn('block', LABEL_VARIANTS[labelVariant])}>
      {label}
      {required && <span className="text-red-500 ml-0.5">*</span>}
    </label>
  )
  return (
    <div className={className}>
      {action ? (
        <div className="flex items-baseline justify-between gap-2">
          {labelEl}
          <span className="mb-1 text-xs">{action}</span>
        </div>
      ) : labelEl}
      <div className={error ? '[&>*]:border-red-400 [&>*]:dark:border-red-600' : ''}>
        {children}
      </div>
      {error && (
        <p className="text-xs text-red-500 dark:text-red-400 mt-1">{error}</p>
      )}
    </div>
  )
}
