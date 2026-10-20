import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Chip } from '@/components/ui/chip'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { getProjectTimings, type ProjectTimings, type StageEntry } from '@/api/timings'
import { queryKeys } from '@/lib/query-keys'
import { formatCount, formatDurationMs, formatShare } from '@/lib/duration'

/**
 * Project "Processing time" panel.
 *
 * Reads the accumulating job_timings rows and answers three questions in the
 * order people actually ask them:
 *   1. How much machine time has this project consumed?
 *   2. Which stage is eating it?
 *   3. Which job is the outlier?
 *
 * Numbers are cumulative across every run — re-running "Apply MT" adds to them
 * rather than replacing them. That's the whole point of the storage decision,
 * so the panel says so rather than letting someone read a re-run job as slow.
 */

interface Props {
  projectId: number
  open: boolean
  onOpenChange: (open: boolean) => void
}

/** Group → label + bar colour. Groups come from the slug prefix server-side. */
const GROUP_META: Record<string, { label: string; bar: string; dot: string }> = {
  ingest:   { label: 'Ingest',   bar: 'bg-slate-400 dark:bg-slate-500',     dot: 'bg-slate-400' },
  tm:       { label: 'TM',       bar: 'bg-emerald-500 dark:bg-emerald-600', dot: 'bg-emerald-500' },
  mt:       { label: 'MT',       bar: 'bg-orange-500 dark:bg-orange-600',   dot: 'bg-orange-500' },
  ai:       { label: 'QE+APE', bar: 'bg-violet-500 dark:bg-violet-600',   dot: 'bg-violet-500' },
  feedback: { label: 'Completion', bar: 'bg-cyan-500 dark:bg-cyan-600',       dot: 'bg-cyan-500' },
  qa:       { label: 'QA',       bar: 'bg-amber-500 dark:bg-amber-600',     dot: 'bg-amber-500' },
  // task.* self time is whatever the task did outside any named stage.
  task:     { label: 'Overhead', bar: 'bg-blue-500 dark:bg-blue-600',       dot: 'bg-blue-500' },
}

/** Display order for groups, so the panel reads as the pipeline runs rather
 *  than reshuffling by size. Overhead last — it's the residue, not a phase. */
const GROUP_ORDER = ['ingest', 'tm', 'mt', 'ai', 'feedback', 'qa', 'task']

interface StageGroup {
  group: string
  selfMs: number
  share: number
  err: number
  stages: StageEntry[]
}

/**
 * Fold the flat stage list into one entry per group.
 *
 * Summing self time across a group is exact — that's the whole point of the
 * self-time model. Doing the same with inclusive time would be nonsense, since
 * a task span contains the stages in every other group.
 */
function groupStages(stages: StageEntry[]): StageGroup[] {
  const byGroup = new Map<string, StageGroup>()
  let total = 0
  for (const s of stages) {
    total += s.self_ms
    const g = byGroup.get(s.group) ?? { group: s.group, selfMs: 0, share: 0, err: 0, stages: [] }
    g.selfMs += s.self_ms
    g.err += s.err
    g.stages.push(s)
    byGroup.set(s.group, g)
  }
  const out = [...byGroup.values()]
  for (const g of out) g.share = total > 0 ? g.selfMs / total : 0
  out.sort((a, b) => {
    const ai = GROUP_ORDER.indexOf(a.group)
    const bi = GROUP_ORDER.indexOf(b.group)
    return (ai === -1 ? 99 : ai) - (bi === -1 ? 99 : bi)
  })
  return out
}

function groupMeta(group: string) {
  return GROUP_META[group] ?? { label: group, bar: 'bg-gray-400', dot: 'bg-gray-400' }
}

/**
 * Human labels per stage slug. Explicit rather than de-underscoring the slug,
 * which gives "Ai step" and "Predict llm".
 * Unknown slugs fall back to the derived form so a stage added server-side
 * still renders readably before the next frontend deploy.
 */
