import { useState, useEffect, useEffectEvent } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { findReplace, findMatches, replaceOne, type ApiError, type FindOptions } from '../api'
import { replaceSummary } from '../lib/find-replace'
import {
  findInJobs, replaceAllInJobs, replaceOneInJob, scopeReplaceSummary, type FindReplaceJobs, type ScopeReplaceResult,
} from '../lib/find-replace-scope'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { toast } from 'sonner'
import { errorText } from '@/lib/error-text'

const findReplaceSchema = z.object({
  findText: z.string().min(1, 'Find text is required'),
  replaceText: z.string(),
  caseSensitive: z.boolean(),
  includeLocked: z.boolean(),
  inSelection: z.boolean(),
})
type FindReplaceFormValues = z.infer<typeof findReplaceSchema>

interface Props {
  open: boolean
  onClose: () => void
  /** The editor's jobs: one, or the files opened together. */
  jobs: FindReplaceJobs
  onHighlight: (term: string | null) => void
  onScrollToTranslation: (translationId: number) => void
  onDeactivate: () => void
  /** A replace landed in at least one file. */
  onComplete: (result: ScopeReplaceResult) => void
  /** A job's task-edit guard refused a replace; without it the refusal is a plain error toast. */
  onTaskLocked?: (err: ApiError, jobId: number) => void
  /** Find only: the task cannot be edited, so the replace field and buttons are hidden. */
  readOnly?: boolean
  /** The editor's segment selection on screen (translation ids). When present,
   *  an "In selection" option limits find and replace to it. */
  selectedIds?: readonly number[]
  /** Whether "In selection" is ticked when the bar opens. */
  initialInSelection?: boolean
  /** Owner/admin: with "Include locked", replace writes locked segments too
   *  and unlocks them. Only changes the hint; the server enforces the rule. */
  canReplaceLocked?: boolean
}

/**
 * Find & Replace bar — compact top bar.
 * 4 buttons: Find (navigate next), Find All (count + highlight),
 * Replace (single + advance), Replace all (batch).
 */
