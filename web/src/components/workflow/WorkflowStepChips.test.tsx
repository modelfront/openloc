// The "(deleted router)" warning belongs to steps that run an AI router —
// hybrid and ai. A Pre-translation step never has one, so it must not wear it.
import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { WorkflowStepChips, type ChipStep } from './WorkflowStepChips'

afterEach(cleanup)

function renderChips(steps: ChipStep[]) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(['ai-routers'], [])
  qc.setQueryData(['members'], [])
  render(<MemoryRouter><QueryClientProvider client={qc}><WorkflowStepChips steps={steps} /></QueryClientProvider></MemoryRouter>)
}

describe('WorkflowStepChips — deleted router warning', () => {
  it('does not flag a Pre-translation step, which has no router', () => {
    renderChips([{ name: 'Pre-translation', step_type: 'pretranslate', ai_router_id: null }])
    expect(screen.getByTestId('step-type-pretranslate')).toBeInTheDocument()
    expect(screen.queryByText('(deleted router)')).not.toBeInTheDocument()
    expect(screen.getByText('→ Automated')).toBeInTheDocument()
  })

  it('still flags an ai step whose router is gone', () => {
    renderChips([{ name: 'Auto APE', step_type: 'ai', ai_router_id: null }])
    expect(screen.getByText('(deleted router)')).toBeInTheDocument()
  })
})

describe('pre-translation row', () => {
  it('heads the rail unnumbered, with the steps still counting from 1', () => {
    const qc = new QueryClient()
    render(
      <MemoryRouter>
        <QueryClientProvider client={qc}>
          <WorkflowStepChips
            steps={[{ name: 'Review', step_type: 'human', step_definition_id: 1 }]}
            pretranslation={<span>Default MT router</span>}
          />
        </QueryClientProvider>
      </MemoryRouter>,
    )
    const row = screen.getByTestId('workflow-pretranslation')
    expect(row).toHaveTextContent('Pre-translation')
    expect(row).toHaveTextContent('Default MT router')
    expect(row.querySelector('[aria-hidden]')).not.toHaveTextContent(/\d/)
    expect(screen.getByTestId('workflow-step-badge-0')).toHaveTextContent('Review')
    expect(screen.getByTestId('workflow-step-dot-0')).toHaveTextContent('1')
  })
})

describe('AI router name', () => {
  it('links to the AI page pointing at that router\'s card', () => {
    const qc = new QueryClient()
    qc.setQueryData(['ai-routers'], [])
    qc.setQueryData(['members'], [])
    render(
      <MemoryRouter>
        <QueryClientProvider client={qc}>
          <WorkflowStepChips steps={[{ name: 'Auto APE', step_type: 'ai', step_definition_id: 2, ai_router_id: 12, ai_router_name: 'Default QE+APE router' }]} />
        </QueryClientProvider>
      </MemoryRouter>,
    )
    expect(screen.getByRole('link', { name: 'Default QE+APE router' })).toHaveAttribute('href', '/ai?attention=ai-router-12')
  })
})