const STAGE_LABELS: Record<string, string> = {
  'ingest.upload_s3': 'Upload to storage',
  'ingest.parse': 'Parse file',
  'ingest.db_insert': 'Insert segments',
  'ingest.wordcount': 'Word count',
  'ingest.job_create': 'Create jobs',

  'tm.resolve_assignments': 'Resolve assignments',
  'tm.exact_lookup': 'Exact lookup',
  'tm.fuzzy_lookup': 'Fuzzy lookup',
  'tm.context_match': 'Context match',
  'tm.apply': 'Apply matches',

  'mt.resolve_engine': 'Resolve engine',
  'mt.engine_call': 'Engine call',
  'mt.tag_transfer': 'Tag transfer',
  'mt.db_save': 'Save translations',

  'ai.predict_extension': 'Extension predict',
  'ai.predict_llm': 'LLM predict',
  'ai.route_apply': 'Route + apply',

  'feedback.tm_writeback': 'TM write-back',

  'qa.spellcheck': 'Spell check',

  // These carry SELF time, which for a whole-task span is the work inside the
  // task that no stage claimed. Labelled as overhead so nobody reads a small
  // number as "the pipeline only took 300ms".
  'task.translate': 'Translate overhead',
  'task.ai_step': 'QE+APE overhead',
  'task.pipeline': 'Pipeline overhead',
}

function stageLabel(slug: string): string {
  const known = STAGE_LABELS[slug]
  if (known) return known
  const rest = slug.includes('.') ? slug.slice(slug.indexOf('.') + 1) : slug
  const words = rest.replace(/_/g, ' ')
  return words.charAt(0).toUpperCase() + words.slice(1)
}

/** Engine/model tags worth surfacing next to a stage name. */
function metaChips(meta?: Record<string, string>): string[] {
  if (!meta) return []
  return Object.entries(meta)
    .filter(([k]) => k === 'engine' || k === 'model' || k === 'format' || k === 'source')
    .map(([, v]) => v)
}

function Tile({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="rounded-lg border border-gray-200 dark:border-gray-700 px-3 py-2">
      <div className="text-xs uppercase tracking-wide text-gray-500 dark:text-gray-400">{label}</div>
      <div className="text-lg font-semibold tabular-nums">{value}</div>
      {hint && <div className="text-xs text-gray-500 dark:text-gray-400">{hint}</div>}
    </div>
  )
}

/** One stage row: proportional bar, count, total, avg, max, error badge. */
function StageRow({ entry }: { entry: StageEntry }) {
  const meta = groupMeta(entry.group)
  const chips = metaChips(entry.meta)
  return (
    <div className="py-1.5" data-testid={`stage-row-${entry.stage}`}>
      <div className="flex items-baseline justify-between gap-3 text-sm">
        <div className="flex min-w-0 items-baseline gap-2">
          <span className={`inline-block h-2 w-2 shrink-0 rounded-full ${meta.dot}`} aria-hidden />
          <span className="font-medium">{stageLabel(entry.stage)}</span>
          {/* The task.* labels already say "task" — the group chip would just
              repeat them. */}
          {entry.group !== 'task' && (
            <span className="shrink-0 text-xs text-gray-500 dark:text-gray-400">{meta.label}</span>
          )}
          {entry.err > 0 && (
            <Chip color="red" className="shrink-0">
              {formatCount(entry.err)} failed
            </Chip>
          )}
        </div>
        <div className="shrink-0 tabular-nums font-semibold">{formatDurationMs(entry.self_ms)}</div>
      </div>

      <div className="mt-1 flex items-center gap-2">
        <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
          <div
            className={`h-full rounded-full ${meta.bar}`}
            style={{ width: `${Math.max(entry.share * 100, 1)}%` }}
          />
        </div>
        <div className="shrink-0 text-xs tabular-nums text-gray-500 dark:text-gray-400">
          {formatShare(entry.share)}
        </div>
      </div>

      <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
        <span className="tabular-nums">
          {formatCount(entry.n)} × · avg {formatDurationMs(entry.avg_ms)} · slowest{' '}
          {formatDurationMs(entry.max_ms)}
        </span>
        {/* Only spans that contain other spans have total > self. Showing the
            inclusive figure there keeps "the whole save took 2.2s" available
            without letting it into the sum. */}
        {entry.total_ms > entry.self_ms && (
          <span className="tabular-nums">
            · {formatDurationMs(entry.total_ms)} incl. nested
          </span>
        )}
        {/* Engine/model tags live here rather than on the title line so a long
            model name can wrap instead of truncating the stage name. */}
        {chips.map(c => (
          <Chip key={c} size="small" className="whitespace-normal wrap-anywhere">
            {c}
          </Chip>
        ))}
      </div>
    </div>
  )
}