export default function FindReplaceModal({
  open, onClose, jobs, onHighlight, onScrollToTranslation, onDeactivate, onComplete, onTaskLocked,
  readOnly = false, selectedIds = [], initialInSelection = false, canReplaceLocked = false,
}: Props) {
  const {
    register,
    getValues,
    reset,
    watch,
    setValue,
    formState: { isSubmitting },
  } = useForm<FindReplaceFormValues>({
    resolver: zodResolver(findReplaceSchema),
    defaultValues: { findText: '', replaceText: '', caseSensitive: false, includeLocked: false, inSelection: false },
  })
  const hasSelection = selectedIds.length > 0
  // Stays visible while ticked, even when the selection empties, so it can be unticked.
  const showInSelection = hasSelection || watch('inSelection')

  const [matchIds, setMatchIds] = useState<number[]>([])
  const [currentIdx, setCurrentIdx] = useState(-1)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (open) {
      reset({ findText: '', replaceText: '', caseSensitive: false, includeLocked: false, inSelection: initialInSelection })
      setMatchIds([])
      setCurrentIdx(-1)
    }
  }, [open, reset, initialInSelection])

  // The rows to work on: every row (undefined), or the selection. With "In
  // selection" ticked and nothing selected on screen it refuses (null)
  // rather than widen the scope to every row.
  const scopeIds = (): readonly number[] | undefined | null => {
    if (!getValues().inSelection) return undefined
    if (!hasSelection) {
      toast.warning('No selected segments on screen. Untick "In selection" to search every segment.')
      return null
    }
    return selectedIds
  }

  const findOptions = (): FindOptions => {
    const { caseSensitive, includeLocked } = getValues()
    return { caseSensitive, includeLocked }
  }

  const searchIds = async (findText: string): Promise<number[] | null> => {
    const ids = scopeIds()
    if (ids === null) return null
    return findInJobs(jobs, (jobId, part) => findMatches(jobId, findText, findOptions(), part).then(r => r.translation_ids ?? []), ids)
  }

  const handleClose = () => {
    setMatchIds([])
    setCurrentIdx(-1)
    onHighlight(null)
    onClose()
  }

  const closeOnEscape = useEffectEvent(handleClose)
  useEffect(() => {
    if (!open) return
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') { e.preventDefault(); closeOnEscape() }
    }
    document.addEventListener('keydown', handler)
    return () => document.removeEventListener('keydown', handler)
  }, [open])

  const clearMatches = () => {
    setMatchIds([])
    setCurrentIdx(-1)
    onHighlight(null)
  }

  // A search flag: changing it drops the matches found with the old value.
  const flag = (name: 'caseSensitive' | 'includeLocked' | 'inSelection') => ({
    checked: watch(name),
    onChange: (checked: boolean) => {
      setValue(name, checked)
      clearMatches()
    },
  })

  const replaceFailed = (err: unknown, fallback: string) => {
    toast.error((err instanceof Error && err.message) || fallback)
  }
  // Each file whose task refused the write says so; false when no file was
  // written, and then a file that failed for another reason says why too.
  const reportLocked = (out: ScopeReplaceResult) => {
    for (const { jobId, err } of out.locked) {
      if (onTaskLocked) onTaskLocked(err, jobId)
      else toast.error(err.message)
    }
    if (out.writtenJobs.length > 0) return true
    if (out.failed.length > 0) replaceFailed(out.failed[0], 'Replace failed')
    return false
  }

  const doFindAll = async () => {
    const { findText } = getValues()
    if (!findText.trim()) return
    setBusy(true)
    try {
      const ids = await searchIds(findText)
      if (!ids) return
      setMatchIds(ids)
      if (ids.length === 0) {
        toast.info('No matches found')
        setCurrentIdx(-1)
        onHighlight(null)
      } else {
        setCurrentIdx(-1) // Find All highlights only — no segment activation
        onHighlight(findText)
        onDeactivate() // Deselect any active segment so user sees highlights only
        // Blur the search input so user sees only highlights, not a blinking cursor
        if (document.activeElement instanceof HTMLElement) document.activeElement.blur()
      }
    } catch (err) {
      toast.error(errorText(err, 'Find failed'))
    } finally {
      setBusy(false)
    }
  }

  // --- Find (next): if no matches yet → Find All. Otherwise advance. ---
  const doFindNext = async () => {
    if (matchIds.length === 0) {
      const { findText } = getValues()
      if (!findText.trim()) return
      setBusy(true)
      try {
        const ids = await searchIds(findText)
        if (!ids) return
        setMatchIds(ids)
        if (ids.length === 0) {
          toast.info('No matches found')
          setCurrentIdx(-1)
          onHighlight(null)
        } else {
          setCurrentIdx(0)
          onHighlight(findText)
          onScrollToTranslation(ids[0])
        }
      } catch (err) {
        toast.error(errorText(err, 'Find failed'))
      } finally {
        setBusy(false)
      }
      return
    }
    const nextIdx = (currentIdx + 1) % matchIds.length
    setCurrentIdx(nextIdx)
    onScrollToTranslation(matchIds[nextIdx])
  }

  const doReplace = async () => {
    const { findText, replaceText } = getValues()
    if (!findText.trim()) return

    if (matchIds.length === 0) {
      await doFindAll()
      return
    }
    if (currentIdx < 0 || currentIdx >= matchIds.length) return

    setBusy(true)
    try {
      const targetId = matchIds[currentIdx]
      const out = await replaceOneInJob(jobs, targetId, (jobId, id) => replaceOne(jobId, findText, replaceText, findOptions(), id))
      if (!reportLocked(out)) return
      // A match left alone (locked, crosses a tag, changed meanwhile) says why.
      if (out.result.segments === 0) toast.warning(replaceSummary(out.result))

      const remaining = matchIds.filter(id => id !== targetId)
      setMatchIds(remaining)

      if (remaining.length > 0) {
        const newIdx = currentIdx >= remaining.length ? 0 : currentIdx
        setCurrentIdx(newIdx)
        onScrollToTranslation(remaining[newIdx])
      } else {
        setCurrentIdx(-1)
        onHighlight(null)
        toast.success('All matches replaced')
      }
      onComplete(out)
    } catch (err) {
      replaceFailed(err, 'Replace failed')
    } finally {
      setBusy(false)
    }
  }

  const doReplaceAll = async () => {
    const { findText, replaceText } = getValues()
    if (!findText.trim()) return
    setBusy(true)
    try {
      const ids = scopeIds()
      if (ids === null) return
      const out = await replaceAllInJobs(jobs, (jobId, part) => findReplace(jobId, findText, replaceText, findOptions(), part), ids)
      if (!reportLocked(out)) return
      const summary = scopeReplaceSummary(out)
      if (out.result.segments > 0 && out.locked.length + out.failed.length === 0) toast.success(summary)
      else toast.warning(summary)
      setMatchIds([])
      setCurrentIdx(-1)
      onHighlight(null)
      onComplete(out)
      onClose()
    } catch (err) {
      replaceFailed(err, 'Replace all failed')
    } finally {
      setBusy(false)
    }
  }

  if (!open) return null

  const matchCount = matchIds.length
  const positionLabel = matchCount > 0 && currentIdx >= 0
    ? `${currentIdx + 1}/${matchCount}`
    : matchCount > 0
      ? `${matchCount} match${matchCount !== 1 ? 'es' : ''}`
      : null

  return (
    <div className="sticky top-0 z-20 bg-card border-b border-gray-200 dark:border-gray-700 shadow-sm px-4 py-2">
      <div className="flex items-center gap-2 flex-wrap">
        <span className="text-xs font-medium text-gray-500 dark:text-gray-400 mr-1">{readOnly ? 'Find' : 'Find and replace'}</span>

        <Input
          {...register('findText', { onChange: clearMatches })}
          placeholder="Find..."
          autoFocus
          className="w-48 h-8 text-sm"
          onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); doFindNext() } }}
        />

        {!readOnly && (
          <Input
            {...register('replaceText')}
            placeholder="Replace..."
            className="w-48 h-8 text-sm"
            onKeyDown={(e) => { if (e.key === 'Enter' && currentIdx >= 0) { e.preventDefault(); doReplace() } }}
          />
        )}

        <CheckboxField size="compact" label="Aa" {...flag('caseSensitive')} />

        <Tooltip>
          <TooltipTrigger render={<span className="inline-flex" />}>
            <CheckboxField size="compact" label="Include locked" data-testid="find-include-locked" {...flag('includeLocked')} />
          </TooltipTrigger>
          <TooltipContent className="max-w-xs">
            {readOnly
              ? 'Also find locked segments.'
              : canReplaceLocked
                ? 'Also find locked segments. Replace changes them too and unlocks them.'
                : 'Also find locked segments. Only an owner or admin can replace text in them.'}
          </TooltipContent>
        </Tooltip>

        {showInSelection && (
          <CheckboxField
            size="compact"
            label={`In selection (${selectedIds.length})`}
            data-testid="find-in-selection"
            {...flag('inSelection')}
          />
        )}

        <Button type="button" variant="outline" size="sm" className="h-8 text-xs" onClick={doFindNext} disabled={busy}>
          Find
        </Button>
        <Button type="button" variant="outline" size="sm" className="h-8 text-xs" onClick={doFindAll} disabled={busy}>
          Find All
        </Button>
        {!readOnly && (
          <>
            <Button type="button" variant="outline" size="sm" className="h-8 text-xs" onClick={doReplace} disabled={busy || isSubmitting || currentIdx < 0}>
              Replace
            </Button>
            <Button type="button" size="sm" className="h-8 text-xs" onClick={doReplaceAll} disabled={busy || isSubmitting}>
              Replace all
            </Button>
          </>
        )}

        {positionLabel && (
          <span className="text-xs text-primary bg-primary/10 px-2 py-1 rounded tabular-nums" data-testid="match-count">
            {positionLabel}
          </span>
        )}

        <Button type="button" variant="ghost" size="sm" className="h-8 text-xs ml-auto" onClick={handleClose}>
          ✕
        </Button>
      </div>
    </div>
  )
}
