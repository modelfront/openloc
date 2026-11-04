import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Loader2Icon } from 'lucide-react'
import { useQueryClient } from '@tanstack/react-query'
import FindReplaceModal from '@/components/FindReplaceModal'
import JobLeverageStatusBar, { LEVERAGE_BAR_CLEARANCE } from '@/components/JobLeverageStatusBar'
import SegmentFilterBar from '@/components/SegmentFilterBar'
import { SegmentSelectionBar } from '@/components/SegmentSelectionBar'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { PageContainer } from '@/components/layout/PageContainer'
import { Progress } from '@/components/ui/progress'
import { TooltipProvider } from '@/components/ui/tooltip'
import { useJobPipelineActions } from '@/hooks/useJobPipelineActions'
import { useSegmentHold } from '@/hooks/useSegmentHold'
import { useSegmentSplitJoin } from '@/hooks/useSegmentSplitJoin'
import { useSpellFixes } from '@/hooks/useSpellFixes'
import { useTMMatches } from '@/hooks/useTMMatches'
import { isPipelineRunning } from '@/lib/project-logic'
import { patchQAReport, NO_QA_PROFILE_REASON } from '@/lib/qa-findings'
import { queryKeys } from '@/lib/query-keys'
import { isFilterActive } from '@/lib/segment-filters'
import { joinRefusal } from '@/lib/segment-edit'
import { handleTaskLocked, handleTaskReopened } from '@/lib/task-edit'
import { cn } from '@/lib/utils'
import { EditorDialogs } from './EditorDialogs'
import { EditorHeader } from './EditorHeader'
import { EditorSidebarPane } from './EditorSidebarPane'
import { EditorViewToggles, useEditorViewOptions } from './EditorViewToggles'
import { SegmentGrid } from './SegmentGrid'
import { useActiveSegment } from './useActiveSegment'
import { useActiveSegmentUrl } from './useActiveSegmentUrl'
import { useEditorData } from './useEditorData'
import { useEditorDialogs } from './useEditorDialogs'
import { useEditorSelection } from './useEditorSelection'
import { useEditorShortcuts } from './useEditorShortcuts'
import { useEditorSidebar } from './useEditorSidebar'
import { useSegmentFiltering } from './useSegmentFiltering'
import { useSegmentGrid } from './useSegmentGrid'
import { useSegmentReferences } from './useSegmentReferences'
import { useSpellCheckRows } from './useSpellCheckRows'
import { useStepCompletion } from './useStepCompletion'

interface EditorWorkspaceProps {
  projectId: number
  jobId: number
}

/**
 * The CAT editor for one job: header and toolbar, filter bar, segment grid,
 * Resources panel and the dialogs. The hooks beside it hold the wiring; this
 * component connects them and lays the page out.
 */
