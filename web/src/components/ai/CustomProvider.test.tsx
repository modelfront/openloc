import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { AIVendorsSection } from './AIVendorsSection'
import { ApiError } from '../../api/request'

const toastSuccess = vi.fn()
vi.mock('sonner', () => ({ toast: { success: (...a: unknown[]) => toastSuccess(...a), error: vi.fn(), info: vi.fn() } }))

const listAIVendors = vi.fn()
const uploadExtension = vi.fn()
const deleteExtension = vi.fn()
vi.mock('../../api', () => ({
  listAIVendors: () => listAIVendors(),
  listMTEngineCatalog: () => Promise.resolve([]),
  uploadExtension: (file: File) => uploadExtension(file),
  deleteExtension: (id: string) => deleteExtension(id),
  createAIConnection: vi.fn(), updateAIConnection: vi.fn(),
  createVendorToken: vi.fn(), updateVendorToken: vi.fn(), deleteVendorToken: vi.fn(),
  listEnginesUsingKey: vi.fn(), listKeylessEngines: vi.fn(), attachKeyToEngines: vi.fn(),
}))

const MOCK_QE = {
  id: 'mockqe', name: 'Mock QE', version: '1.0.0', provider: 'ext:mockqe', capabilities: ['qe', 'ape'],
  settings: [{ key: 'token', label: 'API token', secret: true }, { key: 'base_url', label: 'Endpoint' }],
  allowed_hosts: ['api.example.com', '{settings.base_url}'],
  created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z',
}
const MOCK_QE_VENDOR = {
  provider: 'ext:mockqe', display_name: 'Mock QE', connected: false, tokens: [], connections: [], models: [],
  extension: {
    id: 'mockqe', version: '1.0.0', capabilities: ['qe', 'ape'],
    settings: MOCK_QE.settings, allowed_hosts: MOCK_QE.allowed_hosts,
  },
}
const TILE = 'ai-connection-tile-new-ext:mockqe'

function renderSection(canWrite: boolean) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const invalidate = vi.spyOn(qc, 'invalidateQueries')
  render(<MemoryRouter><QueryClientProvider client={qc}><AIVendorsSection defaultOpen canWrite={canWrite} /></QueryClientProvider></MemoryRouter>)
  return { invalidate, user: userEvent.setup() }
}

const zip = () => new File(['PK'], 'mockqe.zip', { type: 'application/zip' })
const invalidatedKeys = (invalidate: { mock: { calls: unknown[][] } }) =>
  invalidate.mock.calls.map(c => (c[0] as { queryKey?: unknown } | undefined)?.queryKey)

beforeEach(() => {
  vi.clearAllMocks()
  listAIVendors.mockResolvedValue([MOCK_QE_VENDOR])
})

