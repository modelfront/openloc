import { Languages, FileCheck2, Wand2, Pencil } from 'lucide-react'
import { cn } from '@/lib/utils'
import { OUTPUT_MODES, MODE_DESCRIPTIONS, type OutputMode } from './output-mode'

// Picking what a prompt does is its first and only type decision: it fixes the
// starter template, the fields the model returns, and which per-segment context
// applies. Cards rather than bare acronyms so each choice and its consequence
// are readable without opening anything.

const MODE_ICONS: Record<OutputMode, typeof Languages> = {
  mt: Languages,
  qe: FileCheck2,
  qe_ape: Wand2,
  ape: Pencil,
}

export function ModeCards({
  value, options, onChange, readOnly,
}: {
  /** Undefined on a create page, where the type has not been picked yet. */
  value?: OutputMode
  /** The modes this page offers — an MT page lists only `mt`. */
  options: OutputMode[]
  onChange: (mode: OutputMode) => void
  readOnly?: boolean
}) {
  const label = (mode: OutputMode) =>
    OUTPUT_MODES.find(o => o.value === mode)?.label ?? mode

  return (
    <div
      className="grid gap-2 sm:grid-cols-2"
      role="radiogroup"
      aria-label="What this prompt does"
      data-testid="ai-prompt-type"
    >
      {options.map(mode => {
        const Icon = MODE_ICONS[mode]
        const selected = mode === value
        return (
          // eslint-disable-next-line no-restricted-syntax -- a radio option, not an action: <Button> would style a selection control as something that does something.
          <button
            key={mode}
            type="button"
            role="radio"
            aria-checked={selected}
            disabled={readOnly}
            onClick={() => onChange(mode)}
            data-testid={`ai-prompt-type-option-${mode}`}
            className={cn(
              'flex gap-2.5 rounded-md border p-2.5 text-left transition-colors',
              selected
                ? 'border-primary bg-accent/40'
                : 'border-input hover:bg-muted',
              readOnly && 'cursor-not-allowed opacity-60 hover:bg-transparent',
            )}
          >
            <Icon className={cn('mt-0.5 size-4 shrink-0', selected ? 'text-primary' : 'text-muted-foreground')} />
            <span className="min-w-0">
              <span className="block text-sm font-medium text-gray-900 dark:text-gray-100">{label(mode)}</span>
              <span className="block text-[11px] leading-snug text-muted-foreground">{MODE_DESCRIPTIONS[mode]}</span>
            </span>
          </button>
        )
      })}
    </div>
  )
}
