import { useState } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { getEditorFuzzy } from '../api/editor'
import type { PanelInsert } from '../lib/panel-insert'
import type { EditorId } from '../lib/editor-scope'
import { queryKeys } from '../lib/query-keys'
import { EditorSidebarSection } from './EditorSidebarSection'
import { MatchCardButton, MatchCardTexts } from './EditorSidebarTMMatchCard'

interface Props {
  editorId: EditorId
  /** The open segment; null when none is open. */
  segmentId: number | null
  /** The open segment's source, for the difference marks. */
  sourceText: string
  /** File names by file id, to say where a match comes from; a single-file
   *  editor passes none. */
  fileNames?: ReadonlyMap<number, string>
  onInsertMatch: (insert: PanelInsert) => void
}

/**
 * Internal matches: confirmed segments of the files the editor has open whose
 * source is like the open segment's, scored like TM matches. A click inserts
 * the match's translation into the segment, which saves as the person's own
 * edit; nothing is written to a TM.
 */
export function EditorSidebarFuzzySection({ editorId, segmentId, sourceText, fileNames, onInsertMatch }: Props) {
  const [open, setOpen] = useState(true)
  const { data: matches = [], isFetching, isPlaceholderData } = useQuery({
    queryKey: queryKeys.job.editorFuzzy(editorId, segmentId ?? 0),
    queryFn: () => getEditorFuzzy(editorId, segmentId!),
    enabled: open && segmentId != null && sourceText.length > 2,
    placeholderData: keepPreviousData,
    staleTime: 30_000,
  })
  // The previous segment's matches stay on screen while this one loads; they
  // describe another segment, so they are dimmed and inert.
  const stale = isPlaceholderData

  return (
    <EditorSidebarSection
      data-testid="internal-matches-section"
      title="Internal matches"
      open={open}
      onToggle={() => setOpen(v => !v)}
      meta={matches.length}
      bodyClassName={`space-y-1 transition-opacity duration-200 ${isFetching || stale ? 'opacity-50' : 'opacity-100'}`}
    >
      {matches.length === 0 && (
        <p className="text-[10px] text-gray-400 px-1">{segmentId != null ? 'No matches in the open files' : 'Select a segment'}</p>
      )}
      {matches.map(m => {
        const fileName = fileNames?.get(m.file_id)
        return (
          <MatchCardButton
            key={m.segment_id}
            percent={m.percent}
            stale={stale}
            data-testid="internal-match-card"
            onClick={() => {
              if (!stale) onInsertMatch({ kind: 'tm', match: { target_text: m.target_text, target_rich_text: m.target_rich_text } })
            }}
            title={stale ? 'Loading matches for this segment…' : 'Click to insert'}
          >
            <span className="flex items-center gap-1.5 mb-0.5 min-w-0">
              <span className="text-[10px] font-bold" data-testid="internal-match-percent">{m.percent}%</span>
              {fileName && <span className="text-[9px] text-gray-400 ml-auto shrink-0 truncate max-w-[120px]">{fileName}</span>}
            </span>
            <MatchCardTexts match={m} stale={stale} sourceText={sourceText} />
          </MatchCardButton>
        )
      })}
    </EditorSidebarSection>
  )
}
