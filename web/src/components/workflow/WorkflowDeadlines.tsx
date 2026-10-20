// Workflow step deadlines, per target language.
//
// A project's jobs are one file per target language, so a step can be due on a
// different date per language. Which date wins is resolved server-side, where
// an assignment is written (internal/settings/step_deadlines.go); this file
// only renders and reports edits. Only human and hybrid steps have a deadline.
//
// `WorkflowStepsWithDeadlines` hangs a `StepDeadlineColumn` off the right end
// of each step's row, so every date belonging to a step lines up under the
// step's own. The open state lives in the list, because a row cannot call a
// hook per item.

import { useRef, useState, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listLanguages } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { DeadlinePicker } from '@/components/ui/deadline-picker'
import { Globe } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { AddItemButton } from '@/components/list'
import { MultiCombobox } from '@/components/ui/combobox'
import { DeleteButton } from '@/components/ui/delete-button'
import { langPickerProps, toLangOptions } from '@/components/lang-options'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'
import type { WorkflowStep } from '@/components/WorkflowStepsEditor'
import { WorkflowStepChips, type ChipStep } from './WorkflowStepChips'
import { isAutomatedStep } from '@/lib/workflow-steps'

/** Everything one step's deadline controls need. `index` is the position in
 *  the FULL step list, so a write lands on the right row even though AI steps
 *  show no controls. */
interface StepDeadlineProps {
  steps: WorkflowStep[]
  index: number
  targetLanguages: string[]
  onChange: (steps: WorkflowStep[]) => void
  disabled?: boolean
}

/** A step carries a deadline unless it runs itself. */
function isDatable(step: Pick<WorkflowStep, 'step_type'>): boolean {
  return !isAutomatedStep(step.step_type)
}

/** How many of a step's languages carry a date of their own. */
function languagesSet(step: WorkflowStep, targetLanguages: string[]): number {
  const byLang = step.language_deadlines ?? {}
  return targetLanguages.filter(l => byLang[l]).length
}

function patch(p: StepDeadlineProps, change: Partial<WorkflowStep>) {
  p.onChange(p.steps.map((s, i) => (i === p.index ? { ...s, ...change } : s)))
}

/** Open state for the per-language blocks, keyed by step position. */
function useStepLanguageDisclosure() {
  const [open, setOpen] = useState<Record<number, boolean>>({})
  return {
    isOpen: (index: number) => !!open[index],
    toggle: (index: number) => setOpen(prev => ({ ...prev, [index]: !prev[index] })),
  }
}

interface ColumnProps extends StepDeadlineProps {
  open: boolean
  onToggle: () => void
}

/**
 * One step's deadlines, as a column to hang off the right end of its row:
 *
 *                    [ Due in 4d ]  (globe)
 *     [ ES, FR ]     [ Due in 2d ]  (delete)
 *     [ + Add override per language ]
 *
 * Every date is a chip in the same words the project and job lists use, all
 * ending on one right edge so they read as a column. The globe opens the
 * per-language dates; it is tinted while any language carries one, so a
 * collapsed step still says so.
 */
