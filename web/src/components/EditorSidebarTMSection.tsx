import { useState } from 'react'
import { Maximize2, Minimize2 } from 'lucide-react'
import type { TMMatch } from '../api'
import type { PanelInsert } from '../lib/panel-insert'
import { SegmentText, historyKindColor } from './SegmentRow.shared'
import { Button } from '@/components/ui/button'
import { useStoredState } from '@/hooks/useStoredState'
import { EditorSidebarSection } from './EditorSidebarSection'
import { chipSurfaceClass, chipSurfaceHoverClass, chipTextClass } from '@/components/ui/chip'
import { cn } from '@/lib/utils'

interface Props {
  matches: TMMatch[]
  fetching: boolean
  /** True while the listed hits belong to a different segment than the active
   *  one. Such rows are dimmed and inert — inserting one would write the
   *  previous segment's translation into this cell. */
  stale: boolean
  /** Raw (undebounced) source: '' means no segment is active. */
  sourceText: string
  onInsertMatch: (insert: PanelInsert) => void
  /** Reports the hit the user is pointing at (null on leave), so the editor can
   *  re-diff the segment's source against it instead of the top hit. */
  onMatchHover?: (sourceText: string | null) => void
  /** "⌘" or "Ctrl" — for the per-hit insert shortcut hint. */
  modKey: string
}

/**
 * The TM matches section of the Resources sidebar. Hits are truncated to one
 * line each so the panel stays scannable; the expand toggle in the header
 * unwraps them, persisted like the editor's other display toggles.
 *
 * Each hit's source line is diffed against the segment's source — see
 * lib/tm-diff.ts.
 */
export function EditorSidebarTMSection({ matches, fetching, stale, sourceText, onInsertMatch, onMatchHover, modKey }: Props) {
  const [open, setOpen] = useState(true)
  const [fullText, setFullText] = useStoredState('cat-tm-full-text', stored => stored === true)

  return (
    <EditorSidebarSection
      data-testid="tm-matches-section"
      title="TM matches"
      open={open}
      onToggle={() => setOpen(v => !v)}
      meta={matches.length}
      action={open && matches.length > 0 && (
        <Button
          variant="ghost"
          size="icon-xs"
          onClick={() => setFullText(v => !v)}
          data-testid="tm-full-text-toggle"
          aria-pressed={fullText}
          className={`mr-1 ${fullText ? 'text-blue-600 dark:text-blue-400' : 'text-gray-400'}`}
          title={fullText ? 'Collapse matches to one line' : 'Show full match text'}
        >
          {fullText ? <Minimize2 className="w-3 h-3" /> : <Maximize2 className="w-3 h-3" />}
        </Button>
      )}
      // Dimmed whenever the rows aren't insertable, so the panel never shows
      // full-strength cards that silently ignore a click.
      bodyClassName={`space-y-1 transition-opacity duration-200 ${fetching || stale ? 'opacity-50' : 'opacity-100'}`}
    >
      {matches.length === 0 && (
        <p className="text-[10px] text-gray-400 px-1">{sourceText ? 'No matches' : 'Select a segment'}</p>
      )}
      {matches.map((m, i) => {
        const pct = m.percent
        // Same colour as the grid's Match chip: exact is green, fuzzy yellow.
        const color = historyKindColor('tm', pct)
        return (
          // eslint-disable-next-line no-restricted-syntax -- whole-card target, not an action button
          <button
            type="button"
            key={`${m.tm_id}-${m.source_text}-${i}`}
            data-testid="tm-match-card"
            className={cn(
              'block w-full text-left rounded border px-2 py-1.5',
              chipSurfaceClass(color),
              chipTextClass(color),
              stale ? 'cursor-default' : ['cursor-pointer hover:ring-1 hover:ring-ring', chipSurfaceHoverClass(color)],
            )}
            // Insert the match itself (not its text) so the editor can parse
            // tags into chips and remap them onto the current source.
            onClick={() => { if (!stale) onInsertMatch({ kind: 'tm', match: m, insertToken: m.insert_token }) }}
            // Pointing at a hit re-targets the segment source's own diff to
            // it, so the pair being read always describes one comparison.
            // The card is a button so a keyboard user reaches the same
            // retargeting through focus.
            onMouseEnter={() => { if (!stale) onMatchHover?.(m.source_text) }}
            onMouseLeave={() => onMatchHover?.(null)}
            onFocus={() => { if (!stale) onMatchHover?.(m.source_text) }}
            onBlur={() => onMatchHover?.(null)}
            title={stale ? 'Loading matches for this segment…' : `${modKey}+${i + 1} to insert`}
          >
            <span className="flex items-center gap-1.5 mb-0.5">
              <span className="inline-flex items-center justify-center w-4 h-4 rounded-full bg-gray-200 dark:bg-gray-600 text-[9px] font-bold text-gray-600 dark:text-gray-300">{i + 1}</span>
              <span className="text-[10px] font-bold">{pct}%</span>
              <span className="text-[9px] text-gray-400 ml-auto truncate max-w-[80px]">{m.tm_name}</span>
            </span>
            <span className={`block text-[11px] text-gray-500 dark:text-gray-400 ${fullText ? 'whitespace-pre-wrap break-words' : 'truncate'}`}>
              {/* Stale rows describe another segment, so there is nothing
                  meaningful to diff them against. */}
              <SegmentText text={m.source_text} richText={m.source_rich_text} diffAgainst={stale ? null : sourceText} />
            </span>
            <span className={`block text-[11px] font-medium ${fullText ? 'whitespace-pre-wrap break-words' : 'truncate'}`}>
              <SegmentText text={m.target_text} richText={m.target_rich_text} />
            </span>
          </button>
        )
      })}
    </EditorSidebarSection>
  )
}
