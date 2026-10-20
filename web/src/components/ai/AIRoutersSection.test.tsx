// AIRoutersSection: the list, the dialog's payload shape,
// the duplicate-language guard and the role gate.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { AIRoutersSection } from './AIRoutersSection'
import { AttentionProvider } from '../../hooks/useAttention'
import { aiRouterToShape, aiShapeToInput } from '../../lib/ai-router-rows'
import { shapeToRows, rowsToShape, exclusiveAll, ALL_LANGUAGES } from '../../lib/router-rows'
import type { AIEngine, AIRouter } from '../../api'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

// Plain <select>s stand in for the Combobox / MultiCombobox so the test drives
// the form without the cmdk popover.
interface FakeOption { value: string; label: string }
interface FakeAction { key: string; label: string; onSelect: () => void }
interface FakeSingle { value: string; onChange: (v: string) => void; options: FakeOption[]; actions?: FakeAction[]; 'data-testid'?: string; 'aria-invalid'?: boolean }
interface FakeMulti { value: string[]; onChange: (v: string[]) => void; options: FakeOption[]; 'data-testid'?: string; 'aria-invalid'?: boolean }
vi.mock('@/components/ui/combobox', () => ({
  Combobox: ({ value, onChange, options, actions, 'data-testid': testId, 'aria-invalid': invalid }: FakeSingle) => (
    <>
      <select data-testid={testId} aria-invalid={invalid || undefined} value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">—</option>
        {options.filter(o => o.value !== '').map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
      </select>
      {/* The real picker pins these under its search; the stand-in renders them
          flat so a test can reach one without the cmdk popover. */}
      {actions?.map(a => (
        <button key={a.key} type="button" data-testid={`${testId}-action-${a.key}`} onClick={a.onSelect}>{a.label}</button>
      ))}
    </>
  ),
  MultiCombobox: ({ value, onChange, options, 'data-testid': testId, 'aria-invalid': invalid }: FakeMulti) => (
    <select
      multiple
      data-testid={testId}
      aria-invalid={invalid || undefined}
      value={value}
      onChange={(e) => onChange(Array.from(e.target.selectedOptions).map(o => o.value))}
    >
      {options.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
    </select>
  ),
}))

const listAIRouters = vi.fn()
const createAIRouter = vi.fn()
const updateAIRouter = vi.fn()
const deleteAIRouter = vi.fn()
vi.mock('../../api', () => ({
  listAIRouters: () => listAIRouters(),
  createAIRouter: (input: unknown) => createAIRouter(input),
  updateAIRouter: (id: number, input: unknown) => updateAIRouter(id, input),
  deleteAIRouter: (id: number) => deleteAIRouter(id),
  listAIEngines: () => Promise.resolve(engines),
  listLanguages: () => Promise.resolve([{ code: 'de', name: 'German' }, { code: 'fr', name: 'French' }]),
  listAIPrompts: () => Promise.resolve([]),
  listAIVendors: () => Promise.resolve([]),
}))

const engines: AIEngine[] = [
  { id: 1, tenant_id: 1, name: 'QE A', purpose: 'qe_ape', enabled: true, unusable: '', created_at: '', updated_at: '', rows: [] },
  { id: 2, tenant_id: 1, name: 'QE B', purpose: 'qe_ape', enabled: true, unusable: '', created_at: '', updated_at: '', rows: [] },
  // An MT-purpose engine is never offered.
  { id: 3, tenant_id: 1, name: 'LLM MT', purpose: 'mt', enabled: true, unusable: '', created_at: '', updated_at: '', rows: [] },
]
const production: AIRouter = {
  id: 10, tenant_id: 1, name: 'Production', default_engine_id: 1, unusable: '', created_at: '', updated_at: '', deleted_at: '',
  rules: [{ id: 1, target_lang: 'de', engine_id: 2 }],
}

function renderSection(canWrite = true) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  // MemoryRouter: the engine picker's pinned "New AI engine" goes through
  // useEngineCreateActions, which navigates when a popup is blocked.
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter><AIRoutersSection canWrite={canWrite} /></MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  listAIRouters.mockResolvedValue([production])
  createAIRouter.mockResolvedValue({ ...production, id: 11 })
})

