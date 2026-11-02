import { Workflow as WorkflowIcon } from 'lucide-react'
import type { WorkflowListItem } from '../../api'

/** The library as dropdown options: each workflow by name, its steps beside it. */
export function workflowOptions(workflows: WorkflowListItem[]) {
  return workflows.map(w => ({
    value: String(w.id),
    label: w.name,
    icon: <WorkflowIcon className="size-3.5 text-primary" aria-hidden />,
    chip: <span className="text-xs text-gray-400">{w.steps.map(s => s.name).join(' → ')}</span>,
  }))
}
