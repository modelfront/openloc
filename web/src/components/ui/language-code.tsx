import { Fragment, type HTMLAttributes } from 'react'
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
   * Cap on visible targets; the rest collapse into a `+N` chip whose tooltip
   * shows the full list. Omit to show every target. A row that lists many locales (a TM's
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
  // The tooltip is this pair again, uncapped: the full list, in the same style.
  const chip = hidden > 0 && (
    <OverflowChip
      count={hidden}
      tooltip={<LanguagePair source={source} targets={targetCodes} separator={separator} />}
      className="ml-1"
    />
  )

  // Inline text that wraps only between codes: each code is one nowrap unit
  // (a browser would otherwise break "FR-CA" after its hyphen), and the +N
  // chip rides on the last one.
  return (
    <span className={cn('font-mono text-xs uppercase', className)} {...props}>
      <span className="whitespace-nowrap">
        {shown.length > 0 ? `${source} ${separator}` : source}
        {shown.length === 0 && chip}
      </span>
      {shown.map((code, i) => (
        <Fragment key={i}>
          {' '}
          <span className="whitespace-nowrap">{code}{i < shown.length - 1 ? ',' : chip}</span>
        </Fragment>
      ))}
    </span>
  )
}
