import { useState } from 'react'
import { Maximize2, Minimize2 } from 'lucide-react'
import type { TMMatch } from '../api'
import type { PanelInsert } from '../lib/panel-insert'
import { Button } from '@/components/ui/button'
import { useStoredState } from '@/hooks/useStoredState'
import { EditorSidebarSection } from './EditorSidebarSection'
import { EditorSidebarTMMatchCard } from './EditorSidebarTMMatchCard'

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
 * lib/tm-diff.ts — and a docked hit explains its penalties — see
 * lib/tm-penalties.ts.
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
      {matches.map((m, i) => (
        <EditorSidebarTMMatchCard
          key={`${m.tm_id}-${m.source_text}-${i}`}
          match={m}
          index={i}
          stale={stale}
          sourceText={sourceText}
          fullText={fullText}
          onInsertMatch={onInsertMatch}
          onMatchHover={onMatchHover}
          modKey={modKey}
        />
      ))}
    </EditorSidebarSection>
  )
}
