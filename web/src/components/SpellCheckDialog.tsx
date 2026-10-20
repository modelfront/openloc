import { errorText } from '@/lib/error-text'
import { useState, useRef, useCallback, useEffect, useMemo } from 'react'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from '@/components/ui/dialog'
import { Progress } from '@/components/ui/progress'
import { Button } from '@/components/ui/button'
import { toast } from 'sonner'
import { runSpellcheck, type SpellError } from '../lib/spellcheck-run'
import {
  resolveRunScope, scopeIncludesLocked, allowedRunScope, DEFAULT_RUN_SCOPE,
  type SpellRunScope, type SpellFixActions,
} from '../lib/spellcheck-scope'
import { count } from '../lib/plural'
import SpellFixFields from './SpellFixFields'
import { checkJobSpelling } from '../api'

/**
 * Spell check review dialog — walk each finding, one at a time.
 *
 * Form factor is a blocking modal (Word's "Spelling" dialog), chosen over the
 * Find & Replace-style bar. A modal hides the segment grid, so the source text
 * is shown INSIDE the dialog: deciding between `morgen` and `Morgan` needs the
 * source, and it's the column the modal covers.
 *
 * Applying a fix is delegated upward via `onApply` / `onApplyAll` because the
 * write path differs by segment: the row currently open in an editor must be
 * corrected through that editor so its draft stays coherent, while closed rows
 * go through the bulk draft endpoint. CATEditor owns both.
 *
 * Locked segments are skipped unless the user picks "All segments (includes
 * locked)"; that scope asks the server to check and write them. It is offered
 * only to owners and admins — the roles the server lets write through a lock —
 * and stays in place, disabled with the reason, for everyone else.
 */

type Phase = 'scope' | 'running' | 'review' | 'done'

const EMPTY_SELECTION: ReadonlySet<number> = new Set()

export interface SpellCheckDialogProps {
  open: boolean
  onClose: () => void
  jobId: number
  /**
   * Word-level actions shared with the inline fix panel. The dictionary write
   * goes through here rather than being called directly, so both surfaces
   * produce the same toast and error handling; `canWriteLocked` decides whether
   * the all-segments scope is offered. Absent, nothing writes a locked row.
   */
  spellFixActions?: SpellFixActions
  /** Segment ids in document order (unfiltered). */
  segmentIds: number[]
  /** Indices the filter bar currently shows. */
  filteredIndices: number[]
  /** segment_id → translation row. */
  translationBySegment: Map<number, { id: number; status: string; locked?: boolean }>
  /** Plain text of a translation — the string offsets are measured against. */
  textFor: (translationId: number) => string
  /**
   * Source text for a segment. Required because a modal covers the grid, so the
   * source has to be reproduced inside the dialog for the decision to be
   * makeable at all.
   */
  sourceTextFor: (segmentId: number) => string
  /** True when the filter bar has an active filter (enables the third scope). */
  filterActive: boolean
  /** The editor's segment selection on screen (translation ids); enables the
   *  "Selected segments" scope. */
  selectedIds?: ReadonlySet<number>
  /** The scope chosen when the dialog opens. Default: non-locked segments. */
  initialScope?: SpellRunScope
  /** Flush any pending editor draft before reading or writing text. */
  beforeRun: () => Promise<void>
  /** Bring a segment into view behind the modal as the user navigates. */
  onNavigate: (translationId: number) => void
  /** Apply one correction; `includeLocked` lets it write a locked segment.
   *  Resolves false if it couldn't be applied. */
  onApply: (error: SpellError, replacement: string, includeLocked: boolean) => Promise<boolean>
  /** Apply every listed correction at once. Resolves the number applied. */
  onApplyAll: (errors: SpellError[], replacement: string, includeLocked: boolean) => Promise<number>
  /** Review only: the task cannot be edited, so the findings offer no fix. */
  readOnly?: boolean
}

