// WorkflowStepsEditor: steps are picked from the library;
// the editor owns order and assignee (due dates are WorkflowDeadlines').
//
// Pinned:
//   - Assignee picker filter: project context = qualified-for-ANY
//     target pair UNION managers; template / library context = all members.
//   - Stale-FK indicator for a deleted assignee.
//   - Step picker offers the library; picking a definition takes its identity
//     (type, engine, role); an ai step has no assignee ("Automated").
//   - A pre-library row shows as "(custom)" and can be swapped, never typed.
//   - Placeholders block Save; the 9-step cap hides the add-row.
//
// Mocks: React Query cache pre-seed for members / users-by-pair / ai-engines /
// step-definitions; Combobox rendered as a native <select>; sonner silent.

import { describe, it, expect, vi, afterEach } from 'vitest'
import type { ComboboxProps, MultiComboboxProps } from '@/components/ui/combobox'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { toast } from 'sonner'
import { WorkflowStepsEditor, unpickedRows, type WorkflowStep } from './WorkflowStepsEditor'
import type { Member, AIEngine, StepDefinitionRow } from '../api'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

vi.mock('@/components/ui/combobox', () => ({
  Combobox: ({ value, onChange, options, disabled, className, placeholder, 'data-testid': testId }: ComboboxProps) => (
    <>
      <select
        data-testid={testId}
        value={value}
        disabled={disabled}
        className={className}
        aria-label={placeholder}
        onChange={(e) => onChange(e.target.value)}
      >
        {/* A real Combobox shows the placeholder when nothing is picked; a
            native select would silently pick the first option instead. */}
        {placeholder && <option value="">{placeholder}</option>}
        {options.map(o => (
          <option key={o.value} value={o.value} disabled={o.disabled}>{o.label}</option>
        ))}
      </select>
      {/* The real trigger renders the selected option's chip (the type badge). */}
      {options.find(o => o.value === value)?.chip}
    </>
  ),
  // The per-language deadline rows pick languages with this one. A native
  // multi-select keeps the mock honest about the value being a list, without
  // dragging a popover into jsdom.
  MultiCombobox: ({ value, onChange, options, disabled, 'aria-label': ariaLabel }: MultiComboboxProps) => (
    <select
      multiple
      disabled={disabled}
      aria-label={ariaLabel}
      value={value}
      onChange={(e) => onChange([...e.target.selectedOptions].map(o => o.value))}
    >
      {options.map(o => (
        <option key={o.value} value={o.value}>{o.label}</option>
      ))}
    </select>
  ),
}))

afterEach(() => { cleanup(); vi.clearAllMocks() })

const owner: Member = { id: 1, email: 'o@t', name: 'Olivia (owner)', role: 'owner', created_at: '2026-01-01' }
const admin: Member = { id: 2, email: 'a@t', name: 'Alice (admin)', role: 'admin', created_at: '2026-01-01' }
const memberFR: Member = { id: 3, email: 'fr@t', name: 'Fred (en→fr only)', role: 'linguist', created_at: '2026-01-01' }
const memberDE: Member = { id: 4, email: 'de@t', name: 'Dieter (en→de only)', role: 'linguist', created_at: '2026-01-01' }
const memberNone: Member = { id: 5, email: 'no@t', name: 'Nora (no pairs)', role: 'linguist', created_at: '2026-01-01' }

const mkEngine = (id: number, name: string): AIEngine => ({
  id, tenant_id: 1, name, purpose: 'qe_ape', enabled: true, unusable: '', created_at: '2026-01-01', updated_at: '2026-01-01',
  rows: [{ id, source_lang: 'en', target_langs: ['fr'], system_prompt_id: id, bad_output_action: 'reject' }],
})
const qeEngine = mkEngine(10, 'QE reviewer')
const apeEngine = mkEngine(11, 'APE fixer')
const mtEngine: AIEngine = { ...mkEngine(12, 'MT engine'), purpose: 'mt' }

const mkDef = (id: number, name: string, step_type: StepDefinitionRow['step_type'], ai_router_id: number | null = null): StepDefinitionRow => ({
  id, tenant_id: 1, name, step_type, role_required: 'linguist', ai_router_id: ai_router_id ?? undefined,
  created_at: '2026-01-01', updated_at: '2026-01-01', deleted_at: '', ai_router_name: null, workflow_count: 0,
})
const defTranslation = mkDef(1, 'Translation', 'human')
const defReview = mkDef(2, 'Review', 'human')
const defAutoAPE = mkDef(3, 'Auto APE', 'ai', 10)
const defAIReview = mkDef(4, 'AI review', 'hybrid', 10)
const LIBRARY = [defTranslation, defReview, defAutoAPE, defAIReview]

