import type { WorkflowStep } from '@/components/WorkflowStepsEditor'
import type { StepDefinitionRow, WorkflowDefinitionStepRow, WorkflowStepPayload, StepType } from '../api/workflows'

/** An automated step completes itself: nobody is assigned to it and it has no
 *  deadline. */
export function isAutomatedStep(stepType: string | null | undefined): boolean {
  return stepType === 'ai' || stepType === 'pretranslate'
}

/** A step that runs its AI router when it starts, so it needs one. */
export function runsAIStep(stepType: string | null | undefined): boolean {
  return stepType === 'hybrid' || stepType === 'ai'
}

/** True when the steps are the single built-in Pre-translation step. */
export function isPretranslateOnly(steps: { step_type?: string | null }[]): boolean {
  return steps.length === 1 && steps[0].step_type === 'pretranslate'
}

/** The Workflow card's two modes: `raw_mt` for the built-in Pre-translation
 *  workflow, `workflow` for anything else. */
export type WorkflowMode = 'workflow' | 'raw_mt'

export function workflowMode(steps: { step_type?: string | null }[]): WorkflowMode {
  return isPretranslateOnly(steps) ? 'raw_mt' : 'workflow'
}

/** The steps a new workflow starts with: the library's Translation and Review
 *  definitions, in that order, when they exist. Shared by the New Project
 *  wizard's default and the Workflows list's create dialog. */
export function defaultStepsFrom(definitions: StepDefinitionRow[] | undefined): WorkflowStep[] {
  if (!definitions) return []
  const byName = (n: string) => definitions.find(d => d.name.toLowerCase() === n)
  return [byName('translation'), byName('review')]
    .filter((d): d is StepDefinitionRow => !!d)
    .map(d => ({
      step_definition_id: d.id, name: d.name, step_type: d.step_type as StepType,
      ai_router_id: d.ai_router_id, deadline: null,
    }))
}

/** Editor draft → request payload. The ONE mapper for every workflow-steps
 *  writer (ProjectCreate, WorkflowTab for projects and templates). Identity
 *  travels as a library reference; a pre-library row keeps its identity via
 *  its own id. Rows with neither are the editor's "pick a step" placeholders
 *  and are rejected before this runs. */
export function workflowStepsToPayload(steps: WorkflowStep[]): WorkflowStepPayload[] {
  return steps.map(s => ({
    step_definition_id: s.step_definition_id ?? undefined,
    existing_step_id: s.step_definition_id == null ? s.id : undefined,
    default_assignee: s.default_assignee ?? null,
    deadline: s.deadline ?? null,
    // Sent as a set: a language the user cleared is simply not in it. Sorted
    // so two drafts holding the same overrides compare equal in
    // sameWorkflowSteps, whatever order the keys were written in.
    language_deadlines: Object.entries(s.language_deadlines ?? {})
      .filter(([, deadline]) => !!deadline)
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([target_language, deadline]) => ({ target_language, deadline })),
  }))
}

/** True when two step drafts would produce the same request — the editor's
 *  own keys and row ids don't count. The steps editor reports its draft on
 *  mount and after every external sync, not only on user edits, so a caller
 *  that reacts to "the user changed the steps" needs this comparison. */
export function sameWorkflowSteps(a: WorkflowStep[], b: WorkflowStep[]): boolean {
  return JSON.stringify(workflowStepsToPayload(a)) === JSON.stringify(workflowStepsToPayload(b))
}

/** The step fields profile-step API responses carry (GET settings, GET
 *  template workflow-steps) — a structural subset of the generated WorkflowStep. */
interface ApiWorkflowStep {
  id?: number
  name: string
  step_type?: string
  step_definition_id?: number | null
  default_assignee?: number | null
  deadline?: string | null
  ai_router_id?: number | null
  language_deadlines?: { target_language: string; deadline: string }[] | null
}

/** API step rows → editor drafts. Read-side twin of workflowStepsToPayload. */
export function workflowStepsFromApi(steps: ApiWorkflowStep[]): WorkflowStep[] {
  return steps.map(s => ({
    id: s.id,
    step_definition_id: s.step_definition_id ?? null,
    name: s.name,
    step_type: (s.step_type ?? (s.ai_router_id != null ? 'hybrid' : 'human')) as StepType,
    ai_router_id: s.ai_router_id ?? null,
    default_assignee: s.default_assignee ?? undefined,
    deadline: s.deadline ?? null,
    language_deadlines: Object.fromEntries(
      (s.language_deadlines ?? []).map(ld => [ld.target_language, ld.deadline]),
    ),
  }))
}

/** Library workflow rows → editor drafts (no deadline — library steps carry
 *  none). Used by the workflow edit dialog and the create form's workflow prefill. */
export function workflowDefinitionStepsToDrafts(steps: WorkflowDefinitionStepRow[]): WorkflowStep[] {
  return steps.map(s => ({
    step_definition_id: s.step_definition_id,
    name: s.name,
    step_type: s.step_type,
    ai_router_id: s.ai_router_id,
    default_assignee: s.default_assignee ?? undefined,
    deadline: null,
  }))
}

/** Editor drafts → library workflow input. Every draft must be a library
 *  reference (the library editor never offers legacy rows). */
export function workflowDraftsToLibraryInput(name: string, steps: WorkflowStep[]) {
  return {
    name,
    steps: steps
      .filter(s => s.step_definition_id != null)
      .map(s => ({ step_definition_id: s.step_definition_id as number, default_assignee: s.default_assignee ?? null })),
  }
}
