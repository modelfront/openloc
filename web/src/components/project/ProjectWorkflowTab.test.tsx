// A project tab whose settings query failed must say so, not spin forever:
// with `settings={null}` and an error, the tab shows an error, not
// "Loading workflow...".
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { ProjectWorkflowTab } from './ProjectWorkflowTab'

function setup(props: Partial<Parameters<typeof ProjectWorkflowTab>[0]> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const onRefresh = vi.fn()
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <ProjectWorkflowTab projectId={7} settings={null} onRefresh={onRefresh} {...props} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { onRefresh }
}

describe('ProjectWorkflowTab load states', () => {
  it('shows a loading line while the settings are on their way', () => {
    setup()
    expect(screen.getByText(/loading workflow/i)).toBeInTheDocument()
  })

  it('shows the error and a Retry button when the settings request failed', async () => {
    const user = userEvent.setup()
    const { onRefresh } = setup({ error: new Error('project not found') })
    expect(screen.queryByText(/loading workflow/i)).not.toBeInTheDocument()
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent(/couldn't load the workflow/i)
    expect(alert).toHaveTextContent(/project not found/i)
    await user.click(screen.getByRole('button', { name: /retry/i }))
    expect(onRefresh).toHaveBeenCalledTimes(1)
  })
})