function StepDeadlineColumn({ open, onToggle, ...p }: ColumnProps) {
  const step = p.steps[p.index]
  if (!isDatable(step)) return null

  const setCount = languagesSet(step, p.targetLanguages)
  const hasLanguages = p.targetLanguages.length > 0
  const row = 'flex items-center justify-end gap-2'
  const sectionId = `workflow-deadline-langs-${p.index}`
  const langsHint = setCount > 0
    ? `Deadline override per language (${setCount} ${setCount === 1 ? 'language' : 'languages'} set)`
    : 'Deadline override per language'

  return (
    // `min-w-0`, not `shrink-0`: in a narrow container the 23.5rem override
    // block must shrink rather than overflow.
    <div className="flex min-w-0 flex-col gap-1.5">
      <div className={row}>
        {/* w-30 (7.5rem) fits the longest chip ("Due Sep 30, 2027") and no
            more, or the slack shows as a gap before the chip. */}
        <span className="flex w-30 shrink-0 justify-end">
          <DeadlinePicker
            value={step.deadline}
            onChange={deadline => patch(p, { deadline })}
            disabled={p.disabled}
            aria-label={`Deadline for ${step.name}`}
            data-testid={`workflow-deadline-input-${p.index}`}
          />
        </span>
        {hasLanguages && (
          <TooltipProvider delay={100}>
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    // Outline, not ghost: the frame is what says "clickable"
                    // for a glyph someone might otherwise read as decoration.
                    // The variant tints itself while aria-expanded, so an open
                    // step's button looks pressed.
                    variant="outline"
                    // icon-xs, not icon: these end a row beside a chip, and the
                    // deadline chips only form a column while the globe and the
                    // delete below it are the same size.
                    size="icon-xs"
                    onClick={onToggle}
                    disabled={p.disabled}
                    aria-expanded={open}
                    aria-controls={sectionId}
                    aria-label={`${langsHint}, ${step.name}`}
                    color={setCount > 0 ? 'primary' : undefined}
                    className="shrink-0"
                    data-testid={`workflow-deadline-langs-toggle-${p.index}`}
                  />
                }
              >
                <Globe aria-hidden />
              </TooltipTrigger>
              <TooltipContent className="text-xs">{langsHint}</TooltipContent>
            </Tooltip>
          </TooltipProvider>
        )}
      </div>
      {hasLanguages && open && (
        <div id={sectionId}>
          <StepLanguageOverrides
            // Remounts each time the section opens, which is deliberate: the
            // rows are derived from the saved overrides on mount, so there is
            // no second copy of the truth to keep in sync while it is closed.
            stepName={step.name}
            index={p.index}
            overrides={step.language_deadlines ?? {}}
            targetLanguages={p.targetLanguages}
            onChange={next => patch(p, { language_deadlines: next })}
            disabled={p.disabled}
            rowClassName={row}
          />
        </div>
      )}
    </div>
  )
}

/** One override: a set of target languages that share a deadline. */
interface OverrideRow {
  key: number
  langs: string[]
  deadline: string | null
}

/** Saved overrides → rows, grouping the languages that share a date. Two rows
 *  the user kept apart but gave the same date come back as one; they mean the
 *  same thing, and the grouping is what the stored shape can express. */
function rowsFromOverrides(overrides: Record<string, string>, targetLanguages: string[]): OverrideRow[] {
  const byDeadline = new Map<string, string[]>()
  for (const lang of targetLanguages) {
    const deadline = overrides[lang]
    if (!deadline) continue
    byDeadline.set(deadline, [...(byDeadline.get(deadline) ?? []), lang])
  }
  return [...byDeadline].map(([deadline, langs], i) => ({ key: i, langs, deadline }))
}

/** Rows → the stored shape. A row still being filled in (no language yet, or
 *  no date) simply contributes nothing. */
function overridesFromRows(rows: OverrideRow[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const r of rows) {
    if (!r.deadline) continue
    for (const lang of r.langs) out[lang] = r.deadline
  }
  return out
}

interface OverridesProps {
  stepName: string
  index: number
  overrides: Record<string, string>
  targetLanguages: string[]
  onChange: (overrides: Record<string, string>) => void
  disabled?: boolean
  rowClassName: string
}

/**
 * The per-language overrides of one step: a row per group of languages that
 * share a date, and a button that adds another. A language belongs to at most
 * one row, so each row's picker offers only what the others have not taken —
 * which also means the button disables itself once every language is spoken
 * for.
 */
