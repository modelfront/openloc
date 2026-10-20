import { useState } from 'react'
import { useOnChange } from '@/hooks/useOnChange'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { tmFindReplaceFind, tmFindReplaceApply, type TMFRScope, type TMFRFindResult } from '../api'
import { invalidateTMContext } from '../lib/query-invalidation'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import ConfirmDialog from './ConfirmDialog'
import { ChoiceToggle } from '@/components/ui/choice-toggle'

interface Props {
  open: boolean
  onClose: () => void
  tmId: number
  /** Optional language-pair scope (drives source_language/target_language filters). */
  sourceLanguage?: string
  targetLanguage?: string
}

/**
 * Bulk Find & Replace across a TM. Two-step UX:
 *   1. User types Find / Replace + chooses scope + case sensitivity.
 *   2. Click "Find" → backend returns match count (preview mode, NO mutation).
 *   3. Click "Replace all" → ConfirmDialog → backend applies in one tx.
 *
 * Preview before apply: bulk TM edits are irreversible without history
 * rescue, so the user sees what they're about to do BEFORE committing.
 */
export default function TMBulkFindReplaceModal({ open, onClose, tmId, sourceLanguage, targetLanguage }: Props) {
  const queryClient = useQueryClient()

  const [find, setFind] = useState('')
  const [replace, setReplace] = useState('')
  const [caseSensitive, setCaseSensitive] = useState(false)
  const [scope, setScope] = useState<TMFRScope>('both')
  const [preview, setPreview] = useState<TMFRFindResult | null>(null)
  const [confirmOpen, setConfirmOpen] = useState(false)

  // Reset state when the dialog opens or closes — stale state across opens is
  // confusing (user thinks "Replace all" still refers to last run's preview).
  useOnChange([open], () => {
    if (!open) {
      setFind('')
      setReplace('')
      setCaseSensitive(false)
      setScope('both')
      setPreview(null)
      setConfirmOpen(false)
    }
  })

  // The preview must invalidate when ANY input changes — otherwise the user
  // sees a stale match count and clicks "Replace all" with the WRONG query.
  useOnChange([find, replace, caseSensitive, scope], () => setPreview(null))

  // The preview count is the result; only "no matches" gets a notice.
  const findMut = useMutationWithToast({
    mutationFn: () => tmFindReplaceFind(tmId, { find, replace, caseSensitive, scope, sourceLanguage, targetLanguage }),
    successMessage: () => null,
    errorMessage: 'Find failed',
    onSuccess: (data) => {
      setPreview(data)
      if (data.total_entries === 0) {
        toast.info('No matches found')
      }
    },
  })

  const applyMut = useMutationWithToast({
    mutationFn: () => tmFindReplaceApply(tmId, { find, replace, caseSensitive, scope, sourceLanguage, targetLanguage }),
    successMessage: data =>
      `Replaced ${data.total_replacements} occurrence${data.total_replacements !== 1 ? 's' : ''} ` +
      `in ${data.entries_updated} entr${data.entries_updated !== 1 ? 'ies' : 'y'}`,
    errorMessage: 'Replace failed',
    onSuccess: () => {
      invalidateTMContext(queryClient, tmId)
      onClose()
    },
  })

  const onFindClick = () => {
    if (!find.trim()) return
    findMut.mutate()
  }
  const onReplaceAllClick = () => {
    if (!find.trim()) return
    if (!preview) {
      // Force a Find pass first so the user sees what they're about to do.
      // Only opens the confirm if there are matches; the "no matches" toast
      // is already handled by `findMut.onSuccess` so we don't duplicate it.
      findMut.mutate(undefined, {
        onSuccess: (data) => {
          if (data.total_entries > 0) setConfirmOpen(true)
        },
      })
      return
    }
    if (preview.total_entries === 0) {
      toast.info('No matches to replace')
      return
    }
    setConfirmOpen(true)
  }

  return (
    <>
      <Dialog open={open} onOpenChange={v => !v && onClose()}>
        <DialogContent draggable className="sm:max-w-xl" data-testid="tm-find-replace-dialog">
          <DialogHeader>
            <DialogTitle>Find &amp; replace in TM</DialogTitle>
            <DialogDescription>
              Bulk-replace text across this TM's entries. Each match is captured in the entry's
              edit history, and the entire batch is applied atomically.
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-3 py-2">
            <div>
              <label className="text-xs font-medium text-gray-700 dark:text-gray-300 mb-1 block">Find</label>
              <Input
                value={find}
                onChange={e => setFind(e.target.value)}
                placeholder="Text to find"
                autoFocus
                data-testid="tm-fr-find"
              />
            </div>
            <div>
              <label className="text-xs font-medium text-gray-700 dark:text-gray-300 mb-1 block">Replace with</label>
              <Input
                value={replace}
                onChange={e => setReplace(e.target.value)}
                placeholder="Replacement text (leave blank to delete the find term)"
                data-testid="tm-fr-replace"
              />
            </div>

            <div className="flex items-center gap-4 text-xs">
              <CheckboxField size="compact" label="Case sensitive" checked={caseSensitive} onChange={setCaseSensitive} data-testid="tm-fr-case" />
              <div className="flex items-center gap-1.5">
                <span className="text-gray-500 dark:text-gray-400">Search in:</span>
                <ChoiceToggle
                  size="sm"
                  aria-label="Search in"
                  value={scope}
                  onChange={setScope}
                  options={[
                    { value: 'both', label: 'Both' },
                    { value: 'source', label: 'Source' },
                    { value: 'target', label: 'Target' },
                  ]}
                  data-testid="tm-fr-scope"
                />
              </div>
            </div>

            {/* Preview banner */}
            {preview && (
              <div
                className="px-3 py-2 rounded bg-primary/10 text-sm text-primary"
                data-testid="tm-fr-preview"
              >
                {preview.total_entries === 0
                  ? 'No matches found.'
                  : (
                    <>
                      <strong>{preview.total_replacements}</strong> occurrence
                      {preview.total_replacements !== 1 ? 's' : ''} in{' '}
                      <strong>{preview.total_entries}</strong> entr
                      {preview.total_entries !== 1 ? 'ies' : 'y'} will be replaced.
                    </>
                  )}
              </div>
            )}
          </div>

          <DialogFooter className="gap-2">
            <Button variant="outline" onClick={onClose} data-testid="tm-fr-cancel">Cancel</Button>
            <Button
              variant="outline"
              onClick={onFindClick}
              disabled={!find.trim() || findMut.isPending}
              data-testid="tm-fr-find-btn"
            >
              {findMut.isPending ? 'Finding…' : 'Find'}
            </Button>
            <Button
              onClick={onReplaceAllClick}
              disabled={!find.trim() || applyMut.isPending}
              data-testid="tm-fr-replace-all-btn"
            >
              {applyMut.isPending ? 'Replacing…' : 'Replace all'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={
          preview
            ? `Replace ${preview.total_replacements} occurrence${preview.total_replacements !== 1 ? 's' : ''}?`
            : 'Apply find and replace?'
        }
        description={
          preview
            ? `This will update ${preview.total_entries} entr${preview.total_entries !== 1 ? 'ies' : 'y'} ` +
              `in this TM. Each affected entry is captured in its edit history. ` +
              `The entire batch is applied as a single transaction — partial application can't happen, ` +
              `but you cannot undo this action other than editing the affected entries by hand.`
            : 'Apply find and replace across this TM.'
        }
        actionLabel="Replace all"
        onConfirm={() => applyMut.mutate()}
      />
    </>
  )
}