const step = (def: StepDefinitionRow, over: Partial<WorkflowStep> = {}): WorkflowStep => ({
  step_definition_id: def.id, name: def.name, step_type: def.step_type as WorkflowStep['step_type'],
  ai_router_id: def.ai_router_id, deadline: null, ...over,
})

interface Setup {
  members?: Member[]
  qualifiedFR?: Member[]
  qualifiedDE?: Member[]
  steps?: WorkflowStep[]
  sourceLang?: string
  targetLangs?: string[]
  definitions?: StepDefinitionRow[]
  onSave?: (steps: WorkflowStep[]) => Promise<void>
  initialEditing?: boolean
}

function setup(opts: Setup) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(['members'], opts.members ?? [owner, admin, memberFR, memberDE, memberNone])
  qc.setQueryData(['ai-engines'], [qeEngine, apeEngine, mtEngine])
  qc.setQueryData(['ai-prompts'], [])
  qc.setQueryData(['ai-vendors'], [])
  qc.setQueryData(['step-definitions'], opts.definitions ?? LIBRARY)
  if (opts.sourceLang && opts.targetLangs) {
    for (const t of opts.targetLangs) {
      const data = t === 'fr' ? (opts.qualifiedFR ?? [memberFR]) : t === 'de' ? (opts.qualifiedDE ?? [memberDE]) : []
      qc.setQueryData(['users-by-pair', opts.sourceLang, t], data)
    }
  }
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <WorkflowStepsEditor
          steps={opts.steps ?? [step(defTranslation)]}
          initialEditing={opts.initialEditing ?? true}
          sourceLang={opts.sourceLang}
          targetLangs={opts.targetLangs}
          onSave={opts.onSave}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const optionsOf = (testId: string) =>
  Array.from((screen.getByTestId(testId) as HTMLSelectElement).options).map(o => o.textContent ?? '')

describe('WorkflowStepsEditor assignee picker filter', () => {
  it('project context: UNION of members qualified for ANY target pair plus managers', () => {
    setup({ sourceLang: 'en', targetLangs: ['fr', 'de'] })
    const opts = optionsOf('workflow-step-assignee-0')
    expect(opts).toContain('No default')
    expect(opts.some(o => o.includes('Olivia'))).toBe(true)
    expect(opts.some(o => o.includes('Alice'))).toBe(true)
    expect(opts.some(o => o.includes('Fred'))).toBe(true)
    expect(opts.some(o => o.includes('Dieter'))).toBe(true)
    expect(opts.some(o => o.includes('Nora'))).toBe(false)
  })

  it('managers appear even with no language pairs registered', () => {
    setup({ sourceLang: 'en', targetLangs: ['fr', 'de'], qualifiedFR: [], qualifiedDE: [] })
    const opts = optionsOf('workflow-step-assignee-0')
    expect(opts.some(o => o.includes('Olivia'))).toBe(true)
    expect(opts.some(o => o.includes('Alice'))).toBe(true)
    expect(opts.some(o => o.includes('Fred'))).toBe(false)
  })

  it('a member qualified for every target appears once', () => {
    setup({ sourceLang: 'en', targetLangs: ['fr', 'de'], qualifiedFR: [memberFR], qualifiedDE: [memberFR] })
    expect(optionsOf('workflow-step-assignee-0').filter(o => o.includes('Fred')).length).toBe(1)
  })

  it('template / library context shows ALL members', () => {
    setup({})
    const opts = optionsOf('workflow-step-assignee-0')
    expect(opts.length).toBe(6)
    expect(opts.some(o => o.includes('Nora'))).toBe(true)
  })

  it('surfaces a "(deleted user)" option for an assignee no longer in the picker', () => {
    setup({ sourceLang: 'en', targetLangs: ['fr'], steps: [step(defTranslation, { default_assignee: 999 })] })
    expect(screen.getByRole('option', { name: /deleted user/i })).toBeInTheDocument()
  })

  it('clears default_assignee when "No default" is picked', async () => {
    const user = userEvent.setup()
    setup({ sourceLang: 'en', targetLangs: ['fr'], steps: [step(defTranslation, { default_assignee: 3 })] })
    const select = screen.getByTestId('workflow-step-assignee-0') as HTMLSelectElement
    expect(select.value).toBe('3')
    await user.selectOptions(select, '')
    expect(select.value).toBe('')
  })
})

