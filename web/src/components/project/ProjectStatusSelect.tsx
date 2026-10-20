// ProjectStatusSelect — the shared Combobox over PROJECT_STATUSES.

import { PROJECT_STATUSES, type ProjectStatus } from '../../api'
import { Combobox } from '@/components/ui/combobox'
import { type ChipColor } from '@/components/ui/chip'

// The canonical status text + colour tables. A read-only status renders as
// `<Chip color={STATUS_COLORS[s]}>{STATUS_LABELS[s]}</Chip>` at the call site,
// so a PM reads state by colour wherever a status is shown. The select itself
// offers labels only.
//
// Display labels (Title Case). Stored values stay snake_case (matches DB enum).
export const STATUS_LABELS: Record<ProjectStatus, string> = {
  draft: 'Draft',
  in_progress: 'In progress',
  completed: 'Completed',
  on_hold: 'On hold',
}

export const STATUS_COLORS: Record<ProjectStatus, ChipColor> = {
  draft: 'grey',
  in_progress: 'blue',
  completed: 'green',
  on_hold: 'red',
}

// 'draft' is the wizard's autosave state: a project
// leaves it through publish and never goes back, so the select doesn't offer
// it. The badge and the list filter still render it.
const SELECTABLE_STATUSES = PROJECT_STATUSES.filter((s) => s !== 'draft')

interface SelectProps {
  value: ProjectStatus
  onChange: (status: ProjectStatus) => void
  disabled?: boolean
  className?: string
}

export function ProjectStatusSelect({ value, onChange, disabled = false, className = '' }: SelectProps) {
  return (
    <Combobox
      value={value}
      onChange={(v) => onChange(v as ProjectStatus)}
      options={SELECTABLE_STATUSES.map((s) => ({ value: s, label: STATUS_LABELS[s] }))}
      disabled={disabled}
      data-testid="project-status-select"
      className={className}
    />
  )
}

