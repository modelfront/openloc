import type { KeyboardEvent, MouseEvent } from 'react'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

type Props = {
  /** Current input value (controlled). */
  value: string
  /** Called with the next value on each keystroke. */
  onChange: (next: string) => void
  /** Called on Enter or blur to commit the rename. */
  onSubmit: () => void
  /** Called on Escape to abandon the rename. */
  onCancel: () => void
  /** Extra classes merged onto the input. */
  className?: string
  /**
   * Max input length. Defaults to 255 to match the typical `varchar(255)`
   * column width on the entities this field renames (TM, terminology, template,
   * project, QA profile names). Without this, a user can type an arbitrarily
   * long name that produces a confusing 500 on save.
   */
  maxLength?: number
  /**
   * Accessible label. Defaults to "Rename" so screen readers don't announce
   * an unlabeled text field. Callers can override with the entity name
   * (e.g. `aria-label="Rename TM"`) for richer context.
   */
  'aria-label'?: string
  /**
   * Placeholder shown when the input is empty. Used by inline-edit cases like
   * `TerminologyBrowser`'s editable description (where the field is genuinely
   * empty until the user types something), not by pure rename cases (which
   * always start with the existing name).
   */
  placeholder?: string
}

/**
 * Compact inline rename input used by list pages.
 *
 * Key behaviors:
 * - Enter → `onSubmit`, Escape → `onCancel`, blur → `onSubmit`
 * - `autoFocus` so the user can type immediately on mount
 * - Calls BOTH `preventDefault()` and `stopPropagation()` on click so clicks
 *   inside the field don't navigate a parent `<Link>` or trigger a parent card
 *   click handler
 */
export function InlineRenameField({
  value,
  onChange,
  onSubmit,
  onCancel,
  className,
  maxLength = 255,
  'aria-label': ariaLabel = 'Rename',
  placeholder,
}: Props) {
  const handleKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      onSubmit()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      onCancel()
    }
  }

  const swallowClick = (e: MouseEvent<HTMLElement>) => {
    e.preventDefault()
    e.stopPropagation()
  }

  // The swallow wraps the whole field, not just the `<input>`: `Input` draws a
  // box that owns the padding and the clear X, and a click on either would
  // otherwise reach the row underneath — QAProfiles renames inside a row whose
  // own onClick collapses it.
  return (
    <span className="inline-flex min-w-0" onClick={swallowClick}>
      <Input
        value={value}
        onChange={e => onChange(e.target.value)}
        onBlur={onSubmit}
        onKeyDown={handleKeyDown}
        className={cn('font-medium text-gray-900 dark:text-gray-100 h-7 w-auto', className)}
        autoFocus
        maxLength={maxLength}
        aria-label={ariaLabel}
        placeholder={placeholder}
      />
    </span>
  )
}
