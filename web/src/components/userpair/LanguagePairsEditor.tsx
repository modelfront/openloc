// LanguagePairsEditor — modal for managing one user's language-pair qualifications.
//
// UI model: a list of ROWS. Each row multi-selects sources
// and multi-selects targets; the row expands to the cartesian product of its
// sources × targets. "Add languages" appends another row so different source/
// target groups can be entered. The full pair set is every row's product,
// deduped, with self-pairs (source === target) dropped.
//
// Replace semantics: server's PUT clears + bulk inserts atomically. The UI
// mirrors that — the user edits rows locally, then a single Save dispatches the
// full computed set. Cancel discards local changes.
//
// Server normalizes language codes (lowercase, region preserved e.g. en-us) and
// returns the canonical set; the dialog reloads from that on next open.

import { useMemo, useState } from 'react'
import { useOnChange } from '@/hooks/useOnChange'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { X, Plus } from 'lucide-react'
import {
  listLanguages,
  listUserLanguagePairs,
  replaceUserLanguagePairs,
  type LanguagePair,
} from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Chip } from '@/components/ui/chip'
import ConfirmDialog from '../ConfirmDialog'
import { MultiCombobox } from '@/components/ui/combobox'
import { langPickerProps, toLangOptions } from '../lang-options'
import { LoadFailed } from '@/components/layout/LoadFailed'

interface Props {
  userId: number
  userName: string
  open: boolean
  onOpenChange: (open: boolean) => void
}

const MAX_PAIRS = 100 // backend cap; mirror client-side for friendlier UX

// One editor row: multi-select sources × multi-select targets.
export interface PairRow {
  sources: string[]
  targets: string[]
}

const emptyRow = (): PairRow => ({ sources: [], targets: [] })

/**
 * Expand the rows into the flat pair set: each row is the cartesian product of
 * its sources × targets. Each pair is tagged with its row index (`row_group`)
 * so the grouping survives a save/reload. Skips empties and self-pairs
 * (source === target) and dedupes across all rows (first row wins, matching the
 * server's ON CONFLICT DO NOTHING). Exported for unit testing.
 */
export function pairsFromRows(rows: PairRow[]): LanguagePair[] {
  const seen = new Set<string>()
  const result: LanguagePair[] = []
  rows.forEach((row, rowIndex) => {
    for (const rawS of row.sources) {
      const s = rawS.trim()
      if (!s) continue
      for (const rawT of row.targets) {
        const t = rawT.trim()
        if (!t || t === s) continue
        const key = `${s}|${t}`
        if (seen.has(key)) continue
        seen.add(key)
        result.push({ source_lang: s, target_lang: t, row_group: rowIndex })
      }
    }
  })
  return result
}

/**
 * Seed editor rows from a flat pair set: group by `row_group` (the persisted
 * editor-row index), preserving group order and first-seen source/target order
 * within each group. Falls back to grouping legacy pairs (all row_group 0) into
 * one row. Always returns at least one (empty) row. Exported for unit testing.
 */
export function rowsFromPairs(pairs: LanguagePair[]): PairRow[] {
  const byGroup = new Map<number, PairRow>()
  for (const p of pairs) {
    const g = p.row_group ?? 0
    const row = byGroup.get(g) ?? emptyRow()
    if (!row.sources.includes(p.source_lang)) row.sources.push(p.source_lang)
    if (!row.targets.includes(p.target_lang)) row.targets.push(p.target_lang)
    byGroup.set(g, row)
  }
  const rows = [...byGroup.entries()].sort((a, b) => a[0] - b[0]).map(([, row]) => row)
  return rows.length > 0 ? rows : [emptyRow()]
}

/** One grouped language-pair row (`en, fr → es`) as a read-only chip. Shared
 *  by the editor's summary and the org members table so the two read alike. */
