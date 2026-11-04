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

  it('sends the picked segmentation profile, and none for the server to pick while unpicked', () => {
    expect(buildProjectCreatePayload(form, { ...DEFAULT_SETTINGS, segmentationProfileId: 12 }).settings?.segmentation_profile_id).toBe(12)
    expect(buildProjectSetupPayload('', { ...DEFAULT_SETTINGS, segmentationProfileId: 12 }).settings.segmentation_profile_id).toBe(12)
    expect(buildProjectCreatePayload(form, DEFAULT_SETTINGS).settings).not.toHaveProperty('segmentation_profile_id', expect.anything())
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

// The wizard sends every format: its pick, or null for the Default, so a
// template's pick the user set back to Default is not kept by the server.
describe('buildProjectSetupPayload: filter_profile_ids', () => {
  it('sends the picks and null for every other format', () => {
    expect(buildProjectSetupPayload('', { ...DEFAULT_SETTINGS, filterProfileIds: { xlsx: 7 } }).settings.filter_profile_ids)
      .toEqual({ html: null, xlsx: 7, json_i18n: null, po: null, txt: null, xml: null })
  })

  it('sends null everywhere by default', () => {
    expect(Object.values(buildProjectSetupPayload('', DEFAULT_SETTINGS).settings.filter_profile_ids)).toEqual([null, null, null, null, null, null])
  })
})

// "Linguists can add terms" picked in the wizard must reach the server.
describe('buildProjectSetupPayload: linguist_terms', () => {
  it('sends the picked value', () => {
    expect(buildProjectSetupPayload('', { ...DEFAULT_SETTINGS, linguistTerms: 'off' }).settings.linguist_terms).toBe('off')
  })

  it('sends pending by default', () => {
    expect(buildProjectSetupPayload('', DEFAULT_SETTINGS).settings.linguist_terms).toBe('pending')
  })
})

// A router cleared in the wizard must reach the server as null, or a template's
// router stays on the project.
describe('buildProjectSetupPayload: mt_router_id', () => {
  it('sends null when the router is cleared', () => {
    const p = buildProjectSetupPayload('', { ...DEFAULT_SETTINGS, mtRouterId: null })
    expect(p.settings).toHaveProperty('mt_router_id', null)
  })

  it('sends the picked router', () => {
    expect(buildProjectSetupPayload('', { ...DEFAULT_SETTINGS, mtRouterId: 7 }).settings.mt_router_id).toBe(7)
  })
})
