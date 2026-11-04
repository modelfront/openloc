import { useCallback, useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { getEditorCommentCounts, getEditorQAReport } from '@/api'
import { getMe } from '@/api/auth'
import { useEditorIndex } from '@/hooks/useEditorRows'
import { useJobDetail } from '@/hooks/useJobDetail'
import { usePageTitle } from '@/hooks/usePageTitle'
import { translatedRows, type IndexRow } from '@/lib/editor-index'
import { scopeEditorId } from '@/lib/editor-scope'
import { qeChipLabel } from '@/lib/qe-chip'
import { queryKeys } from '@/lib/query-keys'
import { groupFindingsByTranslation, patchQAReport, type QaFinding } from '@/lib/qa-findings'
import { isAdminRole, isManagerRole } from '@/lib/roles'
import { hasOverlayScrollbars } from '@/lib/scrollbar'
import { extrasColumnWidth } from '@/lib/segment-columns'
import { countByDisplayStatus } from '@/lib/segment-filters'
import { unapprovedCounts, type UnapprovedCounts } from '@/lib/step-completion'
import type { RowFile } from './SegmentGrid'

const NO_SET: ReadonlySet<number> = new Set()

/**
 * What the multi-file editor loads: the user, the project (from its first
 * job: every job of a scope shares the project and the target language), the
 * scope's index with one header per file, and the comment counts. Per file it
 * derives who may edit it and what Complete step would leave unapproved.
 */
export function useScopeEditorData(projectId: number, jobIds: readonly number[]) {
  const editorId = scopeEditorId(projectId, jobIds)
  const { data: me, isPending: mePending } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe, staleTime: Infinity })
  const isManager = isManagerRole(me?.role)
  const isAdmin = isAdminRole(me?.role)
  const { data: firstJob, isPending: jobPending } = useJobDetail(jobIds[0])
  const editorIndex = useEditorIndex(editorId, true)
  const files = useMemo(() => editorIndex.files ?? [], [editorIndex.files])
  usePageTitle(`${files.length || jobIds.length} files`, firstJob?.project_name)

  const { data: commentCounts, isLoadingError: commentCountsFailed } = useQuery({
    queryKey: queryKeys.comments.countsByEditor(editorId),
    queryFn: () => getEditorCommentCounts(editorId),
  })
  const commentCountByTranslation = useMemo(
    () => new Map((commentCounts ?? []).map(c => [c.translation_id, c.comment_count])),
    [commentCounts],
  )

  // The stored QA findings of every file, in one report. A confirm runs QA
  // on its row and patches the report; a run replaces it.
  const qaReportKey = queryKeys.job.qaReport(editorId)
  const { data: qaReport, isLoadingError: qaReportFailed } = useQuery({
    queryKey: qaReportKey,
    queryFn: () => getEditorQAReport(editorId),
  })
  const qaResultsByTranslation = useMemo(() => groupFindingsByTranslation(qaReport?.results), [qaReport])
  const queryClient = useQueryClient()
  const patchQA = useCallback((translationId: number, issues: QaFinding[]) => {
    void patchQAReport(queryClient, queryKeys.job.qaReport(editorId), translationId, issues)
  }, [queryClient, editorId])

  const rows: IndexRow[] = useMemo(() => editorIndex.rows ?? [], [editorIndex.rows])

  // What each file's rows get: its job, whether the caller may edit it (the
  // server's task check, per file), and its "Strings contain HTML" setting.
  const rowFiles = useMemo<RowFile[]>(() => files.map(f => ({
    jobId: f.job_id, fileId: f.file_id, canEdit: !f.edit_block, plainText: !f.inline_html,
  })), [files])
  const rowFile = useCallback((fileIdx: number) => rowFiles[fileIdx], [rowFiles])
  // A translation's file, through its row.
  const lookup = editorIndex.lookup
  const fileOf = useCallback((translationId: number): RowFile | undefined => {
    const i = lookup.byTranslation.get(translationId)
    return i === undefined ? undefined : rowFiles[rows[i].fileIdx]
  }, [lookup, rows, rowFiles])
  const fileNames = useMemo(() => new Map(files.map(f => [f.file_id, f.file_name])), [files])

  // Per file: what Complete step would leave unapproved.
  const unapprovedByFile = useMemo(() => {
    const byFile: IndexRow[][] = files.map(() => [])
    for (const r of translatedRows(rows)) byFile[r.fileIdx]?.push(r)
    return byFile.map(list => unapprovedCounts(list))
  }, [files, rows])
  const unapproved = useMemo<UnapprovedCounts>(() => ({
    drafts: unapprovedByFile.reduce((n, u) => n + u.drafts, 0),
    untranslated: unapprovedByFile.reduce((n, u) => n + u.untranslated, 0),
  }), [unapprovedByFile])
  const statusCounts = useMemo(() => countByDisplayStatus(rows, NO_SET), [rows])
  // Track changes: each row compares with its own file's previous step, so
  // the toggle is offered once any file has one.
  const hasPreviousStep = files.some(f => f.has_previous_step)

  const extrasWidth = useMemo(() => extrasColumnWidth(translatedRows(rows).map(r => ({
    locked: r.locked,
    qeLabel: qeChipLabel(r.qeNoteHead),
    hasQA: (qaResultsByTranslation.get(r.translationId!)?.length ?? 0) > 0,
    commentCount: commentCountByTranslation.get(r.translationId!) ?? 0,
  }))), [rows, qaResultsByTranslation, commentCountByTranslation])
  const [overlayScrollbar] = useState(hasOverlayScrollbars)

  return {
    editorId,
    currentUserId: me?.id ?? null,
    isManager,
    isAdmin,
    project: firstJob && {
      id: projectId,
      name: firstJob.project_name,
      sourceLanguage: firstJob.source_language,
      targetLanguage: firstJob.target_language,
    },
    loading: mePending || jobPending || editorIndex.isPending,
    editorIndex,
    files,
    rows,
    indexLookup: editorIndex.lookup,
    rowFile,
    fileNames,
    commentCountByTranslation,
    commentCountsFailed,
    qaReport,
    qaReportKey,
    qaReportFailed,
    qaResultsByTranslation,
    patchQA,
    fileOf,
    unapprovedByFile,
    unapproved,
    statusCounts,
    hasPreviousStep,
    extrasWidth,
    overlayScrollbar,
  }
}
