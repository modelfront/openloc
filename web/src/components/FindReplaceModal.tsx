import { useState, useEffect, useEffectEvent } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { findReplace, findMatches, replaceOne, type ApiError, type FindOptions, type ReplaceResult } from '../api'
import { replaceSummary, sumReplaceCounts } from '../lib/find-replace'
import { mapChunks } from '../lib/segment-bulk'
import { isTaskLockedError } from '../lib/task-edit'
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
  jobId: number
  onHighlight: (term: string | null) => void
  onScrollToTranslation: (translationId: number) => void
  onDeactivate: () => void
  /** A replace landed. */
  onComplete: (result: ReplaceResult) => void
  /** The task-edit guard refused a replace; without it the refusal is a plain error toast. */
  onTaskLocked?: (err: ApiError) => void
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
  open, onClose, jobId, onHighlight, onScrollToTranslation, onDeactivate, onComplete, onTaskLocked,
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

  // Runs fn on the whole job, or on the selection in chunks the server
  // accepts. With "In selection" ticked and nothing selected on screen it
  // refuses (null) rather than widen the scope to the whole job.
  const inScope = async <R,>(fn: (ids?: number[]) => Promise<R>): Promise<R[] | null> => {
    if (!getValues().inSelection) return [await fn(undefined)]
    if (!hasSelection) {
      toast.warning('No selected segments on screen. Untick "In selection" to search the whole job.')
      return null
    }
    return mapChunks(selectedIds, fn)
  }

  const findOptions = (): FindOptions => {
    const { caseSensitive, includeLocked } = getValues()
    return { caseSensitive, includeLocked }
  }

  const searchIds = async (findText: string): Promise<number[] | null> => {
    const results = await inScope(ids => findMatches(jobId, findText, findOptions(), ids))
    return results && results.flatMap(r => r.translation_ids ?? [])
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
    if (isTaskLockedError(err) && onTaskLocked) onTaskLocked(err)
    else toast.error((err instanceof Error && err.message) || fallback)
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
      const result = await replaceOne(jobId, findText, replaceText, findOptions(), targetId)
      // A match left alone (locked, crosses a tag, changed meanwhile) says why.
      if (result.segments === 0) toast.warning(replaceSummary(result))

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
      onComplete(result)
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
      const parts = await inScope(ids => findReplace(jobId, findText, replaceText, findOptions(), ids))
      if (!parts) return
      const result: ReplaceResult = { ...sumReplaceCounts(parts), taskReopened: parts.some(p => p.taskReopened) }
      if (result.segments > 0) toast.success(replaceSummary(result))
      else toast.warning(replaceSummary(result))
      setMatchIds([])
      setCurrentIdx(-1)
      onHighlight(null)
      onComplete(result)
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
