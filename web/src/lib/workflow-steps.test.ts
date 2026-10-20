import { describe, it, expect } from 'vitest'
import {
  workflowStepsFromApi, workflowStepsToPayload, workflowDefinitionStepsToDrafts, workflowDraftsToLibraryInput, sameWorkflowSteps,
  workflowMode,
} from './workflow-steps'
import type { WorkflowStep } from '@/components/WorkflowStepsEditor'

// Every workflow-steps writer goes through this ONE mapper.
// Identity travels as a library reference, never as free text; per-position
// fields are the only editable ones.
describe('workflowStepsToPayload', () => {
  it('sends a library reference plus the per-position fields, explicit nulls for absent ones', () => {
    expect(workflowStepsToPayload([{
      id: 40, step_definition_id: 7, name: 'Review', step_type: 'human',
      default_assignee: 12, deadline: '2026-09-03T17:00:00Z',
    }, {
      step_definition_id: 8, name: 'Auto APE', step_type: 'ai', ai_router_id: 3,
    }])).toEqual([
      { step_definition_id: 7, existing_step_id: undefined, default_assignee: 12, deadline: '2026-09-03T17:00:00Z', language_deadlines: [] },
      { step_definition_id: 8, existing_step_id: undefined, default_assignee: null, deadline: null, language_deadlines: [] },
    ])
    // JSON.stringify drops undefined keys; the backend reads an absent key as
    // "keep current", so clearing a field must serialize as null.
    const json = JSON.parse(JSON.stringify(workflowStepsToPayload([{ step_definition_id: 8, name: 'x', step_type: 'human' }])[0]))
    expect(json).toEqual({ step_definition_id: 8, default_assignee: null, deadline: null, language_deadlines: [] })
  })

  it('carries a pre-library row forward through existing_step_id, never as a name', () => {
    const [p] = workflowStepsToPayload([{ id: 41, step_definition_id: null, name: 'LQA', step_type: 'hybrid', ai_router_id: 5 }])
    expect(p).toEqual({ step_definition_id: undefined, existing_step_id: 41, default_assignee: null, deadline: null, language_deadlines: [] })
    expect(JSON.parse(JSON.stringify(p))).not.toHaveProperty('name')
  })
})

describe('workflowStepsFromApi', () => {
  it('maps every identity + position field, deriving step_type for pre-library rows', () => {
    expect(workflowStepsFromApi([
      { id: 1, name: 'Translation', step_type: 'human', step_definition_id: 9, default_assignee: null, deadline: '2026-08-27T14:50:00Z', ai_router_id: null },
      // A row from before step_type existed: an engine means hybrid.
      { id: 2, name: 'ai review', default_assignee: 5, ai_router_id: 12 },
    ])).toEqual([
      { id: 1, step_definition_id: 9, name: 'Translation', step_type: 'human', ai_router_id: null, default_assignee: undefined, deadline: '2026-08-27T14:50:00Z', language_deadlines: {} },
      { id: 2, step_definition_id: null, name: 'ai review', step_type: 'hybrid', ai_router_id: 12, default_assignee: 5, deadline: null, language_deadlines: {} },
    ])
  })

  it('load → save round-trips the position fields unchanged', () => {
    const api = [{
      id: 3, name: 'Review', step_type: 'human', step_definition_id: 4, default_assignee: 12,
      deadline: '2026-08-27T14:50:00Z', ai_router_id: null,
      language_deadlines: [{ target_language: 'de', deadline: '2026-08-25T14:50:00Z' }],
    }]
    expect(workflowStepsToPayload(workflowStepsFromApi(api))).toEqual([
      {
        step_definition_id: 4, existing_step_id: undefined, default_assignee: 12,
        deadline: '2026-08-27T14:50:00Z',
        language_deadlines: [{ target_language: 'de', deadline: '2026-08-25T14:50:00Z' }],
      },
    ])
  })
})

// A step's due date can differ per target language; the wire shape is a
// complete set, so a language the user cleared is simply not in it.
describe('per-language deadlines', () => {
  it('sends the set sorted, dropping languages with no date', () => {
    const [p] = workflowStepsToPayload([{
      step_definition_id: 7, name: 'Translation', step_type: 'human',
      deadline: '2026-09-03T17:00:00Z',
      language_deadlines: { fr: '2026-09-04T17:00:00Z', de: '2026-09-02T09:00:00Z', es: '' },
    }])
    expect(p.language_deadlines).toEqual([
      { target_language: 'de', deadline: '2026-09-02T09:00:00Z' },
      { target_language: 'fr', deadline: '2026-09-04T17:00:00Z' },
    ])
  })

  it('counts as a change in sameWorkflowSteps, but key order does not', () => {
    const base: WorkflowStep = { id: 40, step_definition_id: 7, name: 'Translation', step_type: 'human' }
    const de = { ...base, language_deadlines: { de: '2026-09-02T09:00:00Z' } }
    const deThenFr = { ...base, language_deadlines: { de: '2026-09-02T09:00:00Z', fr: '2026-09-04T17:00:00Z' } }
    const frThenDe = { ...base, language_deadlines: { fr: '2026-09-04T17:00:00Z', de: '2026-09-02T09:00:00Z' } }
    expect(sameWorkflowSteps([base], [de])).toBe(false)
    expect(sameWorkflowSteps([de], [deThenFr])).toBe(false)
    expect(sameWorkflowSteps([deThenFr], [frThenDe])).toBe(true)
  })
})

