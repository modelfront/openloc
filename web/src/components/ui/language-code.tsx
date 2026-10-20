import type { HTMLAttributes } from 'react'
import { cn } from '@/lib/utils'
import { OverflowChip } from './chip-presets'

type LanguageCodeProps = HTMLAttributes<HTMLSpanElement> & {
  code: string
}

/** Consistent, background-free treatment for language codes across the app. */
export function LanguageCode({ code, className, ...props }: LanguageCodeProps) {
  return <span className={cn('font-mono text-xs uppercase', className)} {...props}>{code}</span>
}

type LanguagePairProps = HTMLAttributes<HTMLSpanElement> & {
  source: string
  targets: string | string[]
  /**
   * Cap on visible targets; the rest collapse into a `+N` chip that names them
   * on hover. Omit to show every target. A row that lists many locales (a TM's
   * coverage) sets this so one wide TM cannot push the rest of the row out.
   */
  max?: number
  /** Between the source and the targets; `|` where the languages are a set
   *  rather than a direction (a term base). */
  separator?: string
}

export function LanguagePair({ source, targets, max, separator = '→', className, ...props }: LanguagePairProps) {
  const targetCodes = Array.isArray(targets) ? targets : [targets]
  const shown = max != null ? targetCodes.slice(0, max) : targetCodes
  const hidden = targetCodes.length - shown.length

  return (
    <span className={cn('inline-flex items-baseline gap-1 font-mono text-xs uppercase', className)} {...props}>
      {shown.length > 0 ? `${source} ${separator} ${shown.join(', ')}` : source}
      {hidden > 0 && <OverflowChip count={hidden} title={targetCodes.slice(shown.length).join(', ')} />}
    </span>
  )
}