/** A group's bar, expandable to the stages inside it. */
function GroupRow({ entry }: { entry: StageGroup }) {
  const [expanded, setExpanded] = useState(false)
  const meta = groupMeta(entry.group)
  return (
    <div className="py-1">
      {/* eslint-disable-next-line no-restricted-syntax -- whole-row expander (stage group), not an action button */}
      <button
        type="button"
        onClick={() => setExpanded(v => !v)}
        className="w-full text-left"
        aria-expanded={expanded}
        data-testid={`group-row-${entry.group}`}
      >
        <div className="flex items-baseline justify-between gap-3 text-sm">
          <div className="flex items-baseline gap-2">
            <span className="w-3 shrink-0 text-xs text-gray-400" aria-hidden>
              {expanded ? '▾' : '▸'}
            </span>
            <span className={`inline-block h-2 w-2 shrink-0 rounded-full ${meta.dot}`} aria-hidden />
            <span className="font-medium">{meta.label}</span>
            <span className="text-xs text-gray-500 dark:text-gray-400">
              {entry.stages.length} {entry.stages.length === 1 ? 'stage' : 'stages'}
            </span>
            {entry.err > 0 && (
              <Chip color="red" className="shrink-0">
                {formatCount(entry.err)} failed
              </Chip>
            )}
          </div>
          <div className="shrink-0 tabular-nums font-semibold">{formatDurationMs(entry.selfMs)}</div>
        </div>
        <div className="mt-1 flex items-center gap-2">
          <div className="h-2 flex-1 overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
            <div
              className={`h-full rounded-full ${meta.bar}`}
              style={{ width: `${Math.max(entry.share * 100, 1)}%` }}
            />
          </div>
          <div className="w-10 shrink-0 text-right text-xs tabular-nums text-gray-500 dark:text-gray-400">
            {formatShare(entry.share)}
          </div>
        </div>
      </button>

      {expanded && (
        <div className="mt-1 pl-5">
          {entry.stages.map(s => (
            <StageRow key={s.stage} entry={s} />
          ))}
        </div>
      )}
    </div>
  )
}

function JobRow({ job }: { job: ProjectTimings['jobs'][number] }) {
  const [expanded, setExpanded] = useState(false)
  return (
    <div className="border-b border-gray-100 last:border-0 dark:border-gray-800">
      {/* eslint-disable-next-line no-restricted-syntax -- whole-row expander (job), not an action button */}
      <button
        type="button"
        onClick={() => setExpanded(v => !v)}
        className="flex w-full items-center justify-between gap-3 py-2 text-left hover:bg-gray-50 dark:hover:bg-gray-800/50"
        aria-expanded={expanded}
        data-testid={`timing-job-${job.job_id}`}
      >
        <div className="flex min-w-0 items-baseline gap-2">
          <span className="text-gray-400" aria-hidden>{expanded ? '▾' : '▸'}</span>
          <span className="truncate text-sm font-medium">{job.file_name}</span>
          <Chip className="shrink-0 px-1.5 font-normal uppercase">{job.target_language}</Chip>
          {job.runs > 1 && (
            <span className="shrink-0 text-xs text-gray-500 dark:text-gray-400">
              {job.runs} runs
            </span>
          )}
        </div>
        <div className="flex shrink-0 items-baseline gap-3 text-xs tabular-nums text-gray-500 dark:text-gray-400">
          {job.queue_wait_ms > 0 && <span>queued {formatDurationMs(job.queue_wait_ms)}</span>}
          <span className="text-sm font-semibold text-gray-900 dark:text-gray-100">
            {formatDurationMs(job.compute_ms)}
          </span>
        </div>
      </button>

      {expanded && (
        <div className="pb-2 pl-6 pr-1">
          {job.stages.length === 0 ? (
            <p className="py-2 text-xs text-gray-500 dark:text-gray-400">
              No stage detail recorded for this job.
            </p>
          ) : (
            job.stages.map(s => <StageRow key={s.stage} entry={s} />)
          )}
        </div>
      )}
    </div>
  )
}

