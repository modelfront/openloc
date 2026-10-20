import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { LabelManager } from './LabelManager'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const listLabels = vi.fn()
vi.mock('../../api', () => ({
  listLabels: (...a: unknown[]) => listLabels(...a),
  createLabel: vi.fn(),
  deleteLabel: vi.fn(),
  createLabelValue: vi.fn(),
  deleteLabelValue: vi.fn(),
}))

function renderManager() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <LabelManager canEdit />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('LabelManager', () => {
  it('a failed load shows the error with a Retry, not "No labels yet"', async () => {
    const user = userEvent.setup()
    listLabels.mockRejectedValueOnce(new Error('server unavailable'))
    renderManager()

    expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't load the labels: server unavailable")
    expect(screen.queryByText(/No labels yet/)).not.toBeInTheDocument()

    listLabels.mockResolvedValueOnce([
      { id: 1, name: 'Country', system: false, in_use: false, values: [] },
    ])
    await user.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByTestId('label-row-1')).toHaveTextContent('Country')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
