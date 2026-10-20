import { describe, it, expect } from 'vitest'
import { buildProjectCreatePayload, buildProjectSetupPayload, type ProjectCreateForm } from './project-create-payload'
import { DEFAULT_SETTINGS } from '../hooks/useProjectCreateSettings'

const form: ProjectCreateForm = {
  name: '  Alpha  ',
  sourceLanguage: 'en',
  targetLanguages: ['fr', 'de'],
  customerId: 3,
  deadline: null,
  domain: ' legal ',
  templateId: '5',
  workflowId: '',
}

describe('buildProjectCreatePayload', () => {
  it('trims free text and maps ids, leaving absent optionals undefined', () => {
    const p = buildProjectCreatePayload(form, DEFAULT_SETTINGS)
    expect(p.name).toBe('Alpha')
    expect(p.domain).toBe('legal')
    expect(p.customer_id).toBe(3)
    expect(p.deadline).toBeNull()
    expect(p.template_id).toBe(5)
    expect(p.workflow_id).toBeUndefined()
    expect(p.workflow_steps).toBeUndefined()
    expect(p.settings).toMatchObject({ fuzzy_threshold: 0.75, mt_on_upload: true, tm_on_upload: true, qa_profile_id: null })
  })

  it('sends an empty form as empty — a draft may have nothing yet', () => {
    const p = buildProjectCreatePayload({ ...form, name: '', sourceLanguage: '', targetLanguages: [], customerId: null, domain: '', templateId: '' }, DEFAULT_SETTINGS)
    expect(p.name).toBe('')
    expect(p.source_language).toBe('')
    expect(p.target_languages).toEqual([])
    expect(p.customer_id).toBeNull()
    expect(p.template_id).toBeUndefined()
    expect(p.tm_assignments).toEqual([])
  })

  it('omits TM rows when the form follows a TM profile', () => {
    const s = { ...DEFAULT_SETTINGS, tmProfileId: 9, tmAssignments: [{ id: 1, tmId: 4, mode: 'read', priority: 1, penaltyPct: 0 }] }
    const p = buildProjectCreatePayload(form, s)
    expect(p.settings?.tm_profile_id).toBe(9)
    expect(p.tm_assignments).toBeUndefined()

    const own = buildProjectCreatePayload(form, { ...s, tmProfileId: null })
    expect(own.settings?.tm_profile_id).toBeUndefined()
    expect(own.tm_assignments).toEqual([{ tm_id: 4, mode: 'read', priority: 1, penalty_pct: 0 }])
  })
})

// "Strings contain HTML" off in the wizard must reach the server, or the new
// project parses its files as HTML.
describe('buildProjectSetupPayload: inline_html', () => {
  it('sends false when the draft turned it off', () => {
    expect(buildProjectSetupPayload('', { ...DEFAULT_SETTINGS, inlineHTML: false }).settings.inline_html).toBe(false)
  })

  it('sends true by default', () => {
    expect(buildProjectSetupPayload('', DEFAULT_SETTINGS).settings.inline_html).toBe(true)
  })
})
