import type { ProjectTask } from '@/api'

// The canonical ProjectTask fixture: a pending, unassigned human "translate"
// task with no deadline. Tests override only what they assert on.
export function makeProjectTask(overrides: Partial<ProjectTask> = {}): ProjectTask {
  return {
    id: 1,
    job_id: 1,
    workflow_step_id: 100,
    status: 'pending',
    deadline: null,
    assigned_to: null,
    assignee_name: null,
    completed_at: null,
    rejection_reason: '',
    handoff_note: null,
    step_name: 'translate',
    step_order: 0,
    step_type: 'human',
    ...overrides,
  }
}