export default function ProcessingTimeDialog({ projectId, open, onOpenChange }: Props) {
  const { data, isLoading, isError, error, refetch } = useQuery({
    queryKey: queryKeys.projectTimings.byProject(projectId),
    queryFn: () => getProjectTimings(projectId),
    // Timings change as the worker flushes, and nothing in the UI mutates
    // them, so there's no invalidation path — refetch whenever the panel
    // opens instead of serving a cached snapshot from an hour ago.
    enabled: open,
    refetchOnMount: 'always',
  })

  // Computed rather than read off jobs[0]: the API happens to sort by compute
  // descending today, but a panel figure shouldn't silently depend on an
  // ORDER BY clause it doesn't control.
  const slowestJobMs = data ? data.jobs.reduce((m, j) => Math.max(m, j.compute_ms), 0) : 0

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {/* Width is stated twice, unprefixed AND `sm:`, on purpose.
          DialogContent hardcodes TWO competing caps — `max-w-[calc(100%-2rem)]`
          and `sm:max-w-sm` — and tailwind-merge keys on modifier+property, so
          it only drops the one whose modifier matches what we pass. Overriding
          just one leaves the other to win: an unprefixed-only override loses
          to `sm:max-w-sm` (384px), and an `sm:`-only override leaves the
          unprefixed calc in charge, which goes full-bleed on a wide monitor.
          Passing both replaces both.
          Wide because each stage row carries a name, a bar, a percentage and a
          "29 × · avg 2.8s · slowest 4.1s" stat line plus engine chips. */}
      <DialogContent
        className="max-w-[min(64rem,calc(100%-2rem))] sm:max-w-[min(64rem,calc(100%-2rem))] max-h-[85vh] overflow-y-auto"
        data-testid="processing-time-dialog"
      >
        <DialogHeader>
          <DialogTitle>Processing time</DialogTitle>
          <DialogDescription>
            Machine time spent on this project's jobs, accumulated across every run —
            re-running pre-translation adds to these figures rather than replacing them.
            Jobs run in parallel, so this is time the machines spent working, not
            clock time you waited.
          </DialogDescription>
        </DialogHeader>

        {isLoading && (
          <p className="py-8 text-center text-sm text-gray-500 dark:text-gray-400">Loading…</p>
        )}

        {isError && !data && (
          <LoadFailed what="processing times" error={error} onRetry={() => refetch()} />
        )}

        {data && data.job_count === 0 && (
          <p className="py-8 text-center text-sm text-gray-500 dark:text-gray-400" data-testid="timings-empty">
            No processing recorded yet. Timings appear once a job runs
            pre-translation, a QE+APE step, or a document spell check.
          </p>
        )}

        {data && data.job_count > 0 && (
          <div className="space-y-6">
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
              {/* Average, not the sum. Jobs run concurrently — a multi-language
                  upload dispatches one task per target language and the worker
                  runs several at once — so a sum overstates wall time several-
                  fold. The mean is a real per-job figure, and the slowest job
                  is the closer proxy for what someone actually waited. */}
              <Tile
                label="Avg per job"
                value={formatDurationMs(data.total_compute_ms / data.job_count)}
                hint={slowestJobMs > 0 ? `slowest ${formatDurationMs(slowestJobMs)}` : undefined}
              />
              <Tile
                label="Queue wait"
                value={formatDurationMs(data.total_queue_wait_ms / data.job_count)}
                hint="avg, enqueue → pickup"
              />
              <Tile label="Jobs tracked" value={formatCount(data.job_count)} />
              <Tile
                label="Words"
                value={formatCount(data.total_words)}
                hint={
                  data.total_words > 0
                    ? `${formatDurationMs(data.total_compute_ms / data.total_words)} / word`
                    : undefined
                }
              />
            </div>

            <section>
              <h3 className="mb-1 text-sm font-semibold">Where the time goes</h3>
              <p className="mb-2 text-xs text-gray-500 dark:text-gray-400">
                By phase, across all jobs. Each figure is the time that phase
                alone is responsible for, so nothing is counted twice and the
                percentages add up to the whole. Select a phase for its stages.
              </p>
              <div className="divide-y divide-gray-100 dark:divide-gray-800">
                {groupStages(data.stages).map(g => (
                  <GroupRow key={g.group} entry={g} />
                ))}
              </div>
            </section>

            <section>
              <h3 className="mb-1 text-sm font-semibold">By job</h3>
              <p className="mb-2 text-xs text-gray-500 dark:text-gray-400">
                Slowest first. Select a job for its stage breakdown.
              </p>
              <div>
                {data.jobs.map(j => (
                  <JobRow key={j.job_id} job={j} />
                ))}
              </div>
            </section>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