export function EditorWorkspace({ projectId: pid, jobId: jid }: EditorWorkspaceProps) {
  const queryClient = useQueryClient()
  // The segment list's scroll container: the selection lookups and the
  // click-outside check are scoped to it.
  const segmentScrollRef = useRef<HTMLDivElement>(null)
  const active = useActiveSegment(segmentScrollRef)
  const { activeSegmentIdx, switchActiveSegment } = active

  const data = useEditorData(jid, active.flushDraftRef)
  const {
    job, isAdmin, isManager, currentUserId, rows, indexRows, indexLookup, editorIndex, canEdit, plainTextFile,
    gridShown, qaReport, qaReportKey, qaResultsByTranslation, commentCountByTranslation, predictedBeforeStep,
  } = data

  const view = useEditorViewOptions()
  const showTrackChanges = view.trackChanges && data.hasPreviousStep

  const { segmentFilters, setSegmentFilters, segmentSearchRef, searchState, filteredIndices } = useSegmentFiltering({
    jid,
    rows,
    qaResultsByTranslation,
    commentCountByTranslation,
    predictedBeforeStep,
    commentCountsFailed: data.commentCountsFailed,
    qaReportFailed: data.qaReportFailed,
  })

  const completion = useStepCompletion({ jid, pid, jobSteps: data.jobSteps, indexLookup, rows })
  const pipeline = useJobPipelineActions(jid, pid)
  const dialogs = useEditorDialogs({ jid, isManager, segmentScrollRef, active })
  const { selection, selectionRef, orderedTranslationIds, indexRowByTranslation, pickedSet, pickedRows } = useEditorSelection(rows, filteredIndices, indexLookup)

  const grid = useSegmentGrid({
    jid, segmentScrollRef, indexRows, rows, indexLookup, filteredIndices, extrasWidth: data.extrasWidth, gridShown, active,
  })
  const { scrollToRow, scrollToTranslation } = grid

  // Split and join, while the task takes edits: Split on the open row's
  // source, Alt+Shift+J before a row it can join, Join on a selection.
  const openTranslation = useCallback((id: number) => void scrollToTranslation(id, { activate: true }), [scrollToTranslation])
  const splitJoin = useSegmentSplitJoin({ editorId: jid, projectId: pid, lookup: indexLookup, openTranslation })
  const joinSelection = async () => {
    await switchActiveSegment(null)
    const ids = pickedRows.flatMap(i => rows[i].translationId ?? [])
    selection.clear()
    splitJoin.joinTranslations(jid, ids)
  }

  useActiveSegmentUrl({
    activeSegmentId: activeSegmentIdx != null ? rows[activeSegmentIdx]?.segmentId : undefined,
    ready: gridShown && !!indexRows,
    indexLookup,
    scrollToRow,
  })
  const activeRow = activeSegmentIdx != null ? grid.rowAt(activeSegmentIdx) : undefined
  const findReplaceJobs = useMemo(() => ({ jobIds: [jid], jobOf: () => jid }), [jid])

  // --- Spell check review dialog ---
  //
  // Every correction goes through the bulk draft endpoint, including the segment
  // the user last had open: `beforeRun` deactivates it first, so there's one
  // write path instead of an editor branch racing its own autosave against the
  // batch writes.
  const spellRows = useSpellCheckRows(rows)

  const activeSourceRich = activeRow?.segment.source_rich_text
  const { refTermsByFile, activeReferences, jumpToReference } = useSegmentReferences({
    jid, enabled: !!job, rows, indexLookup, activeSourceRich, activeFileId: job?.file_id, scrollToTranslation,
  })
  // One file: every row has the job's file and task.
  const rowFile = useCallback(() => ({ jobId: jid, fileId: job?.file_id ?? 0, canEdit, plainText: plainTextFile }),
    [jid, job?.file_id, canEdit, plainTextFile])
  const sidebar = useEditorSidebar({ active, activeSourceRich: activeSourceRich ?? '', canEdit, plainTextFile })

  const activeSourceText = activeRow?.segment.source_text ?? ''

  // Same hook + query key the sidebar's TM section uses, so this shares its
  // request rather than issuing a second one.
  const activeTranslationId = activeSegmentIdx != null ? rows[activeSegmentIdx]?.translationId ?? undefined : undefined

  const { usable: usableTMMatches } = useTMMatches(jid, activeSourceText, activeTranslationId ?? null)

  // The hit the segment's source is diffed against: whichever TM card the user
  // is pointing at, else the best one. Cleared on segment switch so a hover
  // never carries over to the next segment's source.
  const [hoveredTMSource, setHoveredTMSource] = useState<string | null>(null)
  // eslint-disable-next-line react-hooks/set-state-in-effect -- a reset on segment switch, which the hover state cannot derive
  useEffect(() => { setHoveredTMSource(null) }, [activeSegmentIdx])
  const tmDiffReference = hoveredTMSource ?? usableTMMatches[0]?.source_text ?? null

  useEditorShortcuts({
    active,
    canEdit,
    tmMatches: usableTMMatches,
    segmentSearchRef,
    selectionRef,
    openConcordance: dialogs.openConcordance,
    openFindReplace: dialogs.openFindReplace,
    openJump: dialogs.openJump,
    openQuickAddTerm: dialogs.openQuickAddTerm,
    handleSidebarInsert: sidebar.handleSidebarInsert,
  })

  const spellFileOf = useCallback(() => (job ? { jobId: jid, fileId: job.file_id } : undefined), [jid, job])
  const { applySpellFixes, spellFixActions } = useSpellFixes({
    editorId: jid, pid, fileOf: spellFileOf, targetLanguage: job?.target_language, isAdmin, activeTranslationId,
  })

  // Segment holds: the open row is held for this user while they may edit it;
  // a row someone else holds is read-only here and names them. It follows
  // activeTranslationId, which changes only after switchActiveSegment's flush.
  const activeRowLocked = activeSegmentIdx != null && rows[activeSegmentIdx]?.locked === true
  const { heldByOthers, streamLive: holdStreamLive } = useSegmentHold({
    jobId: jid,
    translationId: activeTranslationId ?? null,
    canHold: canEdit && !activeRowLocked,
    currentUserId,
    onIdle: () => { void switchActiveSegment(null) },
  })
  const activeRowHeld = activeTranslationId != null && heldByOthers.has(activeTranslationId)

  // Same message as the route's Suspense fallback, so loading the editor code
  // and loading its data read as one screen.
  if (data.editorLoading) return <LoadingPlaceholder continues message="Loading editor…" />

  return (
    <TooltipProvider delay={200}>
      {/* data-hold-stream says whether other editors' holds arrive live or by poll. */}
      <PageContainer width="full" className={cn('max-md:min-w-3xl', LEVERAGE_BAR_CLEARANCE)} data-hold-stream={holdStreamLive ? 'live' : 'polling'}>
        <EditorHeader projectId={pid} jobId={jid} data={data} completion={completion} dialogs={dialogs} pipeline={pipeline} />

        {data.aiRunning ? (
          // QE+APE hard lock. The QE+APE engine is rewriting/approving rows;
          // human edits would race the predict writes, so the grid is hidden
          // until the step completes. The header keeps its live badge + the
          // job-detail poll (every 2s) trips the finish effect, which reloads
          // translations automatically.
          <div
            className="flex-1 flex flex-col items-center justify-center gap-3 py-24 text-center"
            data-testid="qeape-waiting"
          >
            <Loader2Icon aria-hidden="true" className="size-8 animate-spin text-gray-400 dark:text-gray-500" />
            <div className="text-base font-medium text-gray-900 dark:text-gray-100">
              {job?.current_step || 'QE+APE in progress'}
            </div>
            <div className="max-w-md text-sm text-gray-500 dark:text-gray-400">
              This task is locked while automated quality evaluation and post-editing run.
              It unlocks automatically as soon as the step completes.
            </div>
          </div>
        ) : editorIndex.error ? (
          // Without the index there is no grid to show; an empty one would
          // read as a file with no segments.
          <LoadFailed what="segments" error={editorIndex.error} onRetry={() => void editorIndex.refetch()} />
        ) : (
          <>
          {/* Progress bar */}
          {indexRowByTranslation.size > 0 && (() => {
            // Approved and auto-approved: every translation Complete step would not count.
            const total = indexRowByTranslation.size
            const approved = total - data.unapproved.drafts - data.unapproved.untranslated
            const pct = Math.round((approved / total) * 100)
            const summary = `${pct}% approved (${approved}/${total})`
            return (
              <Progress
                value={pct}
                size="sm"
                tone="green"
                title={summary}
                aria-label="Segments approved"
                aria-valuetext={summary}
                className="mb-2"
              />
            )
          })()}

          <SegmentFilterBar
            filters={segmentFilters}
            searchInputRef={segmentSearchRef}
            searchState={searchState}
            onChange={setSegmentFilters}
            totalCount={rows.length}
            filteredCount={filteredIndices.length}
            commentCountsUnavailable={data.commentCountsFailed}
            qaReportUnavailable={data.qaReportFailed}
            onOpenFindReplace={() => dialogs.openFindReplace(false)}
            statusCounts={data.statusCounts}
            actions={
              <EditorViewToggles
                view={view}
                hasPreviousStep={data.hasPreviousStep}
                showTrackChanges={showTrackChanges}
                previousStepName={data.previousStepName}
              />
            }
          />

          {selection.selected.size > 0 && job && (
            <SegmentSelectionBar
              jobId={jid}
              projectId={pid}
              picked={selection.picked}
              selectedCount={selection.selected.size}
              translationsById={indexRowByTranslation}
              canConfirm={canEdit}
              canLock={canEdit && isManager}
              canUnlock={canEdit}
              canClear={canEdit && data.taskInClearWindow}
              canPretranslate={canEdit && data.inFirstStep && job.pipeline_status !== 'failed' && !isPipelineRunning(job)}
              qaDisabledReason={qaReport?.profile_assigned === false ? NO_QA_PROFILE_REASON : undefined}
              onJoin={canEdit ? () => void joinSelection() : undefined}
              joinRefusal={joinRefusal(rows, pickedRows)}
              beforeRun={() => switchActiveSegment(null)}
              onSpellCheck={() => dialogs.openSpellCheck('selected')}
              onFindReplace={() => dialogs.openFindReplace(true)}
              onClearSelection={selection.clear}
            />
          )}

          <div className="flex-1 flex min-h-0 gap-0">
            <div data-guide="editor.segments" className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 overflow-hidden flex flex-col min-h-0 flex-1 min-w-0">
              {/* Find & Replace bar — sits above segment grid, no backdrop/blur */}
              <FindReplaceModal
                open={dialogs.findReplaceOpen}
                onClose={dialogs.closeFindReplace}
                jobs={findReplaceJobs}
                onHighlight={dialogs.setFindHighlightTerm}
                onScrollToTranslation={scrollToTranslation}
                onDeactivate={() => void switchActiveSegment(null)}
                onComplete={({ reopenedJobs }) => {
                  queryClient.invalidateQueries({ queryKey: queryKeys.job.translations(jid) })
                  queryClient.invalidateQueries({ queryKey: queryKeys.job.detail(jid) })
                  if (reopenedJobs.length > 0) handleTaskReopened(queryClient, pid, jid)
                }}
                onTaskLocked={err => handleTaskLocked(queryClient, pid, jid, err)}
                readOnly={!canEdit}
                selectedIds={selection.picked}
                initialInSelection={dialogs.findInSelection}
                canReplaceLocked={isAdmin}
              />
              <SegmentGrid
                grid={grid}
                active={active}
                selection={selection}
                selectAllDisabled={orderedTranslationIds.length === 0}
                rows={rows}
                filteredIndices={filteredIndices}
                extrasWidth={data.extrasWidth}
                overlayScrollbar={data.overlayScrollbar}
                rowFile={rowFile}
                projectId={job?.project_id ?? null}
                sourceLanguage={job?.source_language ?? ''}
                targetLanguage={job?.target_language ?? ''}
                pipelineRunning={data.pipelineRunning}
                taskInClearWindow={data.taskInClearWindow}
                currentUserId={currentUserId}
                splitJoin={splitJoin}
                qaResultsByTranslation={qaResultsByTranslation}
                qaReportKey={qaReportKey}
                commentCountByTranslation={commentCountByTranslation}
                heldByOthers={heldByOthers}
                autoApprovedBefore={data.autoApprovedBefore}
                predictedBeforeStep={predictedBeforeStep}
                showWhitespace={view.showWhitespace}
                showTrackChanges={showTrackChanges}
                refTermsByFile={refTermsByFile}
                spellFixActions={spellFixActions}
                findHighlightTerm={dialogs.findHighlightTerm}
                tmDiffReference={tmDiffReference}
              />
            </div>
            <EditorSidebarPane
              sidebar={sidebar}
              jobId={jid}
              sourceText={activeSourceText}
              editText={active.activeEditText}
              terms={active.activeTerms}
              references={activeReferences}
              onJumpToReference={refKey => void jumpToReference(refKey)}
              plainText={plainTextFile}
              onAddTerm={dialogs.quickAddOpen ? dialogs.openQuickAddTerm : undefined}
              onMatchHover={setHoveredTMSource}
              translationId={activeTranslationId ?? null}
              segmentId={activeSegmentIdx != null ? rows[activeSegmentIdx]?.segmentId ?? null : null}
              qaIssues={activeTranslationId ? qaResultsByTranslation.get(activeTranslationId) ?? [] : []}
              onQARefresh={(translationId, issues) => {
                void patchQAReport(queryClient, qaReportKey, translationId, issues)
              }}
            />
          </div>
          </>
        )}
      </PageContainer>
      {/* The dialogs live at page level, NOT inside the loaded-segments
          branch: the toolbar buttons that open them render outside every
          loading/locked conditional, so a dialog mounted inside one would make
          the click a silent no-op while translations load or while a QE+APE
          step holds the editor locked. */}
      <EditorDialogs
        jobId={jid}
        data={data}
        dialogs={dialogs}
        completion={completion}
        active={active}
        activeRowReadOnly={activeRowLocked || activeRowHeld}
        spellCheck={{
          spellFixActions,
          segmentIds: spellRows.segmentIds,
          filteredIndices,
          translationBySegment: spellRows.translationBySegment,
          filterActive: isFilterActive(segmentFilters),
          selectedIds: pickedSet,
          onApply: async (error, replacement, includeLocked) =>
            (await applySpellFixes([error], replacement, includeLocked)) > 0,
          onApplyAll: applySpellFixes,
        }}
        scrollToRow={scrollToRow}
        scrollToTranslation={scrollToTranslation}
      />

      {/* Always-visible live leverage breakdown, fixed to the viewport
          bottom, VSCode-style. The page's LEVERAGE_BAR_CLEARANCE keeps
          everything above it. */}
      <JobLeverageStatusBar jobId={jid} localRepetitionStats={editorIndex.repetitionStats} segmentCount={rows.length} wordCount={job?.word_count ?? 0} />
    </TooltipProvider>
  )
}
