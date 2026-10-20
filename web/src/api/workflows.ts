// Step-definition + workflow libraries, and the
// project/template endpoints that copy a library workflow or promote steps
// into one. Manager surface end to end.
import { request } from './request'
import type { StepDefinition, Workflow, WorkflowStep } from '../types/generated'

export type { StepDefinition, Workflow }

/** `pretranslate` is the built-in Pre-translation step's type only; the step
 *  library never offers it, so STEP_TYPES leaves it out. */
export type StepType = 'human' | 'hybrid' | 'ai' | 'pretranslate'

export const STEP_TYPES: { value: StepType; label: string; description: string }[] = [
  { value: 'human', label: 'Human', description: 'A linguist works and completes the step' },
  { value: 'hybrid', label: 'Hybrid', description: 'AI runs first, a human reviews and completes' },
  { value: 'ai', label: 'AI', description: 'AI runs and completes the step itself' },
]

/** GET /step-definitions row: the definition plus its AI router's name and
 *  how many library workflows compose it. */
export interface StepDefinitionRow extends StepDefinition {
  ai_router_name: string | null
  workflow_count: number
}

export interface StepDefinitionInput {
  name: string
  step_type: StepType
  ai_router_id: number | null
}

export const listStepDefinitions = () =>
  request<{ data: StepDefinitionRow[] }>('/step-definitions').then(r => r.data)

export const createStepDefinition = (input: StepDefinitionInput) =>
  request<{ data: StepDefinition }>('/step-definitions', { method: 'POST', body: JSON.stringify(input) }).then(r => r.data)

export const updateStepDefinition = (id: number, input: StepDefinitionInput) =>
  request<{ data: StepDefinition }>(`/step-definitions/${id}`, { method: 'PUT', body: JSON.stringify(input) }).then(r => r.data)

export const deleteStepDefinition = (id: number) =>
  request<void>(`/step-definitions/${id}`, { method: 'DELETE' })

/** One position of a library workflow as the API returns it: the definition's
 *  identity joined in, plus the position's default assignee. */
export interface WorkflowDefinitionStepRow {
  id: number
  workflow_id: number
  step_order: number
  default_assignee: number | null
  step_definition_id: number
  name: string
  step_type: StepType
  role_required: string
  ai_router_id: number | null
  definition_deleted: boolean
  assignee_name: string | null
}

/** One step of a workflow as the list endpoint summarises it. */
export interface WorkflowStepSummary {
  step_definition_id: number
  name: string
  step_type: StepType
  ai_router_id: number | null
  ai_router_name: string | null
  default_assignee: number | null
  assignee_name: string | null
}

export interface WorkflowListItem extends Workflow {
  steps: WorkflowStepSummary[]
}

export interface WorkflowDetail extends Workflow {
  steps: WorkflowDefinitionStepRow[]
}

export interface WorkflowInput {
  name: string
  steps: { step_definition_id: number; default_assignee: number | null }[]
}

export const listWorkflows = () =>
  request<{ data: WorkflowListItem[] }>('/workflows').then(r => r.data)

export const getWorkflow = (id: number) =>
  request<{ data: WorkflowDetail }>(`/workflows/${id}`).then(r => r.data)

/** The built-in Pre-translation workflow. The library hides it; the
 *  Workflow card's Raw MT mode finds it here. */
export const getPretranslateWorkflow = () =>
  request<{ data: WorkflowDetail }>('/workflows/pretranslate-only').then(r => r.data)

export const createWorkflow = (input: WorkflowInput) =>
  request<{ data: WorkflowDetail }>('/workflows', { method: 'POST', body: JSON.stringify(input) }).then(r => r.data)

export const updateWorkflow = (id: number, input: WorkflowInput) =>
  request<{ data: WorkflowDetail }>(`/workflows/${id}`, { method: 'PUT', body: JSON.stringify(input) }).then(r => r.data)

export const deleteWorkflow = (id: number) =>
  request<void>(`/workflows/${id}`, { method: 'DELETE' })

/** The workflow-steps PUT body item (projects, templates, project create).
 *  Identity comes from the library; `existing_step_id` carries a pre-library
 *  row forward unchanged. Exactly one of the two is set. */
export interface WorkflowStepPayload {
  step_definition_id?: number
  existing_step_id?: number
  default_assignee: number | null
  deadline: string | null
  /** Per-target-language overrides of `deadline`, sent as a complete set. */
  language_deadlines: { target_language: string; deadline: string }[]
  /** Per-profile AI router for a hybrid / ai step; absent keeps the snapshot. */
  ai_router_id?: number
}

export const listTemplateWorkflowSteps = (templateId: number) =>
  request<{ data: WorkflowStep[] }>(`/templates/${templateId}/workflow-steps`).then(r => r.data)

export const setTemplateWorkflowSteps = (templateId: number, steps: WorkflowStepPayload[]) =>
  request<{ data: WorkflowStep[] }>(`/templates/${templateId}/workflow-steps`, {
    method: 'PUT', body: JSON.stringify({ steps }),
  }).then(r => r.data)

export const setProjectWorkflowSteps = (projectId: number, steps: WorkflowStepPayload[]) =>
  request<{ data: WorkflowStep[] }>(`/projects/${projectId}/workflow-steps`, {
    method: 'PUT', body: JSON.stringify({ steps }),
  }).then(r => r.data)

export interface ApplyWorkflowResult {
  workflow_id: number
  workflow_name: string | null
  steps: WorkflowStep[]
}

export const applyProjectWorkflow = (projectId: number, workflowId: number) =>
  request<{ data: ApplyWorkflowResult }>(`/projects/${projectId}/workflow/apply`, {
    method: 'POST', body: JSON.stringify({ workflow_id: workflowId }),
  }).then(r => r.data)

export const saveProjectWorkflowAs = (projectId: number, name: string) =>
  request<{ data: Workflow }>(`/projects/${projectId}/workflow/save-as`, {
    method: 'POST', body: JSON.stringify({ name }),
  }).then(r => r.data)

export const applyTemplateWorkflow = (templateId: number, workflowId: number) =>
  request<{ data: WorkflowStep[] }>(`/templates/${templateId}/workflow/apply`, {
    method: 'POST', body: JSON.stringify({ workflow_id: workflowId }),
  }).then(r => r.data)

export const saveTemplateWorkflowAs = (templateId: number, name: string) =>
  request<{ data: Workflow }>(`/templates/${templateId}/workflow/save-as`, {
    method: 'POST', body: JSON.stringify({ name }),
  }).then(r => r.data)

/** Clears the project's "Based on …" pointer; the copied steps stay and become editable. */
export const detachProjectWorkflow = (projectId: number) =>
  request<void>(`/projects/${projectId}/workflow/detach`, { method: 'POST' })

export const detachTemplateWorkflow = (templateId: number) =>
  request<void>(`/templates/${templateId}/workflow/detach`, { method: 'POST' })
