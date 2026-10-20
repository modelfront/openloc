// useProjectCreateSettings — the New Project page's settings + workflow draft.
//
// Two invariants worth pinning. The step draft lives only in the ref (the
// Workflow card owns it), while the settings editor emits whole SettingsValues
// objects that still carry the workflowSteps it mounted with — so a scalar or
// TM change arriving after a workflow pick must not clobber the steps, and a
// workflow pick must not clobber a TM added before it. And the seed-once
// effect must pick the first library workflow exactly once, never over a deep
// link.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import type { ReactNode } from 'react'
import { useProjectCreateSettings, workflowStepsError, pretranslateRouterError, DEFAULT_SETTINGS } from './useProjectCreateSettings'
import type { WorkflowStep } from '../components/WorkflowStepsEditor'
import { buildProjectCreatePayload } from '../lib/project-create-payload'

const api = vi.hoisted(() => ({
  listStepDefinitions: vi.fn(),
  listWorkflows: vi.fn(),
  getWorkflow: vi.fn(),
  getTemplate: vi.fn(),
  getProjectSettings: vi.fn(),
}))
vi.mock('../api', () => api)

const translation = { id: 11, name: 'Translation', step_type: 'human', ai_router_id: null, ai_router_name: null, workflow_count: 1 }
const review = { id: 12, name: 'Review', step_type: 'human', ai_router_id: null, ai_router_name: null, workflow_count: 1 }
const libraryStep = (step_definition_id: number, name: string) => ({
  id: 1, workflow_id: 7, step_order: 1, default_assignee: null, step_definition_id, name,
  step_type: 'human', role_required: 'translator', ai_router_id: null, definition_deleted: false, assignee_name: null,
})
const draft = (name: string): WorkflowStep => ({ step_definition_id: 11, name, step_type: 'human' })

function setup(initialEntry = '/projects/new') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[initialEntry]}>{children}</MemoryRouter>
    </QueryClientProvider>
  )
  const applyLanguages = vi.fn()
  const applyMetadata = vi.fn()
  return {
    applyLanguages,
    ...renderHook(() => useProjectCreateSettings({ ready: false, applyLanguages, applyMetadata }), { wrapper }),
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  api.listStepDefinitions.mockResolvedValue([translation, review])
  api.listWorkflows.mockResolvedValue([])
})

describe('useProjectCreateSettings — draft ownership', () => {
  it('a settings change after a workflow pick keeps the picked steps', async () => {
    api.listWorkflows.mockResolvedValue([{ id: 7, name: 'Standard' }])
    api.getWorkflow.mockResolvedValue({ id: 7, name: 'Standard', steps: [libraryStep(11, 'QE')] })
    const { result } = setup()
    await waitFor(() => expect(result.current.currentSettings().workflowSteps.map(x => x.name)).toEqual(['QE']))

    // The editor's payload still carries the steps it mounted with (empty).
    act(() => result.current.handleSettingsChange({ ...DEFAULT_SETTINGS, mtOnUpload: false, workflowSteps: [] }))

    const s = result.current.currentSettings()
    expect(s.mtOnUpload).toBe(false)
    expect(s.workflowSteps.map(x => x.name)).toEqual(['QE'])
  })

  it('a workflow pick after adding a TM keeps the TM assignment', async () => {
    api.getWorkflow.mockResolvedValue({ id: 7, name: 'Standard', steps: [libraryStep(11, 'QE')] })
    const { result } = setup()
    await waitFor(() => expect(result.current.currentSettings().workflowSteps).toHaveLength(2))

    act(() => result.current.handleSettingsChange({
      ...DEFAULT_SETTINGS,
      tmAssignments: [{ id: 0, tmId: 3, mode: 'read', priority: 0, penaltyPct: 0 }],
    }))
    await act(async () => { await result.current.handleWorkflowChange('7') })

    const s = result.current.currentSettings()
    expect(s.tmAssignments).toHaveLength(1)
    expect(s.workflowSteps.map(x => x.name)).toEqual(['QE'])
  })
})

