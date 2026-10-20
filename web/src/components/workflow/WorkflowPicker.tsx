import { useState, type ReactNode } from 'react'
import { Workflow as WorkflowIcon } from 'lucide-react'
import { Combobox, type ComboboxAction } from '@/components/ui/combobox'
import { HelpTip } from '@/components/ui/help-tip'
import ConfirmDialog from '../ConfirmDialog'
import type { WorkflowListItem } from '../../api'
import { TextLink } from '@/components/ui/text-link'

interface Props {
  workflows: WorkflowListItem[]
  /** The library workflow the steps were copied from, or null for a set that
   *  predates the library (or steps a template brought into the wizard). */
  value: number | null
  /** Its name, resolved server-side so a retired workflow still reads by name. */
  valueName?: string | null
  entity: 'project' | 'template'
  /**
   * Ask before replacing the steps. The wizard applies straight away — nothing
   * is running yet — while a live project or template has steps that jobs may
   * already have reached, so the swap is worth a confirmation.
   */
  confirmApply?: boolean
  disabled?: boolean
  /** Locks the dropdown while the choice is not the user's to make: a created
   *  Raw MT project, which the server keeps on the built-in workflow. */
  locked?: boolean
  /** Replaces the "Workflow" label above the dropdown — the mode toggle. The
   *  field then names itself for assistive tech only. */
  heading?: ReactNode
  /** Icon of a chosen workflow the library list does not hold (a built-in or
   *  retired one). */
  valueIcon?: ReactNode
  /** Extra sentence in the "use this workflow" confirmation. */
  applyWarning?: string
  onApply: (id: number) => Promise<unknown>
  /** Pinned rows under the dropdown's search field ("New template"). */
  actions?: ComboboxAction[]
  /** The steps behind the current choice, rendered read-only under the field. */
  children?: ReactNode
}

/** The card's heading row: the mode toggle when there is one, else the
 *  "Workflow" label of the field. */
export function PickerHeading({ heading, help, htmlFor }: { heading?: ReactNode; help: string; htmlFor?: string }) {
  return heading ? (
    <div className="flex items-center gap-2">
      {heading}
      <HelpTip text={help} />
    </div>
  ) : (
    <label htmlFor={htmlFor} className="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
      Workflow
      <HelpTip text={help} />
    </label>
  )
}

/**
 * Where a project's or template's steps come from: one dropdown over the
 * Workflows library. Choosing one copies its steps here and
 * shows them read-only; the library itself is never edited from here, and
 * editing a library workflow later never reaches an entity that already copied
 * it.
 *
 * Steps with no library workflow behind them — a project made before the
 * library, or steps a template carried into the wizard — render under an empty
 * field until a workflow replaces them.
 */
export function WorkflowPicker({
  workflows, value, valueName, entity, confirmApply = false, disabled = false, locked = false, heading, valueIcon, applyWarning,
  onApply, actions, children,
}: Props) {
  const [pendingApply, setPendingApply] = useState<number | null>(null)
  const [busy, setBusy] = useState(false)

  const attached = value != null
  const linked = attached ? workflows.find(w => w.id === value) : undefined
  const noWorkflows = workflows.length === 0

  const handleChange = (next: string) => {
    const id = Number(next)
    if (!id || id === value) return
    if (confirmApply) setPendingApply(id)
    else void run(() => onApply(id))
  }

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true)
    try {
      await fn()
    } finally {
      setBusy(false)
    }
  }

  const pendingName = workflows.find(w => w.id === pendingApply)?.name
  const nameLabel = linked?.name ?? valueName ?? (attached ? `workflow #${value}` : '')
  const help = `A reusable, ordered list of steps from the Workflows library. Choosing one copies its steps onto this ${entity}; editing the library workflow later never changes a ${entity} that already copied it.`

  return (
    <div data-testid="workflow-picker" className="space-y-4">
      <PickerHeading heading={heading} help={help} htmlFor="workflow-setup" />
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <Combobox
          id="workflow-setup"
          aria-label={heading ? 'Workflow' : undefined}
          value={attached ? String(value) : ''}
          onChange={handleChange}
          actions={actions}
          placeholder="Choose a workflow…"
          options={[
            ...workflows.map(w => ({
              value: String(w.id),
              label: w.name,
              icon: <WorkflowIcon className="size-3.5 text-primary" aria-hidden />,
              chip: <span className="text-xs text-gray-400">{w.steps.map(s => s.name).join(' → ')}</span>,
            })),
            // A built-in or retired workflow still names itself in the trigger.
            ...(attached && !linked ? [{ value: String(value), label: nameLabel, icon: valueIcon ?? <WorkflowIcon className="size-3.5 text-gray-400" aria-hidden /> }] : []),
          ]}
          className="w-80"
          contentClassName="w-96"
          disabled={disabled || busy || locked}
          data-testid="use-workflow-select"
        />
        {noWorkflows && !locked && (
          <span className="text-xs text-gray-500 dark:text-gray-400">
            No workflows yet — create one on the{' '}
            <TextLink to="/workflows">Workflows</TextLink> page.
          </span>
        )}
      </div>

      <div data-testid="workflow-attached-steps">{children}</div>

      <ConfirmDialog
        open={pendingApply != null}
        onOpenChange={open => !open && setPendingApply(null)}
        title={`Use workflow “${pendingName ?? ''}”?`}
        description={`This replaces the current steps with a copy of the workflow's steps.${applyWarning ? ` ${applyWarning}` : ''}`}
        actionLabel="Use workflow"
        variant="default"
        onConfirm={() => {
          const id = pendingApply
          setPendingApply(null)
          if (id != null) void run(() => onApply(id))
        }}
      />
    </div>
  )
}
