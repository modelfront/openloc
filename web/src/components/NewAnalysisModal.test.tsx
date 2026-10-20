import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import NewAnalysisModal from './NewAnalysisModal'

// listJobs is disabled while the modal is closed, so it returns `undefined`
// and the component's `jobs = []` destructuring default produces a fresh array
// each render — the condition an effect-synced selection loops on. Keep the
// mock returning data so the open-state path is also exercised.
vi.mock('../api/analytics', () => ({
  listJobs: () => Promise.resolve([
    { id: 1, file_name: 'a.docx', target_language: 'es' },
    { id: 2, file_name: 'b.docx', target_language: 'fr' },
  ]),
}))
vi.mock('../api/analyses', () => ({
  createAnalysis: vi.fn(),
}))

function renderModal(props: Partial<React.ComponentProps<typeof NewAnalysisModal>> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const defaultProps = { open: false, projectId: 1, onOpenChange: vi.fn(), onCreated: vi.fn() }
  return render(
    <QueryClientProvider client={qc}>
      <NewAnalysisModal {...defaultProps} {...props} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('NewAnalysisModal', () => {
  // Pins that a closed modal mounts and settles without an update-depth loop
  // ("Maximum update depth exceeded").
  it('mounts closed without an infinite render loop', () => {
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    expect(() => renderModal({ open: false })).not.toThrow()
    const maxDepth = errorSpy.mock.calls.some(args =>
      args.some(a => typeof a === 'string' && a.includes('Maximum update depth')),
    )
    expect(maxDepth).toBe(false)
    errorSpy.mockRestore()
  })

  it('shows the job picker when opened', async () => {
    renderModal({ open: true })
    await waitFor(() => {
      expect(screen.getByText('a.docx')).toBeInTheDocument()
      expect(screen.getByText('b.docx')).toBeInTheDocument()
    })
  })
})
