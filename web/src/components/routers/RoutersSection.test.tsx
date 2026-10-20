import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RoutersSection, type RouterAdapter, type RouterLike } from './RoutersSection'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

vi.mock('../../api', () => ({
  listLanguages: () => Promise.resolve([{ code: 'de', name: 'German' }]),
}))

const list = vi.fn<() => Promise<RouterLike[]>>()

const adapter: RouterAdapter<RouterLike, unknown> = {
  noun: 'test router',
  testIdPrefix: 'test-router',
  deleteDescription: '',
  queryKey: ['test-routers'],
  list: () => list(),
  create: vi.fn(),
  update: vi.fn(),
  remove: vi.fn(),
  invalidateKeys: [['test-routers']],
  engineOptions: [],
  emptyEngineText: '',
  toShape: () => ({ defaultEngine: null, rules: [] }),
  fromShape: () => ({}),
}

function renderSection() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <RoutersSection canWrite={false} adapter={adapter} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('RoutersSection', () => {
  it('a failed load shows the error with a Retry, not "No test routers yet"', async () => {
    const user = userEvent.setup()
    list.mockRejectedValueOnce(new Error('server unavailable'))
    renderSection()

    expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't load the test routers: server unavailable")
    expect(screen.queryByText('No test routers yet.')).not.toBeInTheDocument()

    list.mockResolvedValueOnce([{ id: 7, name: 'Production' }])
    await user.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByTestId('test-router-7')).toHaveTextContent('Production')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
