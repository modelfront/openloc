// LanguagePairsEditor — the rows editor for one user's language-pair qualifications.
//
// UI model: a list of ROWS. Each row multi-selects sources
// and multi-selects targets; the row expands to the cartesian product of its
// sources × targets. "Add languages" appends another row so different source/
// target groups can be entered. The full pair set is every row's product,
// deduped, with self-pairs (source === target) dropped.
//
// Controlled: the caller owns the rows and saves the computed set with
// `replaceUserLanguagePairs`, whose PUT clears + bulk inserts atomically.
//
// Server normalizes language codes (lowercase, region preserved e.g. en-us) and
// returns the canonical set.

import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { X, Plus } from 'lucide-react'
import { listLanguages, type LanguagePair } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { Chip } from '@/components/ui/chip'
import { MultiCombobox } from '@/components/ui/combobox'
import { langPickerProps, toLangOptions } from '../lang-options'

/** Backend cap on pairs per user; mirrored client-side for a friendlier error. */
export const MAX_PAIRS = 100

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

// A chip side lists this many languages, then "+N" for the rest: a row of ten
// targets would otherwise push a table's other columns away.
const CHIP_SIDE_MAX = 2

/** One side of a pair chip: the first languages, and how many are left out. */
function ChipSide({ codes }: { codes: string[] }) {
  const more = codes.length - CHIP_SIDE_MAX
  return (
    <>
      {codes.slice(0, CHIP_SIDE_MAX).join(', ')}
      {more > 0 && <span className="opacity-70">+{more}</span>}
    </>
  )
}

/** One grouped language-pair row (`en, fr → es`) as a read-only chip. Shared
 *  by the editor's summary and the org Members and Vendors tables so they read
 *  alike. Each side shows two languages and "+N"; the tooltip has them all. */
export function PairRowChip({ row }: { row: Pick<PairRow, 'sources' | 'targets'> }) {
  const chip = (
    <Chip className="gap-1 px-1.5 font-mono font-normal" data-testid="pair-row-chip">
      <ChipSide codes={row.sources} />
      {/* Weight alone separates the arrow; the colour is the chip's. */}
      <span className="mx-0.5 font-semibold">→</span>
      <ChipSide codes={row.targets} />
    </Chip>
  )
  if (row.sources.length <= CHIP_SIDE_MAX && row.targets.length <= CHIP_SIDE_MAX) return chip
  return (
    <TooltipProvider delay={200}>
      <Tooltip>
        <TooltipTrigger render={<span className="inline-flex" />}>{chip}</TooltipTrigger>
        <TooltipContent className="font-mono text-xs">
          {row.sources.join(', ')} → {row.targets.join(', ')}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}

export default function LanguagePairsEditor({ rows, onChange }: {
  rows: PairRow[]
  onChange: (rows: PairRow[]) => void
}) {
  const { data: languages } = useQuery({
    queryKey: queryKeys.languages.all,
    queryFn: listLanguages,
  })
  const langOptions = useMemo(
    () => toLangOptions((languages ?? []).map(l => ({ code: l.code, name: l.name }))),
    [languages],
  )
  const pairs = useMemo(() => pairsFromRows(rows), [rows])

  const updateRow = (index: number, patch: Partial<PairRow>) =>
    onChange(rows.map((r, i) => (i === index ? { ...r, ...patch } : r)))
  const addRow = () => onChange([...rows, emptyRow()])
  const removeRow = (index: number) => {
    const next = rows.filter((_, i) => i !== index)
    onChange(next.length > 0 ? next : [emptyRow()]) // always keep one editable row
  }

  return (
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
                options={langOptions}
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
                options={langOptions}
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
          startIcon={<Plus />}
          data-testid="add-row-button"
        >
          Add languages
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
  )
}
