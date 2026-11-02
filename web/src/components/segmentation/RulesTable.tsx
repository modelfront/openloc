import { arrayMove } from '@dnd-kit/sortable'
import type { SegmentationRule } from '@/api'
import { AddItemButton } from '@/components/list/AddItemButton'
import { SortableGridRow } from '@/components/list/SortableGridRow'
import { SortableList } from '@/components/list/SortableList'
import { ChoiceToggle } from '@/components/ui/choice-toggle'
import { DeleteButton } from '@/components/ui/delete-button'
import { DragHandle } from '@/components/ui/drag-handle'
import { Input } from '@/components/ui/input'
import { BREAK_OPTIONS, ruleErrorText } from '@/lib/segmentation'

/** A rule with a stable key for drag and drop and React. */
export interface KeyedRule extends SegmentationRule {
  key: string
}

let nextKey = 0
export const withKey = (r: SegmentationRule): KeyedRule => ({ ...r, key: `rule-${++nextKey}` })

const GRID = '28px 32px minmax(0,1fr) minmax(0,1fr) 168px 32px'

/**
 * The ordered rules of a set: the first rule that matches a position decides
 * it. Rows reorder by drag (or Space and the arrow keys on the handle), and
 * errorIndex marks the rule a validation message names.
 */
export function RulesTable({ rules, onChange, readOnly, errorIndex, error }: {
  rules: KeyedRule[]
  onChange: (rules: KeyedRule[]) => void
  readOnly: boolean
  errorIndex: number | null
  error?: string
}) {
  const update = (i: number, patch: Partial<SegmentationRule>) => onChange(rules.map((r, j) => (j === i ? { ...r, ...patch } : r)))

  return (
    <div data-guide="segmentation.rules" data-field="rules" data-testid="segmentation-rules">
      <div className="grid items-center gap-2 border-b border-border px-1 pb-1 text-xs font-medium text-muted-foreground" style={{ gridTemplateColumns: GRID }}>
        <span />
        <span>#</span>
        <span>Before the break</span>
        <span>After the break</span>
        <span>Action</span>
        <span />
      </div>
      {rules.length === 0 && (
        <p className="px-1 py-3 text-sm text-muted-foreground">No rules: nothing breaks except at the word lists' exceptions.</p>
      )}
      <SortableList ids={rules.map(r => r.key)} onMove={(from, to) => onChange(arrayMove(rules, from, to))}>
        {rules.map((rule, i) => (
          <SortableGridRow key={rule.key} id={rule.key} disabled={readOnly} gridCols={GRID} testId={`segmentation-rule-row-${i}`}
            className="gap-2 border-b border-border px-1 py-1.5">
            {({ handleRef, handleProps }) => (
              <>
                {readOnly ? <span /> : (
                  <DragHandle ref={handleRef} {...handleProps}
                    title="Drag to reorder (top = tried first). Keyboard: Space, arrows, Space."
                    data-testid={`segmentation-rule-drag-${i}`} />
                )}
                <span className="text-xs tabular-nums text-muted-foreground">{i + 1}</span>
                <Input value={rule.before} onChange={e => update(i, { before: e.target.value })} readOnly={readOnly}
                  aria-label={`Rule ${i + 1}: before the break`} aria-invalid={errorIndex === i || undefined}
                  placeholder={readOnly ? 'Any text' : 'Regex (optional)'} className="font-mono" clearable={false} data-testid={`segmentation-rule-before-${i}`} />
                <Input value={rule.after} onChange={e => update(i, { after: e.target.value })} readOnly={readOnly}
                  aria-label={`Rule ${i + 1}: after the break`} aria-invalid={errorIndex === i || undefined}
                  placeholder={readOnly ? 'Any text' : 'Regex (optional)'} className="font-mono" clearable={false} data-testid={`segmentation-rule-after-${i}`} />
                <ChoiceToggle size="sm" fill aria-label={`Rule ${i + 1}: action`} options={BREAK_OPTIONS.map(o => ({ ...o, disabled: readOnly }))}
                  value={rule.break ? 'break' : 'no-break'} onChange={v => update(i, { break: v === 'break' })}
                  data-testid={`segmentation-rule-action-${i}`} />
                {readOnly ? <span /> : (
                  <DeleteButton size="icon-xs" aria-label={`Remove rule ${i + 1}`} onClick={() => onChange(rules.filter((_, j) => j !== i))}
                    data-testid={`segmentation-rule-remove-${i}`} />
                )}
                {errorIndex === i && error && (
                  <p className="col-span-3 col-start-3 pb-1 text-xs text-destructive" role="alert" data-testid={`segmentation-rule-error-${i}`}>{ruleErrorText(error)}</p>
                )}
              </>
            )}
          </SortableGridRow>
        ))}
      </SortableList>
      {!readOnly && (
        <AddItemButton label="Add rule" className="mt-2 min-h-(--spacing-input) rounded" testId="segmentation-rule-add"
          onClick={() => onChange([...rules, withKey({ before: '', after: '', break: true })])} />
      )}
    </div>
  )
}