describe('AIRoutersSection', () => {
  it('lists each router by name only — the routes live in the dialog', async () => {
    renderSection()
    const card = await screen.findByTestId('ai-router-10')
    expect(card).toHaveTextContent('Production')
    expect(card).not.toHaveTextContent('QE A')
    expect(card).not.toHaveTextContent('de')
  })

  it('creates a router: a row with "All languages" sets the default, the rest become rules', async () => {
    const user = userEvent.setup()
    renderSection()
    await user.click(await screen.findByTestId('ai-router-add'))
    const form = await screen.findByTestId('ai-router-form')
    // The name autofills and dodges the taken one.
    expect(within(form).getByTestId('ai-router-name')).toHaveValue('New AI router')

    // A new router opens as one route: "All languages" → the first engine.
    const row0 = within(form).getByTestId('ai-router-rule-0')
    expect(within(row0).getByTestId('lang')).toHaveValue([ALL_LANGUAGES])
    const enginePicker = within(form).getByTestId('ai-router-rule-0-engine') as HTMLSelectElement
    expect(enginePicker).toHaveValue('1')
    // MT engines are not offered anywhere in the form.
    expect(Array.from(enginePicker.options).map(o => o.textContent)).toEqual(['—', 'QE A', 'QE B'])
    await user.selectOptions(enginePicker, '2')
    await user.click(within(form).getByTestId('ai-router-rule-add'))
    await user.selectOptions(within(within(form).getByTestId('ai-router-rule-1')).getByTestId('lang'), ['fr'])
    await user.selectOptions(within(form).getByTestId('ai-router-rule-1-engine'), '1')
    await user.click(within(form).getByRole('button', { name: /^create$/i }))
    await waitFor(() => expect(createAIRouter).toHaveBeenCalledWith({
      name: 'New AI router',
      default_engine_id: 2,
      rules: [{ target_lang: 'fr', engine_id: 1 }],
    }))
  })

  // Anti-tautology RED proof: pass `f.onChange` straight through instead of
  // exclusiveAll → the row keeps both and this fails.
  it('"All languages" and specific languages are exclusive within a row', async () => {
    const user = userEvent.setup()
    renderSection()
    await user.click(await screen.findByTestId('ai-router-add'))
    const form = await screen.findByTestId('ai-router-form')
    const langs = within(within(form).getByTestId('ai-router-rule-0')).getByTestId('lang') as HTMLSelectElement
    // The prefilled "All" goes as soon as a language is picked …
    await user.selectOptions(langs, ['fr', 'de'])
    expect(langs).toHaveValue(['fr', 'de'])
    // … and comes back alone when picked again.
    await user.selectOptions(langs, [ALL_LANGUAGES])
    expect(langs).toHaveValue([ALL_LANGUAGES])
    // "All" first, then a language: the language wins and All goes.
    await user.selectOptions(langs, ['de'])
    expect(langs).toHaveValue(['de'])
  })

  it('without an "All languages" row the default is null', async () => {
    const user = userEvent.setup()
    renderSection()
    await user.click(await screen.findByTestId('ai-router-add'))
    const form = await screen.findByTestId('ai-router-form')
    // Picking languages on the prefilled row drops its "All languages".
    await user.selectOptions(within(within(form).getByTestId('ai-router-rule-0')).getByTestId('lang'), ['fr', 'de'])
    await user.click(within(form).getByRole('button', { name: /^create$/i }))
    await waitFor(() => expect(createAIRouter).toHaveBeenCalledWith({
      name: 'New AI router',
      default_engine_id: null,
      rules: [{ target_lang: 'de', engine_id: 1 }, { target_lang: 'fr', engine_id: 1 }],
    }))
  })

  // Anti-tautology RED proof: drop the superRefine from routerSchema → the
  // half-filled row goes through to createAIRouter and this fails.
  it('refuses a half-filled row and never offers a taken language, or a second "all", twice', async () => {
    const user = userEvent.setup()
    renderSection()
    await user.click(await screen.findByTestId('ai-router-add'))
    const form = await screen.findByTestId('ai-router-form')
    // Row 0 opens with "All": no other row offers it. Give row 1 "fr" and
    // row 2 no longer offers "fr" either.
    await user.click(within(form).getByTestId('ai-router-rule-add'))
    const row1 = within(form).getByTestId('ai-router-rule-1')
    const lang1 = within(row1).getByTestId('lang') as HTMLSelectElement
    let offered = Array.from(lang1.options).map(o => o.value)
    expect(offered).not.toContain(ALL_LANGUAGES)
    expect(offered).toContain('fr')
    await user.selectOptions(lang1, ['fr'])
    await user.selectOptions(within(form).getByTestId('ai-router-rule-1-engine'), '1')
    await user.click(within(form).getByTestId('ai-router-rule-add'))
    offered = Array.from((within(within(form).getByTestId('ai-router-rule-2')).getByTestId('lang') as HTMLSelectElement).options).map(o => o.value)
    expect(offered).not.toContain('fr')
    expect(offered).toContain('de')
    // Half a row (engine, no language) is refused: its language picker turns
    // red (no message — the frame is the signal) and nothing is sent.
    await user.selectOptions(within(form).getByTestId('ai-router-rule-2-engine'), '2')
    await user.click(within(form).getByRole('button', { name: /^create$/i }))
    const row2 = within(form).getByTestId('ai-router-rule-2')
    await waitFor(() => expect(within(row2).getByTestId('lang')).toHaveAttribute('aria-invalid', 'true'))
    expect(within(form).getByTestId('ai-router-rule-2-engine')).not.toHaveAttribute('aria-invalid')
    expect(createAIRouter).not.toHaveBeenCalled()
  })

  // Anti-tautology RED proof: skip blank rows in routerSchema's superRefine →
  // the blank row is dropped and createAIRouter is called.
  it('a route left blank is flagged on save, not silently dropped', async () => {
    const user = userEvent.setup()
    renderSection()
    await user.click(await screen.findByTestId('ai-router-add'))
    const form = await screen.findByTestId('ai-router-form')
    await user.click(within(form).getByTestId('ai-router-rule-add'))
    await user.click(within(form).getByRole('button', { name: /^create$/i }))
    const row1 = within(form).getByTestId('ai-router-rule-1')
    await waitFor(() => expect(within(row1).getByTestId('lang')).toHaveAttribute('aria-invalid', 'true'))
    expect(within(form).getByTestId('ai-router-rule-1-engine')).toHaveAttribute('aria-invalid', 'true')
    // The prefilled row is complete and not flagged.
    expect(within(within(form).getByTestId('ai-router-rule-0')).getByTestId('lang')).not.toHaveAttribute('aria-invalid')
    expect(within(form).queryByText('Pick at least one language')).not.toBeInTheDocument()
    expect(createAIRouter).not.toHaveBeenCalled()
  })

  it('edits a router in the dialog: rules row first, the default row after, and sends the full replacement', async () => {
    updateAIRouter.mockResolvedValue(production)
    const user = userEvent.setup()
    renderSection()
    await user.click(await screen.findByTestId('ai-router-10'))
    const form = await screen.findByTestId('ai-router-form')
    // de → QE B is row 0; the default (QE A) is its own row with "all".
    expect(within(within(form).getByTestId('ai-router-rule-0')).getByTestId('lang')).toHaveValue(['de'])
    expect(within(form).getByTestId('ai-router-rule-0-engine')).toHaveValue('2')
    expect(within(within(form).getByTestId('ai-router-rule-1')).getByTestId('lang')).toHaveValue([ALL_LANGUAGES])
    expect(within(form).getByTestId('ai-router-rule-1-engine')).toHaveValue('1')
    await user.click(within(form).getByTestId('ai-router-rule-0-remove'))
    await user.click(within(form).getByRole('button', { name: /^update$/i }))
    await waitFor(() => expect(updateAIRouter).toHaveBeenCalledWith(10, {
      name: 'Production', default_engine_id: 1, rules: [],
    }))
  })

  // Anti-tautology RED proof: drop `disabled={fields.length === 1}` from the
  // remove button → the only route can be removed and this fails.
  it('the last remaining route cannot be removed', async () => {
    const user = userEvent.setup()
    renderSection()
    await user.click(await screen.findByTestId('ai-router-add'))
    const form = await screen.findByTestId('ai-router-form')
    expect(within(form).getByTestId('ai-router-rule-0-remove')).toBeDisabled()
    await user.click(within(form).getByTestId('ai-router-rule-add'))
    expect(within(form).getByTestId('ai-router-rule-0-remove')).toBeEnabled()
    await user.click(within(form).getByTestId('ai-router-rule-1-remove'))
    expect(within(form).getByTestId('ai-router-rule-0-remove')).toBeDisabled()
    expect(within(form).queryByTestId('ai-router-rule-1')).not.toBeInTheDocument()
  })

  // Anti-tautology RED proof: drop `!isDirty` from the submit's disabled
  // expression → the button is enabled before any change and this fails.
  it('Update is disabled until something changed', async () => {
    const user = userEvent.setup()
    renderSection()
    await user.click(await screen.findByTestId('ai-router-10'))
    const form = await screen.findByTestId('ai-router-form')
    const submit = within(form).getByRole('button', { name: /^update$/i })
    expect(submit).toBeDisabled()
    await user.selectOptions(within(form).getByTestId('ai-router-rule-0-engine'), '1')
    expect(submit).toBeEnabled()
  })

  it('read-only for managers: no add button, cards are not clickable', async () => {
    renderSection(false)
    const card = await screen.findByTestId('ai-router-10')
    expect(card).not.toHaveAttribute('role', 'button')
    expect(screen.queryByTestId('ai-router-add')).not.toBeInTheDocument()
  })
})