export default function SpellCheckDialog(props: SpellCheckDialogProps) {
  const {
    open, onClose, jobId, spellFixActions, segmentIds, filteredIndices,
    translationBySegment, textFor, sourceTextFor, filterActive, beforeRun,
    onNavigate, onApply, onApplyAll, readOnly = false,
    selectedIds = EMPTY_SELECTION, initialScope = DEFAULT_RUN_SCOPE,
  } = props

  const canWriteLocked = spellFixActions?.canWriteLocked ?? false
  const [phase, setPhase] = useState<Phase>('scope')
  const [scope, setScope] = useState<SpellRunScope>(() => allowedRunScope(initialScope, canWriteLocked))
  // The scope only changes in the scope phase, so it is the running check's.
  const includeLocked = scopeIncludesLocked(scope)
  const lockedCount = useMemo(
    () => segmentIds.filter(id => translationBySegment.get(id)?.locked).length,
    [segmentIds, translationBySegment],
  )
  const [progress, setProgress] = useState({ done: 0, total: 0 })
  const [errors, setErrors] = useState<SpellError[]>([])
  const [index, setIndex] = useState(0)
  const [failed, setFailed] = useState(0)
  const [busy, setBusy] = useState(false)

  // Session-scoped ignores, on top of the persisted project dictionary. "Ignore
  // once" only advances; this set is what "Ignore all" adds to immediately so
  // the rest of the run reflects the decision without waiting on a round-trip.
  const ignoredWords = useRef<Set<string>>(new Set())
  const abortRef = useRef<AbortController | null>(null)

  const reset = useCallback(() => {
    abortRef.current?.abort()
    abortRef.current = null
    setPhase('scope')
    setScope(allowedRunScope(initialScope, canWriteLocked))
    setProgress({ done: 0, total: 0 })
    setErrors([])
    setIndex(0)
    setFailed(0)
    setBusy(false)
    ignoredWords.current = new Set()
  }, [initialScope, canWriteLocked])

  useEffect(() => {
    if (open) reset()
  }, [open, reset])

  const handleClose = () => {
    abortRef.current?.abort()
    onClose()
  }

  const start = async () => {
    // Any unsaved editor draft has to land first, or we'd check stale text and
    // then write corrections that the autosave overwrites a moment later.
    await beforeRun()

    const ids = resolveRunScope(scope, filteredIndices, segmentIds, translationBySegment, selectedIds)
    if (ids.length === 0) {
      toast.info('No segments to check in that scope')
      return
    }

    const controller = new AbortController()
    abortRef.current = controller
    setPhase('running')
    setProgress({ done: 0, total: ids.length })

    const segmentIndexByTranslation = new Map<number, number>()
    const segmentIdByTranslation = new Map<number, number>()
    segmentIds.forEach((segmentId, i) => {
      const t = translationBySegment.get(segmentId)
      if (!t) return
      segmentIndexByTranslation.set(t.id, i)
      segmentIdByTranslation.set(t.id, segmentId)
    })

    try {
      const result = await runSpellcheck({
        jobId,
        translationIds: ids,
        textFor,
        ignoredWords: ignoredWords.current,
        check: (job, chunkIds, signal) => checkJobSpelling(job, chunkIds, signal, { includeLocked }),
        segmentIdFor: id => segmentIdByTranslation.get(id) ?? 0,
        segmentIndexFor: id => segmentIndexByTranslation.get(id) ?? -1,
        signal: controller.signal,
        onProgress: (done, total) => setProgress({ done, total }),
      })

      if (result.aborted) {
        setPhase('scope')
        return
      }

      setErrors(result.errors)
      setFailed(result.failed)
      setIndex(0)
      setPhase(result.errors.length > 0 ? 'review' : 'done')
      if (result.errors.length > 0) onNavigate(result.errors[0].translationId)
    } catch (err) {
      // Surface the failure; a silent failure reads as a broken feature.
      setPhase('scope')
      toast.error(errorText(err, 'Spell check failed'))
    }
  }

  const current = errors[index]

  const [replacement, setReplacement] = useState('')
  useEffect(() => {
    setReplacement(current?.suggestions[0] ?? '')
  }, [current])

  const advance = useCallback((from: number, remaining: SpellError[]) => {
    if (remaining.length === 0) {
      setPhase('done')
      return
    }
    const next = Math.min(from, remaining.length - 1)
    setIndex(next)
    onNavigate(remaining[next].translationId)
  }, [onNavigate])

  const dropErrors = (predicate: (e: SpellError) => boolean, from: number) => {
    setErrors(prev => {
      const remaining = prev.filter(e => !predicate(e))
      advance(from, remaining)
      return remaining
    })
  }

  const ignoreOnce = () => {
    if (index + 1 >= errors.length) {
      setPhase('done')
      return
    }
    setIndex(index + 1)
    onNavigate(errors[index + 1].translationId)
  }

  const ignoreAll = async () => {
    if (!current) return
    const word = current.word
    ignoredWords.current.add(word)
    dropErrors(e => e.word === word, index)

    // Persisted per project + language so the decision outlives this run.
    await spellFixActions?.ignoreInProject(word)
  }

  const change = async (replacement: string) => {
    if (!current) return
    setBusy(true)
    try {
      const ok = await onApply(current, replacement, includeLocked)
      if (!ok) {
        toast.error('Could not apply that correction to this segment')
        return
      }
      dropErrors(e => e === current, index)
    } finally {
      setBusy(false)
    }
  }

  const changeAll = async (replacement: string) => {
    if (!current) return
    const word = current.word
    const matching = errors.filter(e => e.word === word)
    setBusy(true)
    try {
      const applied = await onApplyAll(matching, replacement, includeLocked)
      toast.success(`Replaced ${applied} of ${matching.length} occurrence${matching.length === 1 ? '' : 's'}`)
      dropErrors(e => e.word === word, index)
    } finally {
      setBusy(false)
    }
  }

  const occurrences = current ? errors.filter(e => e.word === current.word).length : 0
  const fix = replacement.trim()

  return (
    <Dialog open={open} onOpenChange={v => !v && handleClose()}>
      <DialogContent draggable className="sm:max-w-xl" data-testid="spellcheck-dialog">
        <DialogHeader>
          <DialogTitle>Spell check</DialogTitle>
          <DialogDescription>
            {phase === 'scope' && 'Choose which segments to check.'}
            {phase === 'running' && 'Checking segments…'}
            {phase === 'review' && `Error ${index + 1} of ${errors.length}`}
            {phase === 'done' && 'Nothing left to review.'}
          </DialogDescription>
        </DialogHeader>

        {phase === 'scope' && (
          <div className="space-y-3">
            <div className="space-y-2" role="radiogroup" aria-label="Check scope">
              <ScopeOption
                value="nonlocked" current={scope} onChange={setScope}
                label="Non-locked segments"
                hint={count(segmentIds.length - lockedCount, 'segment')}
              />
              <ScopeOption
                value="all" current={scope} onChange={setScope}
                label="All segments (includes locked)"
                hint={canWriteLocked
                  ? `${count(segmentIds.length, 'segment')}${lockedCount > 0 ? `, ${lockedCount} locked` : ''}`
                  : 'Owners and admins only'}
                disabled={!canWriteLocked}
              />
              <ScopeOption
                value="filtered" current={scope} onChange={setScope}
                label="Matches current filter"
                hint={filterActive
                  ? `${count(filteredIndices.length, 'segment')} visible · locked ones skipped`
                  : 'No filter active'}
                disabled={!filterActive}
              />
              <ScopeOption
                value="selected" current={scope} onChange={setScope}
                label="Selected segments"
                hint={selectedIds.size > 0
                  ? `${count(selectedIds.size, 'segment')} selected · locked ones skipped`
                  : 'No segments selected'}
                disabled={selectedIds.size === 0}
              />
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <Button variant="outline" size="sm" onClick={handleClose}>Cancel</Button>
              <Button size="sm" onClick={start} data-testid="spellcheck-start">Check spelling</Button>
            </div>
          </div>
        )}

        {phase === 'running' && (
          <div className="space-y-3">
            <Progress
              value={progress.total ? (progress.done / progress.total) * 100 : 0}
              aria-label="Spell check progress"
              aria-valuetext={`${progress.done} of ${progress.total} segments`}
            />
            <p className="text-xs text-gray-500 dark:text-gray-400 tabular-nums">
              {progress.done} of {progress.total} segments
            </p>
            <div className="flex justify-end">
              <Button variant="outline" size="sm" onClick={() => abortRef.current?.abort()}>
                Cancel
              </Button>
            </div>
          </div>
        )}

        {phase === 'review' && current && (
          <div className="space-y-3">
            <SpellFixFields
              sourceText={current.segmentId ? sourceTextFor(current.segmentId) : undefined}
              targetText={textFor(current.translationId)}
              offset={current.offset}
              length={current.length}
              message={current.message}
              suggestions={current.suggestions}
              replacement={replacement}
              onReplacementChange={setReplacement}
              disabled={busy || readOnly}
            />

            <div className="grid grid-cols-2 gap-2 pt-1">
              <Button variant="outline" size="sm" onClick={ignoreOnce} disabled={busy}>
                Ignore once
              </Button>
              {!readOnly && (
                <Button
                  size="sm" disabled={busy || !fix}
                  onClick={() => change(fix)}
                  data-testid="spellcheck-change"
                >
                  Change
                </Button>
              )}
              <Button variant="outline" size="sm" onClick={ignoreAll} disabled={busy}>
                Ignore all{occurrences > 1 ? ` (${occurrences})` : ''}
              </Button>
              {!readOnly && (
                <Button
                  variant="outline" size="sm" disabled={busy || !fix}
                  onClick={() => changeAll(fix)}
                  data-testid="spellcheck-change-all"
                >
                  Change all{occurrences > 1 ? ` (${occurrences})` : ''}
                </Button>
              )}
            </div>

            <FootNotes failed={failed} />
          </div>
        )}

        {phase === 'done' && (
          <div className="space-y-3">
            <p className="text-sm text-gray-600 dark:text-gray-400">
              {errors.length === 0 && progress.total > 0
                ? `No spelling issues found in ${progress.total} segment${progress.total === 1 ? '' : 's'}.`
                : 'All findings reviewed.'}
            </p>
            <FootNotes failed={failed} />
            <div className="flex justify-end gap-2">
              <Button variant="outline" size="sm" onClick={() => setPhase('scope')}>Check again</Button>
              <Button size="sm" onClick={handleClose}>Done</Button>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}


/**
 * Unchecked segments are reported so a partial run never reads as clean.
 */
function FootNotes({ failed }: { failed: number }) {
  if (failed === 0) return null
  return (
    <div className="text-xs text-amber-700 dark:text-amber-400 space-y-0.5" data-testid="spellcheck-notes">
      <p>{failed} segment{failed === 1 ? '' : 's'} could not be checked.</p>
    </div>
  )
}

function ScopeOption({
  value, current, onChange, label, hint, disabled,
}: {
  value: SpellRunScope
  current: SpellRunScope
  onChange: (v: SpellRunScope) => void
  label: string
  hint: string
  disabled?: boolean
}) {
  return (
    <label
      className={`flex items-start gap-2 rounded border px-3 py-2 cursor-pointer ${
        current === value
          ? 'border-primary bg-primary/10'
          : 'border-gray-200 dark:border-gray-700'
      } ${disabled ? 'opacity-50 cursor-not-allowed' : ''}`}
    >
      <input
        type="radio" name="spellcheck-scope" value={value}
        checked={current === value} disabled={disabled}
        onChange={() => onChange(value)}
        className="mt-0.5 accent-primary"
      />
      <span>
        <span className="block text-sm">{label}</span>
        <span className="block text-xs text-gray-500 dark:text-gray-400">{hint}</span>
      </span>
    </label>
  )
}
