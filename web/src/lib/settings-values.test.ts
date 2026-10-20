import { describe, it, expect } from 'vitest'
import { projectSettingsToValues } from './settings-values'
import type { ProjectSettings } from '../api'
import type { WorkflowStep } from '../types/generated'

const baseProject = {
  id: 1,
  name: 'P',
  description: '',
  source_language: 'en',
  target_languages: ['de'],
  template_id: null,
  fuzzy_threshold: 0.85,
  mt_on_upload: true,
  tm_on_upload: false,
  qa_profile_id: 7,
  created_at: '',
  updated_at: '',
} as ProjectSettings['project']

function settingsWith(partial: Partial<ProjectSettings>): ProjectSettings {
  return {
    project: baseProject,
    tm_assignments: [],
    term_assignments: [],
    workflow_steps: [],
    ...partial,
  }
}

describe('projectSettingsToValues', () => {
  it('maps scalars from the project record', () => {
    const v = projectSettingsToValues(settingsWith({}))
    expect(v.fuzzyThreshold).toBe(0.85)
    expect(v.mtOnUpload).toBe(true)
    expect(v.tmOnUpload).toBe(false)
    expect(v.qaProfileId).toBe(7)
  })

  it('maps the MT router pointer and name, null when the project has none', () => {
    const v = projectSettingsToValues(settingsWith({ project: { ...baseProject, mt_router_id: 4, mt_router_name: 'Production MT' } }))
    expect(v.mtRouterId).toBe(4)
    expect(v.mtRouterName).toBe('Production MT')
    const none = projectSettingsToValues(settingsWith({}))
    expect(none.mtRouterId).toBeNull()
    expect(none.mtRouterName).toBeUndefined()
  })

  it('resolves term base names, with a #id fallback for unknown bases', () => {
    const v = projectSettingsToValues(
      settingsWith({
        term_assignments: [
          { id: 1, project_id: 1, term_base_id: 3, source_language: 'en', target_language: 'de', mode: 'enforce', priority: 1 },
          { id: 2, project_id: 1, term_base_id: 4, source_language: 'en', target_language: 'de', mode: 'read', priority: 2 },
        ],
      }),
      [{ id: 3, name: 'Legal Terms' }],
    )
    expect(v.termAssignments[0].resourceName).toBe('Legal Terms')
    expect(v.termAssignments[1].resourceName).toBe('Term base #4')
  })

  it('preserves workflow step ai_router_id and normalizes null assignee', () => {
    const v = projectSettingsToValues(settingsWith({
      workflow_steps: [
        { id: 1, settings_profile_id: 9, name: 'translation', step_order: 0, default_assignee: null, ai_router_id: null } as unknown as WorkflowStep,
        { id: 2, settings_profile_id: 9, name: 'ai review', step_order: 1, default_assignee: 5, ai_router_id: 12 } as unknown as WorkflowStep,
      ],
    }))
    expect(v.workflowSteps).toEqual([
      { id: 1, step_definition_id: null, name: 'translation', step_type: 'human', default_assignee: undefined, deadline: null, ai_router_id: null, language_deadlines: {} },
      { id: 2, step_definition_id: null, name: 'ai review', step_type: 'hybrid', default_assignee: 5, deadline: null, ai_router_id: 12, language_deadlines: {} },
    ])
  })

  it('preserves workflow step deadline', () => {
    const v = projectSettingsToValues(settingsWith({
      workflow_steps: [
        { id: 1, settings_profile_id: 9, name: 'translation', step_type: 'human', step_definition_id: null, step_order: 0, created_at: '2026-08-26T00:00:00Z', deadline: '2026-08-27T14:50:00Z' } as unknown as WorkflowStep,
      ],
    }))
    expect(v.workflowSteps[0].deadline).toBe('2026-08-27T14:50:00Z')
  })

  it('preserves a step\'s per-target-language deadlines', () => {
    const v = projectSettingsToValues(settingsWith({
      workflow_steps: [
        {
          id: 1, settings_profile_id: 9, name: 'translation', step_type: 'human', step_definition_id: null,
          step_order: 0, created_at: '2026-08-26T00:00:00Z', deadline: '2026-08-27T14:50:00Z',
          language_deadlines: [{ target_language: 'de', deadline: '2026-08-25T09:00:00Z' }],
        } as unknown as WorkflowStep,
      ],
    }))
    expect(v.workflowSteps[0].language_deadlines).toEqual({ de: '2026-08-25T09:00:00Z' })
  })
})

describe('projectSettingsToValues — TM profile link', () => {
  it('carries the linked profile id + name; rows and scoring are whatever the server sent (the effective values)', () => {
    const v = projectSettingsToValues(settingsWith({
      tm_profile: { id: 7, name: 'Acme' },
      tm_assignments: [{ id: 1, settings_profile_id: 9, tm_id: 5, mode: 'read', priority: 0, penalty_pct: 2 }],
    }))
    expect(v.tmProfileId).toBe(7)
    expect(v.tmProfileName).toBe('Acme')
    expect(v.fuzzyThreshold).toBe(0.85)
    expect(v.tmAssignments).toEqual([{ id: 1, tmId: 5, mode: 'read', priority: 0, penaltyPct: 2 }])
  })

  it('maps a missing or null tm_profile to "custom"', () => {
    expect(projectSettingsToValues(settingsWith({})).tmProfileId).toBeNull()
    expect(projectSettingsToValues(settingsWith({ tm_profile: null })).tmProfileId).toBeNull()
    expect(projectSettingsToValues(settingsWith({})).tmProfileName).toBeUndefined()
  })
})
