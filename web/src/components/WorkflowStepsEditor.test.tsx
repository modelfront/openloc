// WorkflowStepsEditor: steps are picked from the library; the editor owns
// order, the assignee and, on a project, each step's deadline.
//
// Pinned:
//   - Assignee picker filter: project context = qualified-for-ANY
//     target pair UNION managers; library context = all members.
//   - Stale-FK indicator for a deleted assignee.
//   - Step picker offers the library; picking a definition takes its identity
//     (type, engine, role); an ai step has no assignee ("Automated").
//   - A pre-library row shows as "(custom)" and can be swapped, never typed.
//   - Locked rows: no grip, no delete, the step cannot be swapped; the
//     assignee and deadline stay editable. Moves stay below them.
//   - The 9-step cap hides the add-row.
//
// Mocks: React Query cache pre-seed for members / users-by-pair / ai-engines /
// step-definitions; Combobox rendered as a native <select>.

import { describe, it, expect, vi, afterEach } from 'vitest'
import type { ComboboxProps, MultiComboboxProps } from '@/components/ui/combobox'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { useState } from 'react'
import { WorkflowStepsEditor, moveTailRow, unpickedRows, type WorkflowStep } from './WorkflowStepsEditor'
import type { Member, AIEngine, StepDefinitionRow } from '../api'
import { pickDeadline, PICKER_FLOW_TIMEOUT } from '@/test/deadline-picker'