// The same path the dialog takes: aiRouterToShape → shapeToRows to fill the
// form, rowsToShape → aiShapeToInput to send it.
const toRows = (router: Parameters<typeof aiRouterToShape>[0]) => shapeToRows(aiRouterToShape(router))

describe('router ↔ rows mapping', () => {
  it('groups rules by engine and folds the default into that engine\'s row', () => {
    expect(toRows({ default_engine_id: 2, rules: [
      { target_lang: 'de', engine_id: 2 }, { target_lang: 'fr', engine_id: 1 }, { target_lang: 'it', engine_id: 2 },
    ] })).toEqual([
      { langs: ['de', 'it', ALL_LANGUAGES], engine: '2' },
      { langs: ['fr'], engine: '1' },
    ])
  })
  it('gives a default with no rules its own last row, and no default no "all"', () => {
    expect(toRows({ default_engine_id: 3, rules: [{ target_lang: 'de', engine_id: 2 }] }))
      .toEqual([{ langs: ['de'], engine: '2' }, { langs: [ALL_LANGUAGES], engine: '3' }])
    expect(toRows({ default_engine_id: undefined, rules: [] })).toEqual([])
  })
  it('round-trips through aiShapeToInput, and trims the name', () => {
    const rows = toRows({ default_engine_id: 2, rules: [{ target_lang: 'de', engine_id: 2 }, { target_lang: 'fr', engine_id: 1 }] })
    expect(aiShapeToInput(' R ', rowsToShape(rows))).toEqual({
      name: 'R', default_engine_id: 2,
      rules: [{ target_lang: 'de', engine_id: 2 }, { target_lang: 'fr', engine_id: 1 }],
    })
  })
})

