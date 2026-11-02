import { useId, useState } from 'react'
import type { TMMatch } from '../api'
import type { PanelInsert } from '../lib/panel-insert'
import { tmPenaltyLineText, tmScoreBreakdown, type TMScoreBreakdown } from '../lib/tm-penalties'
import { tmEntryStamps, type TMEntryStamps } from '../lib/tm-entry-stamps'
import { SegmentText, historyKindColor } from './SegmentRow.shared'
import { LanguageCode } from '@/components/ui/language-code'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { chipSurfaceClass, chipSurfaceHoverClass, chipTextClass } from '@/components/ui/chip'
import { cn } from '@/lib/utils'

interface Props {
  match: TMMatch
  /** 0-based position; the badge and the insert shortcut show it 1-based. */
  index: number
  stale: boolean
  sourceText: string
  fullText: boolean
  onInsertMatch: (insert: PanelInsert) => void
  onMatchHover?: (sourceText: string | null) => void
  modKey: string
}

/** One TM hit in the Resources panel. A docked hit shows its text match struck
 *  out before the percent; hovering them, or focusing the card, lists each
 *  deduction. The last line says when the entry last changed and by whom;
 *  hovering it, or focusing the card, adds when it was created. */
export function EditorSidebarTMMatchCard({ match: m, index: i, stale, sourceText, fullText, onInsertMatch, onMatchHover, modKey }: Props) {
  const [focused, setFocused] = useState(false)
  const descriptionId = useId()
  const pct = m.percent
  // Same colour as the grid's Match chip: exact is green, fuzzy yellow.
  const color = historyKindColor('tm', pct)
  const breakdown = tmScoreBreakdown(m)
  const stamps = tmEntryStamps(m)
  const description = [
    ...(breakdown ? [`${breakdown.textPercent}% text match`, ...breakdown.lines.map(tmPenaltyLineText)] : []),
    `Created ${stamps.created}`,
  ].join('; ')

  return (
    // eslint-disable-next-line no-restricted-syntax -- whole-card target, not an action button
    <button
      type="button"
      data-testid="tm-match-card"
      className={cn(
        'block w-full text-left rounded border px-2 py-1.5',
        chipSurfaceClass(color),
        chipTextClass(color),
        stale ? 'cursor-default' : ['cursor-pointer hover:ring-1 hover:ring-ring', chipSurfaceHoverClass(color)],
      )}
      aria-describedby={descriptionId}
      // Insert the match itself (not its text) so the editor can parse
      // tags into chips and remap them onto the current source.
      onClick={() => { if (!stale) onInsertMatch({ kind: 'tm', match: m, insertToken: m.insert_token }) }}
      // Pointing at a hit re-targets the segment source's own diff to
      // it, so the pair being read always describes one comparison.
      // The card is a button so a keyboard user reaches the same
      // retargeting through focus.
      onMouseEnter={() => { if (!stale) onMatchHover?.(m.source_text) }}
      onMouseLeave={() => onMatchHover?.(null)}
      onFocus={() => { setFocused(true); if (!stale) onMatchHover?.(m.source_text) }}
      onBlur={() => { setFocused(false); onMatchHover?.(null) }}
      title={stale ? 'Loading matches for this segment…' : `${modKey}+${i + 1} to insert`}
    >
      <span className="flex items-center gap-1.5 mb-0.5 min-w-0">
        <span className="inline-flex shrink-0 items-center justify-center w-4 h-4 rounded-full bg-gray-200 dark:bg-gray-600 text-[9px] font-bold text-gray-600 dark:text-gray-300">{i + 1}</span>
        {breakdown && !stale
          ? <MatchScore pct={pct} breakdown={breakdown} open={focused} />
          : <span className="text-[10px] font-bold" data-testid="tm-match-percent">{pct}%</span>}
        <span className="text-[9px] text-gray-400 ml-auto shrink-0 truncate max-w-[80px]">{m.tm_name}</span>
      </span>
      <span id={descriptionId} hidden>{description}</span>
      <span className={`block text-[11px] text-gray-500 dark:text-gray-400 ${fullText ? 'whitespace-pre-wrap break-words' : 'truncate'}`}>
        {/* Stale rows describe another segment, so there is nothing
            meaningful to diff them against. */}
        <SegmentText text={m.source_text} richText={m.source_rich_text} diffAgainst={stale ? null : sourceText} />
      </span>
      <span className={`block text-[11px] font-medium ${fullText ? 'whitespace-pre-wrap break-words' : 'truncate'}`}>
        <SegmentText text={m.target_text} richText={m.target_rich_text} />
      </span>
      <MatchStamps stamps={stamps} open={focused} />
    </button>
  )
}

/** The text match struck out, then the percent; `open` holds the breakdown
 *  open while the card has keyboard focus, as hovering does for the mouse.
 *  The trigger is a plain span: the card around it is the focus stop. */
function MatchScore({ pct, breakdown, open }: { pct: number; breakdown: TMScoreBreakdown; open: boolean }) {
  const [hovered, setHovered] = useState(false)
  return (
    <TooltipProvider delay={200}>
      <Tooltip open={hovered || open} onOpenChange={setHovered}>
        <TooltipTrigger render={<span className="inline-flex min-w-0 items-baseline gap-1 cursor-help" data-testid="tm-match-score" />}>
          <s className="shrink-0 text-[10px] text-gray-500 dark:text-gray-400" data-testid="tm-match-text-percent">{breakdown.textPercent}%</s>
          <span className="shrink-0 text-[10px] font-bold" data-testid="tm-match-percent">{pct}%</span>
        </TooltipTrigger>
        <TooltipContent className="max-w-72 flex-col items-stretch gap-0.5 text-xs leading-relaxed" data-testid="tm-match-penalty-breakdown">
          <span>{breakdown.textPercent}% text match</span>
          {breakdown.lines.map((line, n) => (
            <span key={n} className="flex gap-1.5">
              <span className="shrink-0 tabular-nums">−{line.percent}%</span>
              <span>
                {line.label}
                {line.tmLanguage && line.projectLanguage && (
                  <> (TM: <LanguageCode code={line.tmLanguage} />, project: <LanguageCode code={line.projectLanguage} />)</>
                )}
              </span>
            </span>
          ))}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}

/** The entry's last change, with its creation and last change in a tooltip
 *  below; `open` holds it open while the card has keyboard focus. */
function MatchStamps({ stamps, open }: { stamps: TMEntryStamps; open: boolean }) {
  const [hovered, setHovered] = useState(false)
  return (
    <TooltipProvider delay={200}>
      <Tooltip open={hovered || open} onOpenChange={setHovered}>
        <TooltipTrigger render={<span className="mt-0.5 block truncate text-[9px] text-gray-500 dark:text-gray-400 cursor-help" data-testid="tm-match-stamp" />}>
          {stamps.changed}
        </TooltipTrigger>
        <TooltipContent side="bottom" className="max-w-72 flex-col items-stretch gap-0.5 text-xs leading-relaxed" data-testid="tm-match-stamps">
          <span>Created {stamps.created}</span>
          <span>Changed {stamps.changed}</span>
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}
