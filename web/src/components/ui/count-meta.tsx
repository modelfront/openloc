import { cn } from '@/lib/utils'
import { DotSeparator } from './dot-separator'

// CountMeta — standardized "N units" metadata display (words / segments / chars…).
//
// Always thousands-separated (toLocaleString) + tabular-nums so counts line up
// and read the same everywhere, and joins multiple units with a middot.
// Auto-pluralizes each noun.
export interface CountItem {
  count: number
  /** Singular noun, e.g. 'word'. Pluralized as `${noun}s` unless `plural` is given. */
  noun: string
  plural?: string
}

export function CountMeta({ items, className }: { items: CountItem[]; className?: string }) {
  return (
    <span className={cn('tabular-nums', className)}>
      {items.map((it, i) => {
        const label = it.count === 1 ? it.noun : (it.plural ?? `${it.noun}s`)
        return (
          <span key={it.noun}>
            {i > 0 && <DotSeparator />}
            {it.count.toLocaleString()} {label}
          </span>
        )
      })}
    </span>
  )
}