describe('exclusiveAll', () => {
  it('lets "All" win when it is the newcomer, and a language win when "All" was there', () => {
    expect(exclusiveAll(['fr'], ['fr', ALL_LANGUAGES])).toEqual([ALL_LANGUAGES])
    expect(exclusiveAll([ALL_LANGUAGES], [ALL_LANGUAGES, 'de'])).toEqual(['de'])
    expect(exclusiveAll([], ['fr', 'de'])).toEqual(['fr', 'de'])
    expect(exclusiveAll([], [ALL_LANGUAGES])).toEqual([ALL_LANGUAGES])
  })
})

// Building a router is exactly when you notice the engine you need does not
// exist. Creating one is pinned in the engine picker rather than sending the
// author off to find the Engines tab and come back.
describe('AIRoutersSection — creating an engine from the picker', () => {
  it('opens the engine form in a new tab, leaving the half-built router alone', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue({} as Window)
    const user = userEvent.setup()
    renderSection()
    await user.click(await screen.findByTestId('ai-router-add'))
    const form = await screen.findByTestId('ai-router-form')

    await user.click(within(form).getByTestId('ai-router-rule-0-engine-action-new-ai-engine'))

    // The /new route is the engines page with its create dialog already open,
    // so the link lands on the form rather than on a list with a button to
    // find. No 'noopener' — it suppresses the window handle, and the shared
    // hook reads a null handle as "popup blocked".
    expect(open).toHaveBeenCalledWith('/ai/engines/new', '_blank')
    // …and the router form is still there, unsaved work intact.
    expect(screen.getByTestId('ai-router-form')).toBeInTheDocument()
    open.mockRestore()
  })
})

describe('AIRoutersSection deep link', () => {
  it('rings the router card a link points at, once the list has loaded', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/ai?attention=ai-router-10']}>
          <AttentionProvider><AIRoutersSection canWrite /></AttentionProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    )
    const card = await screen.findByTestId('ai-router-10')
    await waitFor(() => expect(card.dataset.attention).toBe(''))
  })
})