describe('custom providers', () => {
  it('adds a custom provider from a zip and opens its panel', async () => {
    listAIVendors.mockResolvedValueOnce([]).mockResolvedValue([MOCK_QE_VENDOR])
    uploadExtension.mockResolvedValue(MOCK_QE)
    const { invalidate, user } = renderSection(true)
    const add = await screen.findByTestId('ai-custom-provider-add')
    // The add control sits in the QE+APE row, after the provider tiles.
    expect(within(screen.getByTestId('ai-connection-group-qe')).getByTestId('ai-custom-provider-add')).toBe(add)
    expect(add).toHaveTextContent('Add custom provider')

    const file = zip()
    await user.upload(screen.getByTestId('ai-custom-provider-file'), file)

    await waitFor(() => expect(uploadExtension).toHaveBeenCalledWith(file))
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith('Mock QE 1.0.0 added'))
    expect(invalidatedKeys(invalidate)).toContainEqual(['ai-vendors'])
    const panel = await screen.findByTestId('ai-connection-panel')
    expect(within(panel).getByTestId('ai-custom-provider-details')).toHaveTextContent('Version 1.0.0')
    expect(screen.getByTestId(TILE)).toHaveAttribute('aria-pressed', 'true')
    expect(screen.queryByTestId('ai-custom-provider-add-error')).not.toBeInTheDocument()
  })

  it('describes the dialog before the file picker, and Choose zip file opens it', async () => {
    const { user } = renderSection(true)
    await user.click(await screen.findByTestId('ai-custom-provider-add'))

    const dialog = await screen.findByTestId('ai-custom-provider-dialog')
    expect(within(dialog).getByText('Add a custom provider')).toBeInTheDocument()
    expect(uploadExtension).not.toHaveBeenCalled()

    const input = screen.getByTestId('ai-custom-provider-file') as HTMLInputElement
    const click = vi.spyOn(input, 'click')
    await user.click(within(dialog).getByTestId('ai-custom-provider-choose'))
    expect(click).toHaveBeenCalled()
  })

  it('shows why the server refused a zip in the dialog', async () => {
    uploadExtension.mockRejectedValue(new ApiError(400, 'manifest.json: capabilities must include qe or ape.', {
      error: { code: 'Bad Request', message: 'manifest.json: capabilities must include qe or ape.', field: 'file' },
    }))
    const { user } = renderSection(true)
    await screen.findByTestId('ai-custom-provider-add')
    // Picked without the dialog open (as a test or a re-pick does): the
    // refusal opens it, so the reason is never lost.
    await user.upload(screen.getByTestId('ai-custom-provider-file'), zip())

    const dialog = await screen.findByTestId('ai-custom-provider-dialog')
    expect(within(dialog).getByTestId('ai-custom-provider-add-error'))
      .toHaveTextContent('manifest.json: capabilities must include qe or ape.')
    expect(toastSuccess).not.toHaveBeenCalled()
    expect(screen.queryByTestId('ai-connection-panel')).not.toBeInTheDocument()
  })

  it('shows the details read-only without write access', async () => {
    const { user } = renderSection(false)
    await user.click(await screen.findByTestId(TILE))
    const details = await screen.findByTestId('ai-custom-provider-details')
    expect(details).toHaveTextContent('Version 1.0.0')
    expect(within(details).getByText('QE')).toBeInTheDocument()
    expect(within(details).getByText('APE')).toBeInTheDocument()
    // The template host is put in words, never shown raw.
    expect(within(details).getByText('Sends data to: api.example.com, the endpoint set on its key')).toBeInTheDocument()

    expect(screen.queryByTestId('ai-custom-provider-add')).not.toBeInTheDocument()
    expect(screen.queryByTestId('ai-custom-provider-file')).not.toBeInTheDocument()
    expect(screen.queryByTestId('ai-custom-provider-replace')).not.toBeInTheDocument()
    expect(screen.queryByTestId('ai-custom-provider-remove')).not.toBeInTheDocument()
  })

  it('replaces a custom provider with a new version', async () => {
    uploadExtension.mockResolvedValue({ ...MOCK_QE, version: '1.1.0' })
    const { invalidate, user } = renderSection(true)
    await user.click(await screen.findByTestId(TILE))
    await screen.findByTestId('ai-custom-provider-replace')
    const file = zip()
    await user.upload(screen.getByTestId('ai-custom-provider-replace-file'), file)

    await waitFor(() => expect(uploadExtension).toHaveBeenCalledWith(file))
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith('Mock QE updated to 1.1.0'))
    expect(invalidatedKeys(invalidate)).toContainEqual(['ai-vendors'])
  })

  it('removes a custom provider after confirmation and closes its panel', async () => {
    deleteExtension.mockResolvedValue(undefined)
    const { invalidate, user } = renderSection(true)
    await user.click(await screen.findByTestId(TILE))
    await user.click(await screen.findByTestId('ai-custom-provider-remove'))
    // Nothing is removed until the dialog is confirmed.
    expect(deleteExtension).not.toHaveBeenCalled()
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText('Remove Mock QE?')).toBeInTheDocument()
    expect(within(dialog).getByText('Its API keys are deleted. Engines that use them stop until you choose a new key for them.')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Remove' }))

    await waitFor(() => expect(deleteExtension).toHaveBeenCalledWith('mockqe'))
    expect(invalidatedKeys(invalidate)).toContainEqual(['ai-vendors'])
    await waitFor(() => expect(screen.queryByTestId('ai-connection-panel')).not.toBeInTheDocument())
  })
})
