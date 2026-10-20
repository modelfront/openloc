import { useState } from 'react'
import { Input } from '@/components/ui/input'

interface Props {
  /** The stored value; null / undefined reads as empty. */
  value: string | null | undefined
  /** Called on blur with the trimmed draft, only when it differs from `value`. */
  onCommit: (value: string) => void
  placeholder?: string
  /** No default: a cap here is a UI constraint the backend has to enforce too. */
  maxLength?: number
  disabled?: boolean
  /** `seamless` drops the box, for a description that edits in place. */
  variant?: 'default' | 'seamless'
  /** Styles the box — width, and the underline a seamless field draws itself. */
  className?: string
  'aria-label'?: string
  'data-testid'?: string
}

/**
 * Free-text field for a saved entity: edits are buffered in a draft and
 * committed on blur, so every keystroke does not become a PATCH. Enter commits
 * too, by blurring. Used for the domain field on the project page, the text
 * fields on the template page and the TM profile description.
 */
export function BlurCommitInput({
  value,
  onCommit,
  placeholder,
  maxLength,
  disabled,
  variant,
  className,
  'aria-label': ariaLabel,
  'data-testid': testId,
}: Props) {
  const [draft, setDraft] = useState<string | null>(null)
  return (
    <Input
      type="text"
      value={draft ?? value ?? ''}
      onChange={e => setDraft(e.target.value)}
      onBlur={() => {
        if (draft === null) return
        const trimmed = draft.trim()
        setDraft(null)
        if (trimmed !== (value ?? '')) onCommit(trimmed)
      }}
      onKeyDown={e => { if (e.key === 'Enter') (e.target as HTMLInputElement).blur() }}
      maxLength={maxLength}
      placeholder={placeholder}
      disabled={disabled}
      variant={variant}
      className={className}
      aria-label={ariaLabel}
      data-testid={testId}
    />
  )
}