describe('useProjectCreateSettings — seeding', () => {
  it('pre-selects the first library workflow once', async () => {
    api.listWorkflows.mockResolvedValue([{ id: 7, name: 'Standard' }, { id: 8, name: 'Other' }])
    api.getWorkflow.mockImplementation((id: number) => Promise.resolve(
      id === 7
        ? { id: 7, name: 'Standard', steps: [libraryStep(11, 'Translation'), libraryStep(12, 'Review')] }
        : { id: 8, name: 'Other', steps: [libraryStep(12, 'Review')] },
    ))
    const { result, rerender } = setup()

    await waitFor(() => expect(result.current.workflowId).toBe('7'))
    await waitFor(() => expect(result.current.settings.workflowSteps.map(s => s.name)).toEqual(['Translation', 'Review']))
    expect(api.getWorkflow).toHaveBeenCalledTimes(1)
    expect(api.getWorkflow).toHaveBeenCalledWith(7)

    // A refetch / re-render after the user picked another one must not re-seed.
    await act(async () => { await result.current.handleWorkflowChange('8') })
    rerender()
    expect(result.current.workflowId).toBe('8')
    expect(result.current.settings.workflowSteps.map(s => s.name)).toEqual(['Review'])
    expect(api.getWorkflow).toHaveBeenCalledTimes(2)
  })

  it('falls back to the library Translation → Review steps when the org has no workflows', async () => {
    const { result } = setup()
    await waitFor(() => expect(result.current.settings.workflowSteps.map(s => s.step_definition_id)).toEqual([11, 12]))
    expect(result.current.workflowId).toBe('')
    expect(api.getWorkflow).not.toHaveBeenCalled()
  })

  it('leaves a Duplicate deep link alone', async () => {
    api.listWorkflows.mockResolvedValue([{ id: 7, name: 'Standard' }])
    const { result } = setup('/projects/new?fromTemplate=5')
    // Both libraries have loaded (the seed effect has had its chance)…
    await waitFor(() => expect(result.current.workflows).toHaveLength(1))
    await act(async () => { await Promise.resolve() })
    // …yet nothing was seeded: the prefill owns the steps.
    expect(api.getWorkflow).not.toHaveBeenCalled()
    expect(result.current.workflowId).toBe('')
    expect(result.current.settings.workflowSteps).toEqual([])
  })
})

describe('useProjectCreateSettings — start from', () => {
  const templateDetail = {
    template: { id: 5, name: 'Marketing', fuzzy_threshold: 0.6, mt_on_upload: false, tm_on_upload: true, workflow_id: null, project_name: 'Ads project', source_language: 'de', target_languages: ['en', 'it'], customer_id: 4, domain: 'ads' },
    tm_assignments: [], mt_assignments: [], term_assignments: [], workflow_steps: [], tm_profile: null,
  }
  const projectSettings = {
    project: { id: 3, name: 'Source project', domain: 'legal', customer_id: 9, source_language: 'en', target_languages: ['fr'], fuzzy_threshold: 0.65, mt_on_upload: true, tm_on_upload: true, workflow_id: null },
    tm_assignments: [], mt_assignments: [], term_assignments: [], workflow_steps: [],
  }

  it('From scratch after a template prefill restores the defaults and re-seeds the workflow', async () => {
    api.listWorkflows.mockResolvedValue([{ id: 7, name: 'Standard' }])
    api.getWorkflow.mockResolvedValue({ id: 7, name: 'Standard', steps: [libraryStep(11, 'Translation')] })
    api.getTemplate.mockResolvedValue(templateDetail)
    const { result } = setup()
    await waitFor(() => expect(result.current.workflowId).toBe('7'))

    await act(async () => { await result.current.handleTemplateChange('5') })
    expect(result.current.templateId).toBe('5')
    expect(result.current.currentSettings().fuzzyThreshold).toBe(0.6)
    // The template has no library workflow, so the link is gone…
    expect(result.current.workflowId).toBe('')

    await act(async () => { await result.current.resetToDefaults() })
    expect(result.current.templateId).toBe('')
    expect(result.current.hasStartFromDeepLink).toBe(false)
    expect(result.current.currentSettings().fuzzyThreshold).toBe(DEFAULT_SETTINGS.fuzzyThreshold)
    // …and From scratch brings back the same first workflow a fresh page gets.
    expect(result.current.workflowId).toBe('7')
    expect(result.current.currentSettings().workflowSteps.map(s => s.name)).toEqual(['Translation'])
    expect(api.getWorkflow).toHaveBeenCalledTimes(2)
  })

  it('a ?fromProject deep link names the source project; a template pick replaces it', async () => {
    api.getProjectSettings.mockResolvedValue(projectSettings)
    api.getTemplate.mockResolvedValue(templateDetail)
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/projects/new?fromProject=3']}>{children}</MemoryRouter>
      </QueryClientProvider>
    )
    const applyLanguages = vi.fn()
    const applyMetadata = vi.fn()
    const { result } = renderHook(() => useProjectCreateSettings({ ready: true, applyLanguages, applyMetadata }), { wrapper })

    expect(result.current.hasStartFromDeepLink).toBe(true)
    await waitFor(() => expect(result.current.fromProjectName).toBe('Source project'))
    expect(result.current.fromProjectId).toBe('3')
    expect(applyLanguages).toHaveBeenCalledWith('en', ['fr'])
    expect(applyMetadata).toHaveBeenCalledWith({ name: 'Source project', domain: 'legal', customerId: 9 })
    expect(result.current.currentSettings().fuzzyThreshold).toBe(0.65)

    await act(async () => { await result.current.handleTemplateChange('5') })
    expect(result.current.fromProjectId).toBe('')
    expect(result.current.fromProjectName).toBe('')
    expect(result.current.templateId).toBe('5')
    // The template brings its project name, customer and domain, and its own
    // languages take precedence over anything derived from assignments.
    expect(applyMetadata).toHaveBeenLastCalledWith({ name: 'Ads project', domain: 'ads', customerId: 4 })
    expect(applyLanguages).toHaveBeenLastCalledWith('de', ['en', 'it'])
  })
})