export function PairRowChip({ row }: { row: Pick<PairRow, 'sources' | 'targets'> }) {
  return (
    <Chip className="gap-1 px-1.5 font-mono font-normal">
      {row.sources.join(', ')}
      {/* Weight alone separates the arrow; the colour is the chip's. */}
      <span className="mx-0.5 font-semibold">→</span>
      {row.targets.join(', ')}
    </Chip>
  )
}

export default function LanguagePairsEditor({ userId, userName, open, onOpenChange }: Props) {
  const queryClient = useQueryClient()

  const { data: languages } = useQuery({
    queryKey: queryKeys.languages.all,
    queryFn: listLanguages,
  })

  const { data: serverPairs, isLoading, isError, error, refetch } = useQuery({
    queryKey: queryKeys.userLanguagePairs.byUser(userId),
    queryFn: () => listUserLanguagePairs(userId),
    enabled: open, // don't fetch until dialog opens
  })

  // Local working copy of rows; reset when dialog opens or server data arrives.
  const [rows, setRows] = useState<PairRow[]>(() => (open && serverPairs ? rowsFromPairs(serverPairs) : [emptyRow()]))
  useOnChange([open, serverPairs], () => {
    if (open && serverPairs) setRows(rowsFromPairs(serverPairs))
  })

  const langOptions = useMemo(
    () => (languages ?? []).map(l => ({ code: l.code, name: l.name })),
    [languages],
  )

  // The pairs that will be saved — derived from the rows.
  const pairs = useMemo(() => pairsFromRows(rows), [rows])

  const updateRow = (index: number, patch: Partial<PairRow>) =>
    setRows(prev => prev.map((r, i) => (i === index ? { ...r, ...patch } : r)))
  const addRow = () => setRows(prev => [...prev, emptyRow()])
  const removeRow = (index: number) =>
    setRows(prev => {
      const next = prev.filter((_, i) => i !== index)
      return next.length > 0 ? next : [emptyRow()] // always keep one editable row
    })

  // Guard against accidental wipe: if the rows now yield no pairs but the server
  // originally had some, require an explicit confirmation before the PUT.
  const wouldWipeAll = pairs.length === 0 && (serverPairs?.length ?? 0) > 0
  const [confirmWipeOpen, setConfirmWipeOpen] = useState(false)

  // Save replaces the whole set, so it waits for the saved pairs: before they
  // load, the rows are a blank placeholder and saving them would erase the set.
  const loadFailed = isError && !serverPairs

  const handleSaveClick = () => {
    if (!serverPairs) return
    if (pairs.length > MAX_PAIRS) {
      toast.error(`Max ${MAX_PAIRS} pairs per user — that's ${pairs.length}. Trim some rows.`)
      return
    }
    if (wouldWipeAll) {
      setConfirmWipeOpen(true)
      return
    }
    saveMut.mutate(undefined)
  }

  const saveMut = useMutationWithToast({
    mutationFn: () => replaceUserLanguagePairs(userId, pairs),
    successMessage: 'Language pairs saved',
    errorMessage: 'Failed to save language pairs',
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.userLanguagePairs.byUser(userId) })
      // Invalidate the by-pair lookup prefix — qualified-users lists for any
      // source/target combo may have changed. Use the factory's `all` prefix
      // so we don't drift if the key shape changes.
      queryClient.invalidateQueries({ queryKey: queryKeys.usersByPair.all })
      onOpenChange(false)
    },
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange} keepOpen={saveMut.isPending}>
      <DialogContent className="sm:max-w-3xl" data-testid="language-pairs-dialog">
        <DialogHeader>
          <DialogTitle>Language pairs — {userName}</DialogTitle>
          <DialogDescription>
            Each row pairs every selected source with every selected target. Add rows for
            other source/target groups. Workflow assignments filter to qualified members;
            admins and owners are not filtered.
          </DialogDescription>
        </DialogHeader>

        {isLoading ? (
          <div className="py-6 text-sm text-gray-500">Loading…</div>
        ) : loadFailed ? (
          <LoadFailed what="language pairs" error={error} onRetry={() => refetch()} />
        ) : (
          <div className="space-y-4">
            <div className="space-y-2">
              <div className="flex items-center gap-2 text-xs font-medium text-gray-500 dark:text-gray-400">
                <span className="flex-1">Sources</span>
                <span className="w-4 shrink-0" />
                <span className="flex-1">Targets</span>
                <span className="w-7 shrink-0" />
              </div>

              {rows.map((row, i) => (
                <div key={i} className="flex items-center gap-2" data-testid={`pair-row-${i}`}>
                  <div className="flex-1">
                    <MultiCombobox
                      options={toLangOptions(langOptions)}
                      value={row.sources}
                      onChange={sources => updateRow(i, { sources })}
                      placeholder="Sources"
                      className="w-full"
                      {...langPickerProps}
                    />
                  </div>
                  <span className="shrink-0 text-base text-gray-600 dark:text-gray-200 font-semibold">→</span>
                  <div className="flex-1">
                    <MultiCombobox
                      options={toLangOptions(langOptions)}
                      value={row.targets}
                      onChange={targets => updateRow(i, { targets })}
                      placeholder="Targets"
                      className="w-full"
                      {...langPickerProps}
                    />
                  </div>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-xs"
                    onClick={() => removeRow(i)}
                    aria-label={`Remove row ${i + 1}`}
                    color="muted"
                    hoverColor="red"
                    className="shrink-0"
                  >
                    <X className="w-4 h-4" />
                  </Button>
                </div>
              ))}

              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={addRow}
                data-testid="add-row-button"
              >
                <Plus className="w-3 h-3 mr-1" />Add languages
              </Button>
            </div>

            {/* Read-only preview of the resulting pairs — grouped by source and
                styled to match the inline chips in the Org Settings member list. */}
            <div className="border-t border-gray-100 dark:border-gray-800 pt-3" data-testid="pairs-chip-list">
              {pairs.length === 0 ? (
                <span className="text-sm text-gray-400 italic">No pairs yet — pick sources and targets above.</span>
              ) : (
                <div className="flex flex-wrap items-center gap-1.5">
                  <span className="text-xs text-gray-500 dark:text-gray-400 mr-1">
                    {pairs.length} pair{pairs.length === 1 ? '' : 's'}:
                  </span>
                  {/* One chip per editing row (not regrouped) so each row above maps
                      1:1 to a component here. */}
                  {rows
                    .filter(row => row.sources.length > 0 && row.targets.length > 0)
                    .map((row, i) => <PairRowChip key={i} row={row} />)}
                </div>
              )}
            </div>
          </div>
        )}

        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={saveMut.isPending}>
            Cancel
          </Button>
          {!loadFailed && (
            <Button
              onClick={handleSaveClick}
              disabled={saveMut.isPending || !serverPairs}
              data-testid="save-pairs-button"
            >
              {saveMut.isPending ? 'Saving…' : wouldWipeAll ? 'Remove all' : 'Save'}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>

      {/* Wipe-guard: stacked AlertDialog matches the codebase convention
          (ConfirmDialog used in CustomersPage delete, TM browser delete, etc.).
          Avoids native window.confirm which can't be themed and looks foreign
          to the app. */}
      <ConfirmDialog
        open={confirmWipeOpen}
        onOpenChange={setConfirmWipeOpen}
        title={`Remove all language pairs for ${userName}?`}
        description={`${serverPairs?.length ?? 0} pair${(serverPairs?.length ?? 0) === 1 ? '' : 's'} will be removed. ${userName} will no longer appear in workflow-assignment dropdowns filtered by language pair until pairs are re-added.`}
        actionLabel="Remove all"
        onConfirm={() => saveMut.mutate(undefined)}
      />
    </Dialog>
  )
}