function StepLanguageOverrides({
  stepName, index, overrides, targetLanguages, onChange, disabled, rowClassName,
}: OverridesProps) {
  // A step with no overrides yet opens on an empty row rather than on a lone
  // button: the section exists to add one, so the first click should be into
  // the language picker. Seeding state does not call onChange, so merely
  // opening the section leaves the draft clean.
  const [rows, setRows] = useState<OverrideRow[]>(() => {
    const saved = rowsFromOverrides(overrides, targetLanguages)
    return saved.length > 0 ? saved : [{ key: 0, langs: [], deadline: null }]
  })
  const nextKey = useRef(rows.length)
  const { data: languages } = useQuery({ queryKey: queryKeys.languages.all, queryFn: listLanguages })

  const commit = (next: OverrideRow[]) => {
    setRows(next)
    onChange(overridesFromRows(next))
  }
  const update = (key: number, change: Partial<OverrideRow>) =>
    commit(rows.map(r => (r.key === key ? { ...r, ...change } : r)))

  // A language the OTHER rows have taken is not on offer here — one override
  // per language is the rule the stored shape enforces anyway.
  const taken = (exceptKey: number) =>
    new Set(rows.filter(r => r.key !== exceptKey).flatMap(r => r.langs))
  const named = (code: string) => languages?.find(l => l.code === code)?.name ?? code
  const optionsFor = (key: number) => {
    const used = taken(key)
    return toLangOptions(
      targetLanguages.filter(c => !used.has(c)).map(code => ({ code, name: named(code) })),
    )
  }
  const allTaken = taken(-1).size >= targetLanguages.length
  // An override needs both halves to mean anything, so one unfinished row is
  // as many as there is any use for — a second would just be another blank.
  const hasUnfinished = rows.some(r => r.langs.length === 0 || !r.deadline)

  return (
    <div className="mt-1.5 space-y-1.5">
      {rows.map(r => (
        <div
          key={r.key}
          // 23.5rem is how much room the languages get once the chip and the
          // delete are placed: three language chips before they collapse into "+N".
          className={cn(rowClassName, 'w-[23.5rem] max-w-full text-sm')}
          data-testid={`workflow-deadline-override-${index}-${r.key}`}
        >
          {/* Flexes into everything the chip and the delete do not use, so no
              dead space opens up between the languages and the date. */}
          <span className="min-w-0 flex-1">
            <MultiCombobox
              options={optionsFor(r.key)}
              value={r.langs}
              onChange={langs => update(r.key, { langs })}
              placeholder="Languages"
              className="w-full"
              disabled={disabled}
              aria-label={`Languages for this ${stepName} deadline override`}
              {...langPickerProps}
            />
          </span>
          <span className="shrink-0">
            <DeadlinePicker
              value={r.deadline}
              onChange={deadline => update(r.key, { deadline })}
              disabled={disabled}
              aria-label={`Override deadline for ${stepName}`}
              data-testid={`workflow-deadline-override-input-${index}-${r.key}`}
            />
          </span>
          <DeleteButton
            size="icon-xs"
            className="shrink-0"
            onClick={() => commit(rows.filter(x => x.key !== r.key))}
            disabled={disabled}
            aria-label="Remove this override"
            data-testid={`workflow-deadline-override-remove-${index}-${r.key}`}
          />
        </div>
      ))}
      {/* The standard add-a-row affordance, spanning the whole block — the
          languages, the date and the delete column — so it reads as "another
          one of these" rather than a control beside them. `min-h-input` keeps
          it the height of the fields, as the router dialog's does. */}
      <AddItemButton
        label="Add override per language"
        onClick={() => {
          const key = nextKey.current++
          commit([...rows, { key, langs: [], deadline: null }])
        }}
        disabled={disabled || allTaken || hasUnfinished}
        className="min-h-input"
        testId={`workflow-deadline-override-add-${index}`}
      />
    </div>
  )
}

interface ListProps {
  steps: WorkflowStep[]
  targetLanguages: string[]
  onChange: (steps: WorkflowStep[]) => void
  disabled?: boolean
  /** Heads the rail with the Pre-translation row (see WorkflowStepChips). */
  pretranslation?: ReactNode
  'data-testid'?: string
}

interface WithDeadlinesProps extends ListProps {
  /** Rendered under the list — the Workflow tab puts its saving indicator here. */
  children?: ReactNode
}

/**
 * The read-only step list with each step's deadline column on the same row.
 * One list, not two: a step and when it is due are the same thought.
 */
export function WorkflowStepsWithDeadlines({
  steps, targetLanguages, onChange, disabled, pretranslation, children, 'data-testid': testId,
}: WithDeadlinesProps) {
  const langs = useStepLanguageDisclosure()

  return (
    <>
      <WorkflowStepChips
        steps={steps as ChipStep[]}
        pretranslation={pretranslation}
        data-testid={testId}
        trailing={i => (
          <StepDeadlineColumn
            steps={steps}
            index={i}
            targetLanguages={targetLanguages}
            onChange={onChange}
            disabled={disabled}
            open={langs.isOpen(i)}
            onToggle={() => langs.toggle(i)}
          />
        )}
      />
      {children}
    </>
  )
}