describe('library workflow mappers', () => {
  const rows = [
    { id: 1, workflow_id: 1, step_order: 0, default_assignee: null, step_definition_id: 7, name: 'Auto APE', step_type: 'ai' as const, role_required: 'linguist', ai_router_id: 3, definition_deleted: false, assignee_name: null },
    { id: 2, workflow_id: 1, step_order: 1, default_assignee: 12, step_definition_id: 8, name: 'Review', step_type: 'human' as const, role_required: 'linguist', ai_router_id: null, definition_deleted: false, assignee_name: 'Rita' },
  ]
  it('drafts carry identity + assignee and no deadline', () => {
    expect(workflowDefinitionStepsToDrafts(rows)).toEqual([
      { step_definition_id: 7, name: 'Auto APE', step_type: 'ai', ai_router_id: 3, default_assignee: undefined, deadline: null },
      { step_definition_id: 8, name: 'Review', step_type: 'human', ai_router_id: null, default_assignee: 12, deadline: null },
    ])
  })
  it('library input keeps only library references, in order', () => {
    const drafts = workflowDefinitionStepsToDrafts(rows)
    expect(workflowDraftsToLibraryInput('Std', [...drafts, { step_definition_id: null, name: '', step_type: 'human' }])).toEqual({
      name: 'Std',
      steps: [{ step_definition_id: 7, default_assignee: null }, { step_definition_id: 8, default_assignee: 12 }],
    })
  })
})

// The wizard's autosave asks "did the user change the steps?" on every report
// the steps editor makes — including the one it makes on mount and after every
// prefill. What counts as a change is what would reach the server.
describe('sameWorkflowSteps', () => {
  const translation: WorkflowStep = { id: 40, step_definition_id: 7, name: 'Translation', step_type: 'human' }
  const review: WorkflowStep = { id: 41, step_definition_id: 8, name: 'Review', step_type: 'human' }

  it('is true for the same list', () => {
    expect(sameWorkflowSteps([translation, review], [translation, review])).toBe(true)
  })

  it('ignores the editor bookkeeping that never leaves the browser', () => {
    // A row's own id only travels for a pre-library step (one with no
    // step_definition_id), so changing it here is invisible on the wire — as
    // is the name, which comes from the definition the reference points at.
    expect(sameWorkflowSteps(
      [{ ...translation, id: 999, name: 'Translation (renamed in the picker)' }],
      [translation],
    )).toBe(true)
  })

  it('is false when the step, the order, the assignee or the deadline changes', () => {
    expect(sameWorkflowSteps([translation], [review])).toBe(false)
    expect(sameWorkflowSteps([translation, review], [review, translation])).toBe(false)
    expect(sameWorkflowSteps([translation], [{ ...translation, default_assignee: 12 }])).toBe(false)
    expect(sameWorkflowSteps([translation], [{ ...translation, deadline: '2026-09-03T17:00:00Z' }])).toBe(false)
  })

  it('is false when a step is added or removed', () => {
    expect(sameWorkflowSteps([translation], [translation, review])).toBe(false)
    expect(sameWorkflowSteps([translation, review], [])).toBe(false)
  })

  it('distinguishes two pre-library rows by their own ids', () => {
    // No library reference, so `id` IS the identity that travels.
    const own: WorkflowStep = { id: 5, step_definition_id: null, name: 'Legacy', step_type: 'human' }
    expect(sameWorkflowSteps([own], [{ ...own }])).toBe(true)
    expect(sameWorkflowSteps([own], [{ ...own, id: 6 }])).toBe(false)
  })
})

describe('workflowMode', () => {
  it('is raw_mt for the built-in Pre-translation step alone', () => {
    expect(workflowMode([{ step_type: 'pretranslate' }])).toBe('raw_mt')
  })

  it('is workflow for anything else, automated steps and an empty list included', () => {
    expect(workflowMode([{ step_type: 'ai' }])).toBe('workflow')
    expect(workflowMode([{ step_type: 'pretranslate' }, { step_type: 'ai' }])).toBe('workflow')
    expect(workflowMode([{ step_type: 'human' }])).toBe('workflow')
    expect(workflowMode([])).toBe('workflow')
  })
})