describe('workflowStepsError', () => {
  it('requires a step or a linked workflow', () => {
    expect(workflowStepsError([], '')).toMatch(/pick a workflow/i)
    expect(workflowStepsError([], '7')).toBeNull()
    expect(workflowStepsError([draft('Translation')], '')).toBeNull()
  })

  it('names the unpicked rows', () => {
    const unpicked: WorkflowStep = { step_definition_id: null, name: '', step_type: 'human' }
    expect(workflowStepsError([draft('Translation'), unpicked, unpicked], '')).toBe('Pick a step for workflow row 2, 3 or remove it')
  })
})

// A template's match scoring must survive the wizard. The wizard always sends
// its own `settings` block next to `template_id`, and the server lets those
// values win, so a scoring field dropped by the prefill silently reverts the
// template's choice to the default.
describe('useProjectCreateSettings — template prefill carries match scoring', () => {
  it('a By TM order template with custom penalties reaches the create payload', async () => {
    api.getTemplate.mockResolvedValue({
      template: {
        id: 5, name: 'Ordered', fuzzy_threshold: 0.8, mt_on_upload: false, tm_on_upload: true,
        sublanguage_penalty_pct: 7, near_exact_penalty_pct: 3, tm_lookup_order: 'tm_order',
        source_language: 'en', target_languages: ['fr'],
      },
      tm_assignments: [], term_assignments: [], workflow_steps: [], tm_profile: null,
    })
    const { result } = setup()

    await act(async () => { await result.current.handleTemplateChange('5') })

    await waitFor(() => expect(result.current.currentSettings().tmLookupOrder).toBe('tm_order'))
    const payload = buildProjectCreatePayload({
      name: 'P', sourceLanguage: 'en', targetLanguages: ['fr'], customerId: null,
      deadline: null, domain: '', templateId: '5', workflowId: '',
    }, result.current.currentSettings())
    expect(payload.template_id).toBe(5)
    expect(payload.settings).toMatchObject({
      tm_lookup_order: 'tm_order', sublanguage_penalty_pct: 7, near_exact_penalty_pct: 3, fuzzy_threshold: 0.8,
    })
  })
})

describe('pretranslateRouterError', () => {
  const pre: WorkflowStep = { step_definition_id: 9, name: 'Pre-translation', step_type: 'pretranslate' }

  it('asks for an MT router when Pre-translation has none', () => {
    expect(pretranslateRouterError({ ...DEFAULT_SETTINGS, workflowSteps: [pre], mtRouterId: null }))
      .toBe('Choose an MT router: the Pre-translation workflow fills files in with machine translation.')
  })

  it('passes with a router, or with any other workflow', () => {
    expect(pretranslateRouterError({ ...DEFAULT_SETTINGS, workflowSteps: [pre], mtRouterId: 3 })).toBeNull()
    expect(pretranslateRouterError({ ...DEFAULT_SETTINGS, workflowSteps: [draft('Translation')], mtRouterId: null })).toBeNull()
  })
})
