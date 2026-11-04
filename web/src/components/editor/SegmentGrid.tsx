import type { ReactNode } from 'react'
import { useQueryClient, type QueryKey } from '@tanstack/react-query'
import { SegmentRow, SEGMENT_GRID_COLS } from '@/components/SegmentRow'
import { SegmentRowPlaceholder } from '@/components/SegmentRowPlaceholder'
import { SelectAllCheckbox } from '@/components/list/SelectAllCheckbox'
import { tableHeaderRowClass } from '@/components/list/list-row'
import { ColumnResizeHandle } from '@/components/ui/column-resize-handle'
import { cn } from '@/lib/utils'
import { isResizableBorder, segmentColumnsStyle } from '@/lib/segment-columns'
import { REPETITION_REPEAT, REPETITION_SOURCE, segmentNumber, type IndexRow } from '@/lib/editor-index'
import { joinsWithNext } from '@/lib/segment-edit'
import { patchQAReport, type QaFinding } from '@/lib/qa-findings'
import type { RefTerms } from '@/lib/ref-terms'
import type { EditorId } from '@/lib/editor-scope'
import type { SpellFixActions } from '@/lib/spellcheck-scope'
import type { useSegmentSplitJoin } from '@/hooks/useSegmentSplitJoin'
import type { ActiveSegment } from './useActiveSegment'
import type { SegmentGridState } from './useSegmentGrid'
import { isSelectToggleClick, type SegmentSelection } from './useEditorSelection'

/** A row without QA findings. One shared array keeps SegmentRow's memo quiet. */
const NO_ISSUES: QaFinding[] = []

// How far the open row's action bar reaches above the row: half its height
// (SegmentRowActionBar, h-4), as it sits centred on its box's top border.
const ACTION_BAR_REACH = 8

// Header cells, in SEGMENT_GRID_COLS order. Match, Status and the unlabelled
// extras column use px-2, like their body cells.
const SEGMENT_HEADER_CELLS = [
  { label: '#', className: 'px-2 py-2' },
  { label: 'Source', className: 'px-3 py-2' },
  { label: 'Translation', className: 'px-3 py-2' },
  { label: 'Match', className: 'px-2 py-2 text-center' },
  { label: <span className="sr-only">Status</span>, className: 'px-2 py-2' },
  { label: null, className: '' },
]

// The last header with text. The unlabelled extras column after it shows only
// when some row has a lock, QE, QA or comment.
const LAST_LABELLED_HEADER = SEGMENT_HEADER_CELLS.reduce((last, cell, i) => (cell.label ? i : last), 0)

interface SegmentGridProps {
  grid: SegmentGridState
  active: ActiveSegment
  selection: SegmentSelection
  /** No row the filter shows can be selected. */
  selectAllDisabled: boolean
  rows: IndexRow[]
  filteredIndices: number[]
  extrasWidth: number
  overlayScrollbar: boolean
  /** A multi-file editor's id: its rows propagate across its files. Unset
   *  for a single job. */
  editorId?: EditorId
  /** What a row's file gives it: its job, whether the caller may edit it,
   *  and its "Strings contain HTML" setting. By the row's `fileIdx`. */
  rowFile: (fileIdx: number) => RowFile
  projectId: number | null
  sourceLanguage: string
  targetLanguage: string
  /** Drawn above the first row the filter shows of each file; none: no header. */
  renderFileHeader?: (fileIdx: number) => ReactNode
  pipelineRunning: boolean
  taskInClearWindow: boolean
  currentUserId: number | null
  /** Split and join on the open row; unset offers neither (a multi-file
   *  editor: both work on one job's index). */
  splitJoin?: ReturnType<typeof useSegmentSplitJoin>
  qaResultsByTranslation: Map<number, QaFinding[]>
  qaReportKey: QueryKey
  /** Takes a row's fresh QA findings; patches the QA report under qaReportKey
   *  when unset. */
  onQARefresh?: (translationId: number, issues: QaFinding[]) => void
  commentCountByTranslation: Map<number, number>
  heldByOthers: ReadonlyMap<number, string>
  autoApprovedBefore: ReadonlySet<number>
  predictedBeforeStep: ReadonlySet<number>
  showWhitespace: boolean
  showTrackChanges: boolean
  /** Cross-reference terms by file: a chip points at a key in its own file. */
  refTermsByFile: ReadonlyMap<number, RefTerms>
  /** The live spell check's fixes beyond the open segment; without them a
   *  fix applies to the open segment only. */
  spellFixActions?: SpellFixActions
  findHighlightTerm: string | null
  /** The TM match the open row's source is diffed against. */
  tmDiffReference: string | null
}

/** A row's file, as the grid needs it. */
export interface RowFile {
  jobId: number
  fileId: number
  canEdit: boolean
  plainText: boolean
}

const NO_REF_TERMS: RefTerms = new Map()

/**
 * The editor's segment list: a sticky column header and the virtualized rows.
 * The header sits INSIDE the scroll container so it shares the body rows'
 * scrollbar-reduced width.
 */
