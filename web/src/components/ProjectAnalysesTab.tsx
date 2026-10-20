// ProjectAnalysesTab — saved analyses for a project. The list works as an
// accordion: a completed row opens its per-job × target-language breakdown in
// a row directly under it, one analysis at a time. The arrow on each row shows
// it can be opened.

import { Fragment, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { listAnalyses, getAnalysis, deleteAnalysis, type Analysis } from '../api/analyses'
import { queryKeys } from '../lib/query-keys'
import { Button } from './ui/button'
import { DeleteButton } from './ui/delete-button'
import { DisclosureToggle } from './ui/disclosure-toggle'
import { ChoiceToggle, type ChoiceToggleOption } from './ui/choice-toggle'
import { EditableTitle } from './list/EditableTitle'
import NewAnalysisModal from './NewAnalysisModal'
import AnalysisBreakdown, { type AnalysisMetric } from './AnalysisBreakdown'
import { AnalysisExportMenu } from './AnalysisExportMenu'
import ConfirmDialog from './ConfirmDialog'
import { formatDateTime } from '../utils'
import { LoadFailed } from './layout/LoadFailed'
import { LoadingPlaceholder } from './layout/LoadingPlaceholder'
import { CountMeta } from './ui/count-meta'
import { DotSeparator } from './ui/dot-separator'
import { Alert } from './ui/alert'

interface Props {
  projectId: number
}

export default function ProjectAnalysesTab({ projectId }: Props) {
  const qc = useQueryClient()
  const [modalOpen, setModalOpen] = useState(false)
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<{ id: number; name: string } | null>(null)

  // Refetch every 2s while ANY row is non-terminal so the badge flips
  // without a manual refresh. Once everything's completed/failed, polling
  // stops. `refetchInterval` receives the Query object, NOT the data, so read
  // `query.state.data`.
  const listQuery = useQuery({
    queryKey: queryKeys.analyses.list(projectId),
    queryFn: () => listAnalyses(projectId),
    refetchInterval: query => {
      const list = query.state.data
      const anyInFlight = Array.isArray(list) && list.some(a => a.status === 'pending' || a.status === 'running')
      return anyInFlight ? 2000 : false
    },
  })
  const rows = listQuery.data ?? []

  const delMut = useMutationWithToast({
    mutationFn: async (id: number) => {
      await deleteAnalysis(id)
      return id
    },
    successMessage: 'Analysis deleted',
    errorMessage: 'Could not delete the analysis',
    invalidateKeys: [queryKeys.analyses.list(projectId)],
    onSuccess: id => {
      if (selectedId === id) setSelectedId(null)
    },
  })

  const toggleSelect = (id: number) => {
    setSelectedId(prev => (prev === id ? null : id))
  }

  return (
    <div className="space-y-6">
      {/* ── List section ─────────────────────────────────────── */}
      <div className="space-y-3" data-guide="project_analyses.list">
        <div className="flex items-center justify-between">
          <h2 className="text-lg font-semibold">Saved analyses</h2>
          <Button onClick={() => setModalOpen(true)} data-guide="project_analyses.new">New analysis</Button>
        </div>

        {listQuery.isLoading ? (
          <LoadingPlaceholder size="sm" />
        ) : listQuery.isError && !listQuery.data ? (
          <LoadFailed what="analyses" error={listQuery.error} onRetry={() => listQuery.refetch()} />
        ) : rows.length === 0 ? (
          <p className="text-sm text-gray-500">
            No analyses yet. Click <strong>New analysis</strong> to compute a snapshot of TM/MT
            leverage across the project&apos;s jobs.
          </p>
        ) : (
          <div className="overflow-x-auto rounded-md border border-gray-200 dark:border-gray-800">
            {/* Fixed layout: an opened row's breakdown scrolls inside its own
                box instead of stretching this table past the page. */}
            <table className="w-full table-fixed text-sm">
              <thead className="bg-muted text-left text-xs uppercase">
                <tr>
                  <th className="px-3 py-2">Name</th>
                  <th className="px-3 py-2 w-20">Jobs</th>
                  <th className="px-3 py-2 w-52">Created</th>
                  <th className="px-3 py-2 w-24"></th>
                </tr>
              </thead>
              <tbody>
                {rows.map(a => {
                  const isActive = selectedId === a.id
                  const detailId = `analysis-detail-${a.id}`
                  // Only completed analyses have breakdown data to open, so only
                  // those get the hover/click affordance. Pending/running/failed
                  // rows are greyed out and inert — the greying conveys the
                  // state, with no separate status badge (it reads too much
                  // like the project status).
                  const isCompleted = a.status === 'completed'
                  return (
                    <Fragment key={a.id}>
                    <tr
                      data-testid={`analysis-row-${a.id}`}
                      data-status={a.status}
                      className={`border-t border-gray-100 dark:border-gray-800 ${
                        isCompleted
                          ? `cursor-pointer ${isActive ? 'bg-primary/10' : 'hover:bg-muted'}`
                          : 'opacity-50 cursor-default'
                      }`}
                      // The whole row opens it; a click on a button inside the
                      // row (the arrow, Download, Delete) is left to that button.
                      onClick={isCompleted ? e => {
                        if ((e.target as HTMLElement).closest('button')) return
                        toggleSelect(a.id)
                      } : undefined}
                    >
                      <td className="px-3 py-2">
                        <DisclosureToggle
                          open={isActive}
                          onToggle={() => toggleSelect(a.id)}
                          controls={isActive ? detailId : undefined}
                          disabled={!isCompleted}
                          variant="row"
                        >
                          {/* A long name (the default one carries the file
                              name) is cut short, with the full name on hover. */}
                          <EditableTitle name={a.name} canEdit={false} focusable={false} />
                        </DisclosureToggle>
                        {a.status === 'failed' && a.error_message && (
                          <div className="text-xs text-red-600 dark:text-red-400 mt-0.5">{a.error_message}</div>
                        )}
                      </td>
                      <td className="px-3 py-2 text-gray-600 dark:text-gray-400">
                        {/* job_ids is only populated once the worker resolves +
                            persists the set, so a pending/running row would show
                            a misleading 0. Surface the status instead. */}
                        {a.status === 'completed'
                          ? a.job_ids.length
                          : a.status === 'failed'
                            ? '—'
                            : <span className="italic">{a.status === 'running' ? 'Running…' : 'Pending…'}</span>}
                      </td>
                      <td className="px-3 py-2 text-gray-600 dark:text-gray-400">
                        {formatDateTime(a.created_at)}
                      </td>
                      <td className="px-3 py-2">
                        <div className="flex items-center justify-end gap-1">
                          <AnalysisExportMenu analysis={a} />
                          <DeleteButton
                            size="icon-xs"
                            aria-label={`Delete ${a.name}`}
                            onClick={e => {
                              e.stopPropagation()
                              setDeleteTarget({ id: a.id, name: a.name })
                            }}
                          />
                        </div>
                      </td>
                    </tr>
                    {isActive && (
                      <tr className="border-t border-gray-100 dark:border-gray-800">
                        <td colSpan={4} className="p-0">
                          <AnalysisDetailPanel id={detailId} analysisId={a.id} />
                        </td>
                      </tr>
                    )}
                    </Fragment>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <NewAnalysisModal
        open={modalOpen}
        onOpenChange={setModalOpen}
        projectId={projectId}
        onCreated={() => {
          setModalOpen(false)
          qc.invalidateQueries({ queryKey: queryKeys.analyses.list(projectId) })
        }}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={open => !open && setDeleteTarget(null)}
        title={`Delete analysis "${deleteTarget?.name}"?`}
        description="The snapshot is removed permanently. To get fresh numbers later, run a new analysis."
        onConfirm={() => deleteTarget && delMut.mutate(deleteTarget.id)}
      />
    </div>
  )
}

// ── AnalysisDetailPanel ──────────────────────────────────────
//
// The open row's body: the analysis metadata, the metric toggle and the
// breakdown tables. The query refetches while the analysis is not finished so
// its numbers fill in without a reload.

function AnalysisDetailPanel({ id, analysisId }: { id: string; analysisId: number }) {
  // Toggle which metric the breakdown table renders. Words is the default
  // because most translation pricing is per-word; segment count is more
  // useful for QA review estimates. Lives at the panel level so switching
  // doesn't unmount the table and lose scroll position.
  const [metric, setMetric] = useState<AnalysisMetric>('words')

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: queryKeys.analyses.detail(analysisId),
    queryFn: () => getAnalysis(analysisId),
    // Mirror the list-side polling so a `pending` / `running` analysis
    // surfaces its breakdowns the moment they land. Receives the Query, not
    // the data directly.
    refetchInterval: query => {
      const status = query.state.data?.analysis?.status
      return status === 'pending' || status === 'running' ? 2000 : false
    },
  })

  return (
    // Indented a little, so it reads as part of the row above.
    <section id={id} data-testid={id} className="pb-5 pl-6 pr-4 pt-4">
      <div className="mb-4 flex items-center justify-between gap-3">
        {data ? <AnalysisMeta analysis={data.analysis} /> : <span />}
        <ChoiceToggle size="sm" aria-label="Metric" options={METRIC_OPTIONS} value={metric} onChange={setMetric} />
      </div>

      {isLoading && <LoadingPlaceholder size="sm" />}
      {error && !data && <LoadFailed what="analysis" error={error} onRetry={() => refetch()} />}
      {data?.analysis.status === 'failed' && data.analysis.error_message && (
        <Alert tone="error" className="mb-4">
          <strong>Run failed:</strong> {data.analysis.error_message}
        </Alert>
      )}
      {data?.analysis.status === 'pending' || data?.analysis.status === 'running' ? (
        <p className="text-sm text-gray-500">Worker is still computing — refreshing every 2s…</p>
      ) : (
        data && <AnalysisBreakdown rows={data.breakdowns} metric={metric} sourceLanguage={data.analysis.source_language} />
      )}
    </section>
  )
}

function AnalysisMeta({ analysis }: { analysis: Analysis }) {
  return (
    <div className="flex flex-wrap items-center text-xs text-gray-600 dark:text-gray-400">
      <CountMeta items={[{ count: analysis.job_ids.length, noun: 'job' }]} />
      <DotSeparator />
      <span>Created {formatDateTime(analysis.created_at)}</span>
      {analysis.completed_at && analysis.status === 'completed' && (
        <>
          <DotSeparator />
          <span>Completed {formatDateTime(analysis.completed_at)}</span>
        </>
      )}
    </div>
  )
}

const METRIC_OPTIONS: ChoiceToggleOption<AnalysisMetric>[] = [
  { value: 'segments',   label: 'Segments' },
  { value: 'words',      label: 'Words' },
  { value: 'characters', label: 'Characters' },
]