vi.mock('@/components/ui/combobox', () => ({
  Combobox: ({ value, onChange, options, disabled, className, placeholder, 'aria-invalid': ariaInvalid, 'data-testid': testId }: ComboboxProps) => (
    <>
      <select
        data-testid={testId}
        value={value}
        disabled={disabled}
        aria-invalid={ariaInvalid}
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
  deadlines?: boolean
}

// The editor is controlled; this holds its steps the way a caller does.
function Harness({ initial, onChange, ...rest }: { initial: WorkflowStep[]; onChange: (s: WorkflowStep[]) => void; sourceLang?: string; targetLangs?: string[]; deadlines?: boolean }) {
  const [steps, setSteps] = useState(initial)
  return <WorkflowStepsEditor steps={steps} onChange={next => { setSteps(next); onChange(next) }} {...rest} />
}

function setup(opts: Setup) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(['members'], opts.members ?? [owner, admin, memberFR, memberDE, memberNone])
  qc.setQueryData(['ai-engines'], [qeEngine, apeEngine, mtEngine])
  qc.setQueryData(['ai-prompts'], [])
  qc.setQueryData(['ai-vendors'], [])
  qc.setQueryData(['step-definitions'], opts.definitions ?? LIBRARY)
  qc.setQueryData(['languages'], [])
  qc.setQueryData(['ai-routers'], [{ id: 10, name: 'QE router', unusable: 'disabled', rules: [] }])
  if (opts.sourceLang && opts.targetLangs) {
    for (const t of opts.targetLangs) {
      const data = t === 'fr' ? (opts.qualifiedFR ?? [memberFR]) : t === 'de' ? (opts.qualifiedDE ?? [memberDE]) : []
      qc.setQueryData(['users-by-pair', opts.sourceLang, t], data)
    }
  }
  const onChange = vi.fn()
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <Harness
          initial={opts.steps ?? [step(defTranslation)]}
          onChange={onChange}
          sourceLang={opts.sourceLang}
          targetLangs={opts.targetLangs}
          deadlines={opts.deadlines}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { onChange, last: (): WorkflowStep[] => onChange.mock.calls.at(-1)?.[0] }
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

  it('surfaces a "(deleted user)" option for an assignee no longer in the picker, and marks the field invalid', () => {
    setup({ sourceLang: 'en', targetLangs: ['fr'], steps: [step(defTranslation, { default_assignee: 999 }), step(defReview, { default_assignee: 3 })] })
    expect(screen.getByRole('option', { name: /deleted user/i })).toBeInTheDocument()
    expect(screen.getByTestId('workflow-step-assignee-0')).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByTestId('workflow-step-assignee-1')).not.toHaveAttribute('aria-invalid', 'true')
  })

  it('a member outside the language-pair filter keeps their name, not "(deleted user)"', () => {
    // Nora has no en→fr pair, so the filter does not offer her; she is still a member.
    setup({ sourceLang: 'en', targetLangs: ['fr'], steps: [step(defTranslation, { default_assignee: memberNone.id })] })
    const picker = screen.getByTestId('workflow-step-assignee-0') as HTMLSelectElement
    expect(picker.value).toBe(String(memberNone.id))
    expect(picker.selectedOptions[0].textContent).toBe('Nora (no pairs)')
    expect(screen.queryByRole('option', { name: /deleted user/i })).not.toBeInTheDocument()
    expect(picker).not.toHaveAttribute('aria-invalid', 'true')
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

  it('"Add step" appends an unpicked row', async () => {
    const user = userEvent.setup()
    const { last } = setup({})
    await user.click(screen.getByTestId('workflow-add-step'))
    expect((screen.getByTestId('workflow-step-picker-1') as HTMLSelectElement).value).toBe('')
    expect(screen.getByTestId('workflow-step-picker-1'), 'an unpicked row is marked invalid').toHaveAttribute('aria-invalid', 'true')
    expect(unpickedRows(last())).toEqual([2])
    await user.selectOptions(screen.getByTestId('workflow-step-picker-1'), '2')
    expect(last().map(s => s.name)).toEqual(['Translation', 'Review'])
    expect(screen.getByTestId('workflow-step-picker-1')).not.toHaveAttribute('aria-invalid', 'true')
  })

  it('an AI step names its router and what is wrong with it; a human step has no router line', () => {
    setup({ steps: [step(defTranslation), step(defAIReview)] })
    expect(screen.queryByTestId('workflow-step-router-0')).not.toBeInTheDocument()
    expect(screen.getByTestId('workflow-step-router-1')).toHaveTextContent('QE router')
    expect(screen.getByTestId('workflow-step-router-1')).toHaveTextContent('(engine off)')
  })

  it('points at the step library when it is empty', () => {
    setup({ definitions: [] })
    expect(screen.getByTestId('workflow-library-empty')).toHaveTextContent(/Create a step/)
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

describe('locked rows', () => {
  const locked = (def: StepDefinitionRow, id: number, reached: number, over: Partial<WorkflowStep> = {}) =>
    step(def, { id, locked: true, reached_jobs: reached, ...over })

  it('a locked row keeps its step but not its assignee: no grip, no delete, picker disabled', async () => {
    const user = userEvent.setup()
    const { last } = setup({ steps: [locked(defTranslation, 1, 2), step(defReview, { id: 2 })] })
    expect(screen.getByTestId('workflow-step-row-0')).toHaveAttribute('data-locked', 'true')
    expect(screen.getByTestId('workflow-step-locked-0')).toBeInTheDocument()
    expect(screen.getByLabelText('Locked step')).toBeInTheDocument()
    expect(screen.queryByTestId('workflow-step-drag-0')).not.toBeInTheDocument()
    expect(screen.queryByTestId('workflow-step-remove-0')).not.toBeInTheDocument()
    expect(screen.getByTestId('workflow-step-picker-0')).toBeDisabled()
    expect(screen.getByTestId('workflow-step-assignee-0')).toBeEnabled()
    // The unlocked row after it has both.
    expect(screen.getByTestId('workflow-step-drag-1')).toBeInTheDocument()
    expect(screen.getByTestId('workflow-step-remove-1')).toBeInTheDocument()
    expect(screen.getByTestId('workflow-step-picker-1')).toBeEnabled()

    await user.selectOptions(screen.getByTestId('workflow-step-assignee-0'), '3')
    expect(last()[0]).toMatchObject({ step_definition_id: defTranslation.id, default_assignee: 3, locked: true })
  })

  it('removing an unlocked row leaves the locked ones', async () => {
    const user = userEvent.setup()
    const { last } = setup({ steps: [locked(defTranslation, 1, 1), step(defReview, { id: 2 }), step(defAIReview, { id: 3 })] })
    await user.click(screen.getByTestId('workflow-step-remove-1'))
    expect(last().map(s => s.id)).toEqual([1, 3])
  })

  it('a locked row keeps an editable deadline on a project', () => {
    const { last } = setup({ deadlines: true, sourceLang: 'en', targetLangs: ['fr'], steps: [locked(defTranslation, 1, 1), step(defAutoAPE, { id: 2 })] })
    expect(screen.getByTestId('workflow-deadline-input-0')).toBeEnabled()
    pickDeadline('workflow-deadline-input-0', '2030-03-04', '09:00')
    expect(last()[0].deadline).toMatch(/^2030-03-04/)
    expect(last()[0]).toMatchObject({ id: 1, locked: true })
    // An automated step has no deadline.
    expect(screen.queryByTestId('workflow-deadline-input-1')).not.toBeInTheDocument()
  }, PICKER_FLOW_TIMEOUT)

  it('moves happen below the locked rows', () => {
    const rows = [locked(defTranslation, 1, 1), locked(defReview, 2, 1), step(defAIReview, { id: 3 }), step(defAutoAPE, { id: 4 })]
    expect(moveTailRow(rows, 1, 0).map(s => s.id)).toEqual([1, 2, 4, 3])
    expect(moveTailRow(rows, 0, 1).map(s => s.id)).toEqual([1, 2, 4, 3])
  })
})
