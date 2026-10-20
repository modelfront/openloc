import { cn } from '@/lib/utils'

// DotSeparator — the inline middot (·, U+00B7) that separates adjacent metadata
// bits in list/meta rows, e.g. "31 segments · 145 words" or
// "en → es · Acme · 69 entries". One glyph + spacing in one place so every row
// reads identically. Color-neutral (inherits the parent's text color) and
// aria-hidden since it's purely decorative.
export function DotSeparator({ className }: { className?: string }) {
  return (
    <span aria-hidden className={cn('mx-2 text-base leading-none', className)}>
      ·
    </span>
  )
}