export function SegmentGrid({
  grid, active, selection, selectAllDisabled, rows, filteredIndices, extrasWidth, overlayScrollbar,
  editorId, rowFile, projectId, sourceLanguage, targetLanguage, renderFileHeader,
  pipelineRunning, taskInClearWindow, currentUserId, splitJoin,
  qaResultsByTranslation, qaReportKey, onQARefresh, commentCountByTranslation, heldByOthers, autoApprovedBefore, predictedBeforeStep,
  showWhitespace, showTrackChanges, refTermsByFile, spellFixActions, findHighlightTerm, tmDiffReference,
}: SegmentGridProps) {
  const queryClient = useQueryClient()
  const { segmentScrollRef, headerRef, headerHeight, columnDrag, rowVirtualizer, captureAnchorBeforeActivate, goToSegment, rowAt } = grid
  const { activeSegmentIdx, entryCaret, switchActiveSegment } = active
  // The header draws no border after its last visible column.
  const lastHeader = extrasWidth > 0 ? SEGMENT_HEADER_CELLS.length - 1 : LAST_LABELLED_HEADER

  return (
    <div ref={segmentScrollRef} data-testid="segment-grid" className="flex-1 overflow-y-auto overflow-x-hidden" style={segmentColumnsStyle({ extrasWidth, widths: columnDrag.widths, overlayScrollbar })}>
      {/* Header is a sticky row INSIDE the scroll container so it shares
          the same scrollbar-reduced width as the body rows (see
          headerHeight/scrollMargin in useSegmentGrid). z-20 keeps it above the
          active row (zIndex 10); its background is opaque so rows scroll
          cleanly beneath it. */}
      <div ref={headerRef} className={`${SEGMENT_GRID_COLS} sticky top-0 z-20 ${tableHeaderRowClass}`}>
        {SEGMENT_HEADER_CELLS.map((cell, i) => (
          <div
            key={i}
            className={cn(
              'relative',
              cell.className,
              // A visible line on every draggable border, like the
              // project Tasks tab's header.
              i < lastHeader && 'border-r border-gray-200 dark:border-gray-700',
            )}
          >
            {i === 0 ? (
              <SelectAllHeader state={selection.header} onToggle={selection.toggleAll} disabled={selectAllDisabled} />
            ) : cell.label}
            {/* The border after every visible column but the last: drag
                to move it, double-click to restore the default widths. */}
            {i < lastHeader && isResizableBorder(i, extrasWidth > 0) && (
              <ColumnResizeHandle
                active={columnDrag.dragging === i}
                onPointerDown={columnDrag.startDrag(i)}
                onDoubleClick={columnDrag.reset}
                title="Drag to resize. Double-click to reset."
                data-testid={`segment-column-resize-${i}`}
              />
            )}
          </div>
        ))}
      </div>
      <div style={{ height: `${rowVirtualizer.getTotalSize()}px`, width: '100%', position: 'relative' }}>
        {rowVirtualizer.getVirtualItems().map(virtualRow => {
          const idx = filteredIndices[virtualRow.index]
          const indexRow = rows[idx]
          const loaded = rowAt(idx)
          const seg = loaded?.segment
          const translation = loaded?.translation ?? undefined
          const tid = indexRow.translationId ?? -1
          const file = rowFile(indexRow.fileIdx)
          const canEdit = file.canEdit
          const pos = virtualRow.index
          const firstOfFile = renderFileHeader && (pos === 0 || rows[filteredIndices[pos - 1]].fileIdx !== indexRow.fileIdx)
          return (
            <div
              key={indexRow.segmentId}
              // Dynamic measurement. `estimateSize` is only a
              // first guess; multi-line rows grow past it, so ref +
              // data-index opt the row into TanStack Virtual's
              // auto-measurement loop to keep row N+1 from overlapping.
              ref={rowVirtualizer.measureElement}
              data-index={virtualRow.index}
              // Stable anchor for the scroll-restoration
              // settle loop in the activeSegmentIdx effect — uses
              // boundingClientRect of the row's DOM node, which
              // reflects the current layout even while measureElement
              // refs are still flushing.
              data-segment-idx={idx}
              // Capture position on mousedown (the FIRST
              // event in the click sequence — fires before blur on
              // the previously-active editor, which would otherwise
              // trigger saveDraft and shift layout BEFORE the React
              // synthetic click reaches our handler).
              onMouseDownCapture={activeSegmentIdx === idx ? undefined : (e) => {
                if (!isSelectToggleClick(e)) captureAnchorBeforeActivate(idx)
              }}
              // Cmd/Ctrl+click adds the row to the selection (or takes
              // it out) instead of opening it.
              onClickCapture={(e) => {
                if (indexRow.translationId == null || !isSelectToggleClick(e)) return
                e.preventDefault()
                e.stopPropagation()
                selection.toggle(indexRow.translationId)
              }}
              style={{
                position: 'absolute',
                top: 0,
                left: 0,
                width: '100%',
                // virtualRow.start includes scrollMargin (the sticky
                // header height); the spacer already sits below the
                // header in flow, so subtract it to avoid double-counting.
                transform: `translateY(${virtualRow.start - rowVirtualizer.options.scrollMargin}px)`,
                transition: 'transform 150ms ease-out',
                zIndex: activeSegmentIdx === idx ? 10 : 0,
              }}
            >
              {firstOfFile && renderFileHeader(indexRow.fileIdx)}
              {!seg ? <SegmentRowPlaceholder number={segmentNumber(rows[idx])} /> : (
              <SegmentRow
                segment={seg}
                translation={translation}
                number={segmentNumber(rows[idx])}
                onSplit={splitJoin && activeSegmentIdx === idx && canEdit && tid > 0
                  ? (offset, version) => splitJoin.split.mutate({ jobId: file.jobId, req: { translation_id: tid, offset, expected_updated_at: version } })
                  : undefined}
                onJoinNext={splitJoin && activeSegmentIdx === idx && canEdit && tid > 0 && joinsWithNext(rows, idx)
                  ? version => splitJoin.joinTranslations(file.jobId, [tid, rows[idx + 1].translationId ?? 0], { [tid]: version })
                  : undefined}
                jobId={file.jobId}
                fileId={file.fileId}
                editorId={editorId}
                projectId={projectId}
                targetLanguage={targetLanguage}
                sourceLanguage={sourceLanguage}
                isActive={activeSegmentIdx === idx}
                // virtualRow.start counts from the top of the scroll
                // content, header included: the row's top below the
                // header's bottom edge.
                actionsBelow={activeSegmentIdx === idx && virtualRow.start - (rowVirtualizer.scrollOffset ?? 0) - headerHeight < ACTION_BAR_REACH}
                onActivate={caret => void switchActiveSegment(idx, { caret })}
                onNavigate={(dir, caret) => {
                  // Navigate within filtered results
                  const pos = filteredIndices.indexOf(idx)
                  if (pos === -1) return
                  goToSegment(dir === 'next' ? pos + 1 : pos - 1, caret)
                }}
                entryCaret={entryCaret?.idx === idx ? entryCaret.caret : undefined}
                // Escape is the ONLY transition that discards (no flush).
                // Every other path routes through switchActiveSegment(...) without `discard`.
                onDeactivate={() => void switchActiveSegment(null, { discard: true })}
                dirtyRef={active.activeRowDirtyRef}
                onTermsLoaded={activeSegmentIdx === idx ? active.setActiveTerms : undefined}
                onEditTextChange={activeSegmentIdx === idx ? active.setActiveEditText : undefined}
                insertRef={activeSegmentIdx === idx ? active.activeInsertRef : undefined}
                flushDraftRef={activeSegmentIdx === idx ? active.flushDraftRef : undefined}
                readOnly={!canEdit}
                pretranslating={pipelineRunning}
                qaIssues={qaResultsByTranslation.get(tid) ?? NO_ISSUES}
                showWhitespace={showWhitespace}
                refTerms={refTermsByFile.get(file.fileId) ?? NO_REF_TERMS}
                spellCheck
                spellFixActions={spellFixActions}
                findHighlightTerm={findHighlightTerm}
                tmDiffReference={activeSegmentIdx === idx ? tmDiffReference : undefined}
                currentUserId={currentUserId}
                inClearWindow={taskInClearWindow}
                isRepetition={indexRow.repetition === REPETITION_REPEAT}
                isRepetitionSource={indexRow.repetition === REPETITION_SOURCE}
                commentCount={commentCountByTranslation.get(tid) ?? 0}
                isSelected={selection.selected.has(tid)}
                onSelectClick={selection.onCheckboxClick}
                heldByName={heldByOthers.get(tid) ?? null}
                autoApprovedBeforeStep={autoApprovedBefore.has(tid)}
                predictedBeforeStep={predictedBeforeStep.has(tid)}
                trackChanges={showTrackChanges}
                trackChangesFrom={showTrackChanges ? loaded?.previous_step_text ?? null : null}
                placeables={loaded?.placeables}
                plainText={file.plainText}
                onQARefresh={onQARefresh ?? ((translationId, issues) => {
                  void patchQAReport(queryClient, qaReportKey, translationId, issues)
                })}
              />
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

/** The # header: select or unselect every row the filter shows. */
function SelectAllHeader({ state, onToggle, disabled }: { state: 'none' | 'some' | 'all'; onToggle: () => void; disabled: boolean }) {
  return (
    <label className="flex cursor-pointer items-center gap-1.5 select-none">
      <SelectAllCheckbox
        checked={state === 'all'}
        indeterminate={state === 'some'}
        onChange={onToggle}
        disabled={disabled}
        aria-label="Select all segments shown"
        data-testid="segment-select-all"
      />
      #
    </label>
  )
}
