// StepDefinitionDialog: pins the type ↔ AI-router pairing.
import { describe, it, expect, vi, afterEach } from 'vitest'
import type { ComboboxProps } from '@/components/ui/combobox'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { StepDefinitionDialog } from './StepDefinitionDialog'
import type { AIRouter } from '../../api'

vi.mock('@/components/ui/combobox', () => ({
  Combobox: ({ value, onChange, options, 'data-testid': testId, 'aria-invalid': invalid }: ComboboxProps) => (
    <select data-testid={testId} aria-invalid={invalid || undefined} value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">—</option>
      {options.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
    </select>
  ),
}))

afterEach(cleanup)

const base = { tenant_id: 1, created_at: '', updated_at: '', deleted_at: '' }
const llmRouter: AIRouter = { ...base, id: 20, name: 'LLM router', default_engine_id: 10, unusable: '', rules: [] }
const ruleRouter: AIRouter = { ...base, id: 21, name: 'Rule router', default_engine_id: 10, unusable: '', rules: [{ id: 1, target_lang: 'fr', engine_id: 13 }] }

function setup(onSubmit = vi.fn().mockResolvedValue(undefined)) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(['ai-routers'], [llmRouter, ruleRouter])
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <StepDefinitionDialog open onClose={() => {}} onSubmit={onSubmit} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return onSubmit
}

describe('StepDefinitionDialog', () => {
  it('a human step needs only a name and submits without a router', async () => {
    const user = userEvent.setup()
    const onSubmit = setup()
    expect(screen.queryByTestId('step-definition-router')).not.toBeInTheDocument()
    await user.type(screen.getByTestId('step-definition-name'), 'Proofread')
    await user.click(screen.getByTestId('step-definition-submit'))
    expect(onSubmit).toHaveBeenCalledWith({ name: 'Proofread', step_type: 'human', ai_router_id: null })
  })

  it('hybrid / ai steps require an AI router', async () => {
    const user = userEvent.setup()
    const onSubmit = setup()
    await user.type(screen.getByTestId('step-definition-name'), 'Auto APE')
    await user.click(screen.getByTestId('step-type-option-ai'))
    const router = screen.getByTestId('step-definition-router') as HTMLSelectElement
    const labels = Array.from(router.options).map(o => o.textContent)
    expect(labels).toContain('LLM router')
    expect(labels).toContain('Rule router')
    await user.click(screen.getByTestId('step-definition-submit'))
    expect(await screen.findByText('AI router is required')).toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
    await user.selectOptions(router, '20')
    await user.click(screen.getByTestId('step-definition-submit'))
    expect(onSubmit).toHaveBeenCalledWith({ name: 'Auto APE', step_type: 'ai', ai_router_id: 20 })
  })

  // A step name is free text whatever router backs it.
  it('saves any step name with any router', async () => {
    const user = userEvent.setup()
    const onSubmit = setup()
    await user.type(screen.getByTestId('step-definition-name'), 'Proofread')
    await user.click(screen.getByTestId('step-type-option-hybrid'))
    await user.selectOptions(screen.getByTestId('step-definition-router'), '21')
    await user.click(screen.getByTestId('step-definition-submit'))
    expect(onSubmit).toHaveBeenCalledWith({ name: 'Proofread', step_type: 'hybrid', ai_router_id: 21 })
  })

  it('an empty name is refused inline, not by a disabled button', async () => {
    const user = userEvent.setup()
    const onSubmit = setup()
    expect(screen.getByTestId('step-definition-submit')).toBeEnabled()
    await user.click(screen.getByTestId('step-definition-submit'))
    expect(await screen.findByText('Name is required')).toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
    // Typing clears the error; whitespace-only still fails the trim.
    await user.type(screen.getByTestId('step-definition-name'), '   ')
    await user.click(screen.getByTestId('step-definition-submit'))
    expect(screen.getByText('Name is required')).toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  // Closing mid-save would drop the typed fields if the request then fails.
  it('stays open on Escape and disables Cancel while the save runs', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    qc.setQueryData(['ai-routers'], [llmRouter, ruleRouter])
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <StepDefinitionDialog open onClose={onClose} onSubmit={() => new Promise<void>(() => {})} />
        </MemoryRouter>
      </QueryClientProvider>,
    )
    await user.type(screen.getByTestId('step-definition-name'), 'Proofread')
    await user.click(screen.getByTestId('step-definition-submit'))
    expect(await screen.findByRole('button', { name: 'Saving…' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
    await user.keyboard('{Escape}')
    expect(onClose).not.toHaveBeenCalled()
  })

  it('surfaces the server rejection inline', async () => {
    const user = userEvent.setup()
    setup(vi.fn().mockRejectedValue(new Error('a step with this name already exists')))
    await user.type(screen.getByTestId('step-definition-name'), 'Review')
    await user.click(screen.getByTestId('step-definition-submit'))
    expect(await screen.findByRole('alert')).toHaveTextContent(/already exists/)
  })
})

describe('StepDefinitionDialog — edit', () => {
  // Anti-tautology RED proof: drop `!isDirty` from the submit's disabled
  // expression → Update is enabled before any change and this fails.
  it('says Update, disabled until something changed', async () => {
    const user = userEvent.setup()
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    qc.setQueryData(['ai-routers'], [llmRouter, ruleRouter])
    const initial = {
      id: 5, tenant_id: 1, name: 'Auto APE', step_type: 'ai', role_required: 'linguist', ai_router_id: 20,
      created_at: '', updated_at: '', deleted_at: '', ai_router_name: 'LLM router', workflow_count: 0,
    }
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <StepDefinitionDialog open onClose={() => {}} onSubmit={vi.fn().mockResolvedValue(undefined)} initial={initial} />
        </MemoryRouter>
      </QueryClientProvider>,
    )
    const submit = await screen.findByRole('button', { name: /^update$/i })
    // The dialog re-seeds its fields in an effect after opening.
    await screen.findByDisplayValue('Auto APE')
    await waitFor(() => expect(submit).toBeDisabled())
    await user.type(screen.getByTestId('step-definition-name'), ' v2')
    expect(submit).toBeEnabled()
  })

  // Anti-tautology RED proof: drop `aria-invalid={routerStale}` from the
  // router picker → the first assertion fails.
  it('marks the router picker invalid while it points at a deleted router', async () => {
    const user = userEvent.setup()
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    qc.setQueryData(['ai-routers'], [llmRouter, ruleRouter])
    const initial = {
      id: 5, tenant_id: 1, name: 'Auto APE', step_type: 'ai', role_required: 'linguist', ai_router_id: 99,
      created_at: '', updated_at: '', deleted_at: '', ai_router_name: '', workflow_count: 0,
    }
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <StepDefinitionDialog open onClose={() => {}} onSubmit={vi.fn().mockResolvedValue(undefined)} initial={initial} />
        </MemoryRouter>
      </QueryClientProvider>,
    )
    const router = await screen.findByTestId('step-definition-router')
    await waitFor(() => expect(router).toHaveAttribute('aria-invalid', 'true'))
    await user.selectOptions(router, '20')
    expect(router).not.toHaveAttribute('aria-invalid')
  })
})