describe('WorkflowStepsEditor step picker', () => {
  it('offers the library definitions, no free-text name input', () => {
    setup({})
    expect(optionsOf('workflow-step-picker-0')).toEqual(['Choose a step…', 'Translation', 'Review', 'Auto APE', 'AI review'])
    expect(screen.queryByPlaceholderText(/step name/i)).not.toBeInTheDocument()
  })

  it('picking a definition takes its type and engine; an ai step has no assignee', async () => {
    const user = userEvent.setup()
    setup({ steps: [step(defTranslation, { default_assignee: 3 })] })
    expect(screen.getByTestId('step-type-human')).toBeInTheDocument()
    await user.selectOptions(screen.getByTestId('workflow-step-picker-0'), '3')
    expect(screen.getByTestId('step-type-ai')).toBeInTheDocument()
    expect(screen.getByTestId('workflow-step-automated-0')).toHaveTextContent('Automated')
    expect(screen.queryByTestId('workflow-step-assignee-0')).not.toBeInTheDocument()
    // Back to a human step: the assignee picker returns, cleared.
    await user.selectOptions(screen.getByTestId('workflow-step-picker-0'), '2')
    expect((screen.getByTestId('workflow-step-assignee-0') as HTMLSelectElement).value).toBe('')
  })

  it('a pre-library row shows as "(custom)" and can be swapped for a library step', async () => {
    const user = userEvent.setup()
    setup({ steps: [{ id: 77, step_definition_id: null, name: 'LQA', step_type: 'human' }] })
    const picker = screen.getByTestId('workflow-step-picker-0') as HTMLSelectElement
    expect(picker.value).toBe('legacy:77')
    expect(optionsOf('workflow-step-picker-0')[1]).toBe('LQA (custom)')
    await user.selectOptions(picker, '2')
    expect(picker.value).toBe('2')
  })

  it('"Add step" appends an unpicked row and Save refuses until it is picked', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn().mockResolvedValue(undefined)
    setup({ initialEditing: false, onSave })
    await user.click(screen.getByRole('button', { name: 'Edit' }))
    await user.click(screen.getByTestId('workflow-add-step'))
    expect((screen.getByTestId('workflow-step-picker-1') as HTMLSelectElement).value).toBe('')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(onSave).not.toHaveBeenCalled()
    expect(toast.error).toHaveBeenCalledWith(expect.stringMatching(/row 2/))
    await user.selectOptions(screen.getByTestId('workflow-step-picker-1'), '2')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(onSave).toHaveBeenCalledTimes(1)
    expect(onSave.mock.calls[0][0].map((s: WorkflowStep) => s.name)).toEqual(['Translation', 'Review'])
  })

  it('points at the step library when it is empty', () => {
    setup({ definitions: [] })
    expect(screen.getByTestId('workflow-library-empty')).toHaveTextContent(/Create a step/)
  })



  it('view mode names the step with its type and "Automated" for an ai step', () => {
    setup({ initialEditing: false, steps: [step(defAutoAPE), step(defReview, { default_assignee: 3 })] })
    expect(screen.getByTestId('workflow-step-badge-0')).toHaveTextContent('Auto APE')
    expect(screen.getByTestId('workflow-step-badge-0')).toHaveTextContent('Automated')
    expect(screen.getByTestId('workflow-step-badge-1')).toHaveTextContent('Fred')
  })
})

describe('9-step cap', () => {
  const mkSteps = (n: number) => Array.from({ length: n }, () => step(defTranslation))
  it('shows the add-step control below the cap', () => {
    setup({ steps: mkSteps(8) })
    expect(screen.getByTestId('workflow-add-step')).toBeTruthy()
  })
  it('hides the add-step control at the cap', () => {
    setup({ steps: mkSteps(9) })
    expect(screen.queryByTestId('workflow-add-step')).toBeNull()
    expect(screen.getByText(/Maximum of 9 steps/i)).toBeTruthy()
  })
})

describe('unpickedRows', () => {
  it('reports 1-based rows with neither a library reference nor an existing id', () => {
    expect(unpickedRows([
      step(defTranslation),
      { step_definition_id: null, name: '', step_type: 'human' },
      { id: 5, step_definition_id: null, name: 'LQA', step_type: 'human' },
    ])).toEqual([2])
  })
})
