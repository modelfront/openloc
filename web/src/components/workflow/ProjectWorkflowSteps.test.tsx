// A project's steps on the Workflow tab: read-only until Edit steps, then
// one draft.
//
// Pinned:
//   - The steps and their deadlines (per language behind the globe) are shown
//     read-only; Edit steps opens the editor and shuts the workflow picker.
//   - Nothing saves on its own: a step, an assignee or a deadline waits for
//     Save changes, which asks for the preview; only the confirmation saves.
//   - Discard puts the saved steps back and leaves the editor.
//   - A refused preview keeps the draft and re-reads the steps (their locks).
//   - A refetch while editing keeps the edits and an open date picker.
//   - The confirmation's summary line counts the step changes.

import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import { render, screen, cleanup, act, fireEvent, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { toast } from 'sonner'
import type { WorkflowPreview, StepDefinitionRow, Member } from '../../api'
import type { WorkflowStep } from '../WorkflowStepsEditor'
import { ProjectWorkflowSteps, previewSummary } from './ProjectWorkflowSteps'
import { pickDeadline, openPicker, clickDay, typeTime, clickDone, PICKER_FLOW_TIMEOUT } from '@/test/deadline-picker'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('@/components/ui/combobox', async () => (await import('@/test/combobox-mock')).comboboxSelectMock)

beforeEach(() => vi.useFakeTimers())
afterEach(() => { vi.useRealTimers(); cleanup(); vi.clearAllMocks() })

const def = (id: number, name: string): StepDefinitionRow => ({
  id, tenant_id: 1, name, step_type: 'human', role_required: 'linguist', ai_router_id: undefined,
  created_at: '2026-01-01', updated_at: '2026-01-01', deleted_at: '', ai_router_name: null, workflow_count: 0,
})
const pm: Member = { id: 3, email: 'pm@t', name: 'Pat (manager)', role: 'project_manager', created_at: '2026-01-01' }

const saved: WorkflowStep[] = [
  { id: 1, step_definition_id: 1, name: 'Translation', step_type: 'human', deadline: null, locked: true, reached_jobs: 1 },
  { id: 2, step_definition_id: 2, name: 'Review', step_type: 'human', deadline: null, locked: false, reached_jobs: 0 },
]

const preview: WorkflowPreview = {
  added: 1, removed: 0, changed: 0, open_jobs: 1, start_now: [],
  effects: ['1 job is in progress. Each one follows the new steps when it gets to them.'],
}

function setup(over: { onPreview?: () => Promise<WorkflowPreview>; onSave?: (steps: WorkflowStep[]) => Promise<void>; steps?: WorkflowStep[] } = {}) {
  const onDraftOpen = vi.fn()
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(['step-definitions'], [def(1, 'Translation'), def(2, 'Review'), def(3, 'Proofread')])
  qc.setQueryData(['members'], [pm])
  qc.setQueryData(['users-by-pair', 'en', 'de'], [])
  qc.setQueryData(['languages'], [])
  qc.setQueryData(['ai-routers'], [])
  const onSave = vi.fn<(steps: WorkflowStep[]) => Promise<void>>(over.onSave ?? (() => Promise.resolve()))
  const onPreview = vi.fn<(steps: WorkflowStep[]) => Promise<WorkflowPreview>>(over.onPreview ?? (() => Promise.resolve(preview)))
  const onRefresh = vi.fn()
  const ui = (steps: WorkflowStep[]) => (
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <ProjectWorkflowSteps steps={steps} sourceLang="en" targetLangs={['de']} onSave={onSave} onPreview={onPreview} onRefresh={onRefresh} onDraftOpen={onDraftOpen} />
      </MemoryRouter>
    </QueryClientProvider>
  )
  const { rerender } = render(ui(over.steps ?? saved))
  /** The saved steps arriving again, as new objects: a refetch. */
  const refetch = (steps: WorkflowStep[]) => rerender(ui(steps))
  return { onSave, onPreview, onRefresh, onDraftOpen, refetch }
}

const click = (testId: string) => act(() => { fireEvent.click(screen.getByTestId(testId)) })
const pick = (testId: string, value: string) => act(() => { fireEvent.change(screen.getByTestId(testId), { target: { value } }) })

/** Adds Proofread at the end: a step change. */
function addProofread() {
  click('workflow-add-step')
  pick('workflow-step-picker-2', '3')
}

/** Opens the editor. */
const edit = () => click('workflow-steps-edit')

describe('ProjectWorkflowSteps', () => {
  it('shows the steps and their deadlines read-only until Edit steps', () => {
    const { onDraftOpen } = setup({ steps: saved.map((s, i) => (i === 1 ? { ...s, deadline: '2030-03-04T09:00:00Z' } : s)) })
    expect(screen.queryByTestId('workflow-step-picker-0')).not.toBeInTheDocument()
    expect(screen.queryByTestId('workflow-deadline-input-1')).not.toBeInTheDocument()
    expect(screen.getByTestId('workflow-deadline-1')).toHaveAttribute('data-value', '2030-03-04T09:00:00.000Z')
    expect(onDraftOpen).toHaveBeenLastCalledWith(false)

    edit()
    expect(screen.getByTestId('workflow-step-picker-0')).toBeInTheDocument()
    expect(onDraftOpen, 'the picker must close while editing').toHaveBeenLastCalledWith(true)
    expect(screen.getByTestId('workflow-steps-save'), 'Save changes with nothing changed').toHaveAttribute('aria-disabled', 'true')
  })

  it('lists the per-language deadlines behind the globe in the read-only view', () => {
    setup({ steps: saved.map((s, i) => (i === 1 ? { ...s, language_deadlines: { de: '2030-03-01T09:00:00Z' } } : s)) })
    expect(screen.queryByTestId('workflow-deadline-langs-toggle-0'), 'a globe on a step with no language dates').not.toBeInTheDocument()
    click('workflow-deadline-langs-toggle-1')
    const controlled = document.getElementById(screen.getByTestId('workflow-deadline-langs-toggle-1').getAttribute('aria-controls') ?? '')
    expect(controlled, 'the globe does not point at the dates it opens').toContainElement(screen.getByTestId('workflow-deadline-override-1-0'))
    expect(screen.getByTestId('workflow-deadline-override-1-0')).toHaveTextContent('de')
    expect(screen.getByTestId('workflow-deadline-override-value-1-0')).toHaveAttribute('data-value', '2030-03-01T09:00:00.000Z')
  })

  it('a step change writes nothing until Save changes', async () => {
    const { onSave } = setup()
    edit()
    addProofread()
    await act(async () => { vi.advanceTimersByTime(5000) })
    expect(onSave).not.toHaveBeenCalled()
    expect(screen.getByTestId('workflow-steps-save')).not.toHaveAttribute('aria-disabled')
  })

  it('Save changes shows the preview, and only the confirmation saves the draft', async () => {
    const { onSave, onPreview } = setup()
    edit()
    addProofread()
    await act(async () => { fireEvent.click(screen.getByTestId('workflow-steps-save')) })
    expect(onPreview).toHaveBeenCalledTimes(1)
    expect(onPreview.mock.calls[0][0].map(s => s.name)).toEqual(['Translation', 'Review', 'Proofread'])
    const dialog = screen.getByRole('alertdialog')
    expect(within(dialog).getByText('This save adds 1 step.')).toBeInTheDocument()
    expect(within(dialog).getByTestId('workflow-steps-preview')).toHaveTextContent('1 job is in progress')
    expect(onSave).not.toHaveBeenCalled()

    await act(async () => { fireEvent.click(screen.getByTestId('workflow-steps-confirm')) })
    expect(onSave).toHaveBeenCalledTimes(1)
    expect(onSave.mock.calls[0][0].map((s: WorkflowStep) => s.name)).toEqual(['Translation', 'Review', 'Proofread'])
    expect(screen.getByTestId('workflow-steps-edit'), 'back to the read-only view').toBeInTheDocument()
  })

  it('Discard puts the saved steps back and leaves the editor', () => {
    const { onDraftOpen } = setup()
    edit()
    addProofread()
    click('workflow-steps-discard')
    expect(onDraftOpen).toHaveBeenLastCalledWith(false)
    expect(screen.getByTestId('workflow-steps-edit')).toBeInTheDocument()
    expect(screen.queryByTestId('workflow-step-row-2')).not.toBeInTheDocument()
  })

  it('a refused preview keeps the draft and re-reads the steps', async () => {
    const { onSave, onRefresh } = setup({ onPreview: () => Promise.reject(new Error('step 2 ("Review") has already been reached by 1 job(s)')) })
    edit()
    addProofread()
    await act(async () => { fireEvent.click(screen.getByTestId('workflow-steps-save')) })
    expect(toast.error).toHaveBeenCalledWith(expect.stringMatching(/already been reached/))
    expect(onRefresh).toHaveBeenCalledTimes(1)
    expect(onSave).not.toHaveBeenCalled()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(screen.getByTestId('workflow-step-row-2')).toBeInTheDocument()
  })

  it('a deadline and an assignee are part of the draft: nothing saves until Save changes', async () => {
    const { onSave, onPreview } = setup()
    edit()
    pick('workflow-step-assignee-1', '3')
    pickDeadline('workflow-deadline-input-1', '2030-03-04', '09:00')
    await act(async () => { vi.advanceTimersByTime(5000) })
    expect(onSave, 'a deadline saved on its own').not.toHaveBeenCalled()

    await act(async () => { fireEvent.click(screen.getByTestId('workflow-steps-save')) })
    const sent = onPreview.mock.calls[0][0]
    expect(sent[1].default_assignee).toBe(3)
    expect(sent[1].deadline).toMatch(/^2030-03-04/)
  }, PICKER_FLOW_TIMEOUT)

  it('a refetch while a date picker is open keeps the picker and its date', async () => {
    const { onPreview, refetch } = setup()
    edit()
    openPicker('workflow-deadline-input-1')
    clickDay('2030-03-04')
    // The steps are read again mid-pick (a job reached a step).
    act(() => { refetch(saved.map(s => ({ ...s }))) })
    typeTime('09:00')
    clickDone()
    await act(async () => { fireEvent.click(screen.getByTestId('workflow-steps-save')) })
    expect(onPreview.mock.calls[0][0][1].deadline).toMatch(/^2030-03-04/)
  }, PICKER_FLOW_TIMEOUT)

  it('a refetch while editing keeps the edits and takes the new locks', () => {
    const { refetch } = setup()
    edit()
    addProofread()
    expect(screen.getByTestId('workflow-step-row-1')).not.toHaveAttribute('data-locked')
    // A job reached Review meanwhile.
    act(() => { refetch(saved.map((s, i) => (i === 1 ? { ...s, locked: true, reached_jobs: 1 } : { ...s }))) })
    expect(screen.getByTestId('workflow-step-row-2'), 'the added step was dropped').toBeInTheDocument()
    expect(screen.getByTestId('workflow-step-row-1')).toHaveAttribute('data-locked', 'true')
  })

  it('a draft with no edits yet takes the steps saved meanwhile, so Save cannot put them back', () => {
    const { refetch } = setup()
    edit()
    act(() => { refetch(saved.map((s, i) => (i === 1 ? { ...s, deadline: '2030-05-06T09:00:00Z' } : { ...s }))) })
    expect(screen.getByTestId('workflow-deadline-input-1')).toHaveAttribute('data-value', '2030-05-06T09:00:00.000Z')
    expect(screen.getByTestId('workflow-steps-save'), 'Save changes offered for a list nobody changed').toHaveAttribute('aria-disabled', 'true')
  })

  it('asks before a reload or close only while there are unsaved changes', () => {
    setup()
    const unload = () => {
      const e = new Event('beforeunload', { cancelable: true })
      window.dispatchEvent(e)
      return e.defaultPrevented
    }
    edit()
    expect(unload(), 'asked with nothing changed').toBe(false)
    addProofread()
    expect(unload(), 'did not ask with unsaved changes').toBe(true)
    click('workflow-steps-discard')
    expect(unload(), 'still asks after Discard').toBe(false)
  })

  it('a failed save keeps the draft open and re-reads the steps', async () => {
    const { onRefresh } = setup({ onSave: () => Promise.reject(new Error('nope')) })
    edit()
    addProofread()
    await act(async () => { fireEvent.click(screen.getByTestId('workflow-steps-save')) })
    await act(async () => { fireEvent.click(screen.getByTestId('workflow-steps-confirm')) })
    expect(toast.error).toHaveBeenCalledWith('nope')
    expect(onRefresh).toHaveBeenCalledTimes(1)
    expect(screen.getByTestId('workflow-steps-save')).toBeInTheDocument()
    expect(screen.getByTestId('workflow-step-row-2')).toBeInTheDocument()
  })
})

describe('previewSummary', () => {
  it('counts the step changes', () => {
    const p = (added: number, changed: number, removed: number): WorkflowPreview => ({ ...preview, added, changed, removed })
    expect(previewSummary(p(1, 0, 0))).toBe('This save adds 1 step.')
    expect(previewSummary(p(2, 1, 0))).toBe('This save adds 2 steps and changes 1 step.')
    expect(previewSummary(p(0, 1, 2))).toBe('This save changes 1 step and removes 2 steps.')
    expect(previewSummary(p(0, 0, 0))).toBe('This save changes assignees and deadlines only.')
  })
})
