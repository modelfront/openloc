import { useState } from 'react'
import { Puzzle } from 'lucide-react'
import { cn } from '@/lib/utils'
import { providerMark } from './provider-marks'
import { isExtensionProvider } from './vendor-catalog'

// Small brand badge for a provider: its own mark, knocked out white on its own
// colour, so a tile is recognisable before the label is read.
//
// The badge SHAPE is the constant, not the artwork. A provider with no
// published mark we can use — the mock test engine, which is not a vendor —
// falls back to a monogram in the same square, and an installed extension to a
// generic puzzle piece until its own icon loads, so a grid mixing them still reads as one row of badges
// rather than a ransom note. Adding a mark to `provider-marks.ts` upgrades a
// provider with no change here.
const MONOGRAM: Record<string, { mark: string; className: string }> = {
  // Grey says "not a real vendor"; K keeps it apart from a real provider's M.
  mock: { mark: 'K', className: 'bg-gray-500 text-white' },
}

const BADGE = 'inline-flex size-5 shrink-0 items-center justify-center rounded text-[11px] font-semibold leading-none'

export function ProviderIcon({ provider, className }: { provider: string; className?: string }) {
  const mark = providerMark(provider)
  if (mark) {
    return (
      <span
        aria-hidden="true"
        // A hex mark is white-on-brand; a `badgeClass` mark carries both its own
        // background and the glyph colour, which is how OpenAI flips with the theme.
        className={cn(BADGE, mark.badgeClass ?? 'text-white', className)}
        style={mark.hex ? { backgroundColor: mark.hex } : undefined}
      >
        {/* 60% of the badge: brand marks are drawn edge-to-edge in their
            viewBox, so at full size they crowd the rounded corners. */}
        <svg viewBox="0 0 24 24" className="size-[60%]" fill="currentColor">
          <path d={mark.path} />
        </svg>
      </span>
    )
  }

  if (isExtensionProvider(provider)) {
    return <ExtensionIcon key={provider} id={provider.slice('ext:'.length)} className={className} />
  }

  const cfg = MONOGRAM[provider] ?? {
    mark: (provider[0] ?? '?').toUpperCase(),
    className: 'bg-gray-400 text-white dark:bg-gray-600',
  }
  return (
    <span aria-hidden="true" className={cn(BADGE, cfg.className, className)}>
      {cfg.mark}
    </span>
  )
}

// The icon from the extension's zip, or the puzzle piece while it loads and
// when the extension has none (the icon URL answers 404).
function ExtensionIcon({ id, className }: { id: string; className?: string }) {
  const [loaded, setLoaded] = useState(false)
  return (
    <span
      aria-hidden="true"
      className={cn(BADGE, !loaded && 'bg-gray-500 text-white dark:bg-gray-600', className)}
      data-testid={`extension-icon-${id}`}
    >
      {!loaded && <Puzzle className="size-[60%]" />}
      <img
        src={`/api/ai/extensions/${encodeURIComponent(id)}/icon`}
        alt=""
        className={loaded ? 'size-full rounded object-contain' : 'hidden'}
        onLoad={() => setLoaded(true)}
        onError={() => setLoaded(false)}
      />
    </span>
  )
}
