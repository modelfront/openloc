import { useCallback, useMemo, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { checkJobSpelling } from '@/api'
import FindReplaceModal from '@/components/FindReplaceModal'
import SegmentFilterBar from '@/components/SegmentFilterBar'
import { modKey } from '@/components/SegmentRow'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { PageContainer } from '@/components/layout/PageContainer'
import { EditableTitle } from '@/components/list/EditableTitle'
import { Button } from '@/components/ui/button'
import { Chip } from '@/components/ui/chip'
import { Progress } from '@/components/ui/progress'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { useSegmentHold } from '@/hooks/useSegmentHold'
import { useSegmentSplitJoin } from '@/hooks/useSegmentSplitJoin'
import { useSpellFixes } from '@/hooks/useSpellFixes'
import { useTMMatches } from '@/hooks/useTMMatches'
import { NO_QA_PROFILE_REASON } from '@/lib/qa-findings'
import { invalidateJobsContext } from '@/lib/query-invalidation'
import { joinRefusal } from '@/lib/segment-edit'
import { isFilterActive } from '@/lib/segment-filters'
import { checkByJob } from '@/lib/spellcheck-run'
import { DEFAULT_RUN_SCOPE } from '@/lib/spellcheck-scope'
import { handleTaskLocked, handleTaskReopened } from '@/lib/task-edit'
import { EditorSidebarPane } from './EditorSidebarPane'
import { RunQAButton, SpellCheckButton } from './EditorToolButtons'
import { EditorViewToggles, useEditorViewOptions } from './EditorViewToggles'
import { EditorToolDialogs } from './EditorToolDialogs'
import { ScopeCompleteDialog } from './ScopeCompleteDialog'
import { ScopeFileHeader } from './ScopeFileHeader'
import { ScopeSelectionBar } from './ScopeSelectionBar'
import { SegmentGrid } from './SegmentGrid'
import { useActiveSegment } from './useActiveSegment'
import { useActiveSegmentUrl } from './useActiveSegmentUrl'
import { useEditorDialogs } from './useEditorDialogs'
import { useEditorSelection } from './useEditorSelection'
import { useEditorShortcuts } from './useEditorShortcuts'
import { useEditorSidebar } from './useEditorSidebar'
import { useScopeComplete } from './useScopeComplete'
import { useScopeEditorData } from './useScopeEditorData'
import { useScopeQARun } from './useScopeQARun'
import { useSegmentFiltering } from './useSegmentFiltering'
import { useSegmentGrid } from './useSegmentGrid'
import { useSegmentReferences } from './useSegmentReferences'
import { useSpellCheckRows } from './useSpellCheckRows'

const NO_IDS = new Set<number>()

interface Props {
  projectId: number
  jobIds: number[]
}

/**
 * The CAT editor over several files of one project in one target language:
 * one grid, a header above each file's rows, repetitions and propagation
 * across the files, internal matches from them in the Resources panel, and
 * one Complete step for all of them. Each row keeps its own file's job: its
 * saves, its hold and whether it may be edited. Find & replace, QA, Spell
 * check and track changes cover every file, each request going to its rows'
 * job; Split and Join stay within one file. XLIFF import and the leverage
 * bar are the single-file editor's.
 */
export function MultiFileWorkspace({ projectId: pid, jobIds }: Props) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const segmentScrollRef = useRef<HTMLDivElement>(null)
  const active = useActiveSegment(segmentScrollRef)
  const { activeSegmentIdx, switchActiveSegment } = active

  const data = useScopeEditorData(pid, jobIds)
  const { editorId, rows, files, indexLookup, editorIndex, rowFile, fileOf, isManager, isAdmin, project } = data
  const gridShown = !data.loading

  const view = useEditorViewOptions()
  const showTrackChanges = view.trackChanges && data.hasPreviousStep
  const { segmentFilters, setSegmentFilters, segmentSearchRef, searchState, filteredIndices } = useSegmentFiltering({
    jid: editorId,
    rows,
    qaResultsByTranslation: data.qaResultsByTranslation,
    commentCountByTranslation: data.commentCountByTranslation,
    predictedBeforeStep: NO_IDS,
    commentCountsFailed: data.commentCountsFailed,
    qaReportFailed: data.qaReportFailed,
  })
  const { selection, selectionRef, orderedTranslationIds, indexRowByTranslation, pickedSet, pickedRows } = useEditorSelection(rows, filteredIndices, indexLookup)
  const grid = useSegmentGrid({
    jid: editorId, segmentScrollRef, indexRows: editorIndex.rows, rows, indexLookup, filteredIndices,
    extrasWidth: data.extrasWidth, gridShown, active,
  })
  const { scrollToRow, scrollToTranslation } = grid
  useActiveSegmentUrl({
    activeSegmentId: activeSegmentIdx != null ? rows[activeSegmentIdx]?.segmentId : undefined,
    ready: gridShown && !!editorIndex.rows,
    indexLookup,
    scrollToRow,
  })

  // The open row and its file: the Resources panel, the hold and the
  // keyboard inserts follow that file's job and task.
  const activeIndexRow = activeSegmentIdx != null ? rows[activeSegmentIdx] : undefined
  const activeFile = activeIndexRow ? rowFile(activeIndexRow.fileIdx) : rowFile(0)
  const activeJobId = activeFile?.jobId ?? jobIds[0]
  const activeCanEdit = activeFile?.canEdit ?? false
  const activeRow = activeSegmentIdx != null ? grid.rowAt(activeSegmentIdx) : undefined
  const activeSourceRich = activeRow?.segment.source_rich_text
  const activeSourceText = activeRow?.segment.source_text ?? ''
  const activeTranslationId = activeIndexRow?.translationId ?? undefined

  const { refTermsByFile, activeReferences, jumpToReference } = useSegmentReferences({
    jid: editorId, enabled: true, rows, indexLookup, activeSourceRich, activeFileId: activeFile?.fileId, scrollToTranslation,
  })
  const sidebar = useEditorSidebar({ active, activeSourceRich: activeSourceRich ?? '', canEdit: activeCanEdit, plainTextFile: activeFile?.plainText ?? false })
  const { usable: usableTMMatches } = useTMMatches(activeJobId, activeSourceText, activeTranslationId ?? null)
  const dialogs = useEditorDialogs({ jid: activeJobId, isManager, segmentScrollRef, active })

  // Split on the open row, Join on rows of one file: each through that file's job.
  const openTranslation = useCallback((id: number) => void scrollToTranslation(id, { activate: true }), [scrollToTranslation])
  const splitJoin = useSegmentSplitJoin({ editorId, projectId: pid, lookup: indexLookup, openTranslation })
  const joinFile = pickedRows.length > 0 ? rowFile(rows[pickedRows[0]].fileIdx) : undefined
  const selectionJoinRefusal = joinRefusal(rows, pickedRows) ?? (joinFile?.canEdit ? null : 'You cannot edit this file now.')
  const joinSelection = async () => {
    if (!joinFile) return
    await switchActiveSegment(null)
    const ids = pickedRows.flatMap(i => rows[i].translationId ?? [])
    selection.clear()
    splitJoin.joinTranslations(joinFile.jobId, ids)
  }

  // Find & replace, QA and Spell check over every file, each request sent to
  // the job of its rows.
  const jobOf = useCallback((id: number) => fileOf(id)?.jobId, [fileOf])
  const findReplaceJobs = useMemo(() => ({ jobIds: files.map(f => f.job_id), jobOf }), [files, jobOf])
  const anyEditable = files.some(f => !f.edit_block)
  const qaRun = useScopeQARun({
    editorId,
    jobIds: findReplaceJobs.jobIds,
    fileName: jobId => files.find(f => f.job_id === jobId)?.file_name ?? String(jobId),
    beforeRun: () => switchActiveSegment(null),
  })
  const spellRows = useSpellCheckRows(rows)
  const { applySpellFixes, spellFixActions } = useSpellFixes({
    editorId, pid, fileOf, targetLanguage: project?.targetLanguage, isAdmin, activeTranslationId,
  })

  const noop = useCallback(() => {}, [])
  useEditorShortcuts({
    active,
    canEdit: activeCanEdit,
    tmMatches: usableTMMatches,
    segmentSearchRef,
    selectionRef,
    openConcordance: dialogs.openConcordance,
    openFindReplace: dialogs.openFindReplace,
    openJump: noop,
    openQuickAddTerm: dialogs.openQuickAddTerm,
    handleSidebarInsert: sidebar.handleSidebarInsert,
  })

  const activeRowLocked = activeIndexRow?.locked === true
  const { heldByOthers, streamLive: holdStreamLive } = useSegmentHold({
    jobId: activeJobId,
    editorId,
    translationId: activeTranslationId ?? null,
    canHold: activeCanEdit && !activeRowLocked,
    currentUserId: data.currentUserId,
    onIdle: () => { void switchActiveSegment(null) },
  })
  const activeRowHeld = activeTranslationId != null && heldByOthers.has(activeTranslationId)

  const complete = useScopeComplete({
    editorId, projectId: pid, files, isManager, currentUserId: data.currentUserId,
    beforeSend: () => switchActiveSegment(null),
    // A linguist has no project page: back to their tasks.
    onAllDone: () => navigate(isManager ? `/projects/${pid}` : '/tasks'),
  })
  const unapprovedOf = (jobId: number) =>
    data.unapprovedByFile[files.findIndex(f => f.job_id === jobId)] ?? { drafts: 0, untranslated: 0 }

  if (data.loading) return <LoadingPlaceholder continues message="Loading editor…" />

  const total = indexRowByTranslation.size
  const approved = total - data.unapproved.drafts - data.unapproved.untranslated
  const pct = total > 0 ? Math.round((approved / total) * 100) : 0

  return (
    <TooltipProvider delay={200}>
      <PageContainer width="full" className="max-md:min-w-3xl" data-hold-stream={holdStreamLive ? 'live' : 'polling'}>
        <Breadcrumbs
          items={[
            { label: 'Projects', to: '/projects' },
            { label: <EditableTitle name={project?.name ?? String(pid)} canEdit={false} />, to: `/projects/${pid}`, shrink: true },
            { label: <span data-testid="scope-file-count">{files.length} files</span> },
          ]}
          trailing={project && <Chip data-testid="cat-target-language">{project.targetLanguage.toUpperCase()}</Chip>}
          actions={
            <>
              <Tooltip>
                <TooltipTrigger render={
                  <Button variant="outline" size="sm" onMouseDown={e => e.preventDefault()} onClick={() => dialogs.openConcordance()} />
                }>
                  Concordance
                </TooltipTrigger>
                <TooltipContent>{modKey}+K — search TM/terminology for the selected phrase</TooltipContent>
              </Tooltip>
              <SpellCheckButton disabled={total === 0} onClick={() => dialogs.openSpellCheck(DEFAULT_RUN_SCOPE)} />
              <RunQAButton report={data.qaReport} pending={qaRun.isPending} disabled={total === 0} onRun={() => qaRun.mutate(undefined)} />
              <Button
                size="sm"
                variant="success"
                disabled={complete.completable.length === 0}
                disabledReason={complete.completable.length === 0 ? 'None of these files has a step you can complete.' : undefined}
                onClick={complete.openDialog}
                data-testid="scope-complete-open"
              >
                Complete step
              </Button>
            </>
          }
        />

        {editorIndex.error ? (
          <LoadFailed what="segments" error={editorIndex.error} onRetry={() => void editorIndex.refetch()} />
        ) : (
          <>
            {total > 0 && (
              <Progress value={pct} size="sm" tone="green" title={`${pct}% approved (${approved}/${total})`}
                aria-label="Segments approved" aria-valuetext={`${pct}% approved (${approved}/${total})`} className="mb-2" />
            )}
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
              actions={<EditorViewToggles view={view} hasPreviousStep={data.hasPreviousStep} showTrackChanges={showTrackChanges} previousStepName={undefined} />}
            />
            {selection.selected.size > 0 && (
              <ScopeSelectionBar
                projectId={pid}
                picked={selection.picked}
                selectedCount={selection.selected.size}
                rowOf={id => { const i = indexLookup.byTranslation.get(id); return i === undefined ? undefined : rows[i] }}
                rowFile={rowFile}
                isManager={isManager}
                beforeRun={() => switchActiveSegment(null)}
                onClearSelection={selection.clear}
                onJoin={() => void joinSelection()}
                joinRefusal={selectionJoinRefusal}
                onRunQA={() => qaRun.mutate(selection.picked)}
                qaRunning={qaRun.isPending}
                qaDisabledReason={data.qaReport?.profile_assigned === false ? NO_QA_PROFILE_REASON : undefined}
                onSpellCheck={() => dialogs.openSpellCheck('selected')}
                onFindReplace={() => dialogs.openFindReplace(true)}
              />
            )}
            <div className="flex-1 flex min-h-0 gap-0">
              <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 overflow-hidden flex flex-col min-h-0 flex-1 min-w-0">
                <FindReplaceModal
                  open={dialogs.findReplaceOpen}
                  onClose={dialogs.closeFindReplace}
                  jobs={findReplaceJobs}
                  onHighlight={dialogs.setFindHighlightTerm}
                  onScrollToTranslation={scrollToTranslation}
                  onDeactivate={() => void switchActiveSegment(null)}
                  onComplete={({ writtenJobs, reopenedJobs }) => {
                    invalidateJobsContext(queryClient, pid, writtenJobs)
                    for (const jobId of reopenedJobs) handleTaskReopened(queryClient, pid, jobId)
                  }}
                  onTaskLocked={(err, jobId) => handleTaskLocked(queryClient, pid, jobId, err)}
                  readOnly={!anyEditable}
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
                  editorId={editorId}
                  rowFile={rowFile}
                  projectId={pid}
                  sourceLanguage={project?.sourceLanguage ?? ''}
                  targetLanguage={project?.targetLanguage ?? ''}
                  renderFileHeader={i => <ScopeFileHeader file={files[i]} unapproved={data.unapprovedByFile[i]} />}
                  pipelineRunning={false}
                  taskInClearWindow={false}
                  currentUserId={data.currentUserId}
                  qaResultsByTranslation={data.qaResultsByTranslation}
                  splitJoin={splitJoin}
                  qaReportKey={data.qaReportKey}
                  onQARefresh={data.patchQA}
                  commentCountByTranslation={data.commentCountByTranslation}
                  heldByOthers={heldByOthers}
                  autoApprovedBefore={NO_IDS}
                  predictedBeforeStep={NO_IDS}
                  showWhitespace={view.showWhitespace}
                  showTrackChanges={showTrackChanges}
                  refTermsByFile={refTermsByFile}
                  spellFixActions={spellFixActions}
                  findHighlightTerm={dialogs.findHighlightTerm}
                  tmDiffReference={usableTMMatches[0]?.source_text ?? null}
                />
              </div>
              <EditorSidebarPane
                sidebar={sidebar}
                jobId={activeJobId}
                editorId={editorId}
                segmentId={activeIndexRow?.segmentId ?? null}
                fileNames={data.fileNames}
                sourceText={activeSourceText}
                editText={active.activeEditText}
                terms={active.activeTerms}
                references={activeReferences}
                onJumpToReference={refKey => void jumpToReference(refKey)}
                plainText={activeFile?.plainText ?? false}
                onAddTerm={dialogs.quickAddOpen ? dialogs.openQuickAddTerm : undefined}
                translationId={activeTranslationId ?? null}
                qaIssues={activeTranslationId ? data.qaResultsByTranslation.get(activeTranslationId) ?? [] : []}
                onQARefresh={data.patchQA}
              />
            </div>
          </>
        )}
      </PageContainer>

      <EditorToolDialogs
        dialogs={dialogs}
        active={active}
        projectId={pid}
        sourceLanguage={project?.sourceLanguage ?? ''}
        targetLanguage={project?.targetLanguage ?? ''}
        isManager={isManager}
        termJobId={activeJobId}
        plainText={activeFile?.plainText ?? false}
        activeRowReadOnly={!activeCanEdit || activeRowLocked || activeRowHeld}
        spellCheck={{
          // Each chunk's rows go to their own file's job.
          check: (ids, signal, opts) => checkByJob(jobOf, (jobId, part, s) => checkJobSpelling(jobId, part, s, opts))(ids, signal),
          readOnly: !anyEditable,
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
        // Scrolls the row into view behind the dialog without opening it.
        onNavigate={id => void scrollToTranslation(id, { activate: false })}
      />
      <ScopeCompleteDialog
        open={complete.open}
        onOpenChange={complete.setOpen}
        files={complete.targets}
        unapproved={complete.targets.map(f => unapprovedOf(f.job_id))}
        pending={complete.mutation.isPending}
        failures={complete.failures}
        onConfirm={(comment, approveAll) => complete.mutation.mutate({ comment, approveAll })}
      />
    </TooltipProvider>
  )
}
