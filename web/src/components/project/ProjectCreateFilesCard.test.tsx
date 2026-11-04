// The wizard's Files card pages through the draft's files: the server sends
// 100 at a time, and scrolling to the end loads the next page. A dropped file
// whose format has profiles of the org's own asks for its filter profile.

import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup, waitFor, act, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { FilterProfileChoice } from '../../api'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const mockListFiles = vi.fn()
const mockListFilterProfiles = vi.fn()
const mockUploadFile = vi.fn()
const mockToastError = vi.fn()
vi.mock('sonner', () => ({ toast: { error: (...a: unknown[]) => mockToastError(...a), success: vi.fn() } }))
vi.mock('../../api', async (importOriginal) => ({
  ...await importOriginal<typeof import('../../api')>(),
  listFiles: (...a: unknown[]) => mockListFiles(...a),
  listFilterProfiles: (...a: unknown[]) => mockListFilterProfiles(...a),
  uploadFile: (...a: unknown[]) => mockUploadFile(...a),
}))

// jsdom has no layout, so the virtualizer would mount no rows: render every
// row, and expose the load-more callback the real table calls near the end.
const loadMore = vi.hoisted(() => ({ current: undefined as undefined | (() => void) }))
vi.mock('@/components/ui/virtual-data-table', () => ({
  VirtualDataTable: ({ data, columns, onLoadMore }: {
    data: unknown[]
    columns: Array<{ cell?: (ctx: { row: { original: unknown } }) => React.ReactNode }>
    onLoadMore?: () => void
  }) => {
    loadMore.current = onLoadMore
    return (
      <table><tbody>
        {data.map((row, i) => <tr key={i}>{columns.map((c, j) => <td key={j}>{c.cell?.({ row: { original: row } })}</td>)}</tr>)}
      </tbody></table>
    )
  },
}))

import { ProjectCreateFilesCard } from './ProjectCreateFilesCard'
import { makeFilterProfile } from '@/test/fixtures/filter-profile'

const file = (id: number) => ({ id, project_id: 5, name: `file-${id}.txt`, original_name: null, format: 'txt', segment_count: 3 })
const page = (from: number, count: number, more: boolean) => ({
  data: Array.from({ length: count }, (_, i) => file(from + i)),
  cursor: more ? from + count - 1 : 0,
  has_more: more,
  total: 150,
})

afterEach(() => {
  cleanup()
  mockListFiles.mockReset()
  mockListFilterProfiles.mockReset()
  mockUploadFile.mockReset()
})

function renderCard(filterChoices?: FilterProfileChoice[], ensureDraft: () => Promise<number> = async () => 5) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <ProjectCreateFilesCard draftId={5} ensureDraft={ensureDraft} analyze={false} onAnalyzeChange={() => {}} filterChoices={filterChoices} />
    </QueryClientProvider>,
  )
}

describe('ProjectCreateFilesCard — many files', () => {
  it('shows the total and loads the files past the first page', async () => {
    mockListFiles.mockImplementation((_id: number, opts?: { cursor?: number }) =>
      Promise.resolve(opts?.cursor ? page(101, 50, false) : page(1, 100, true)))
    renderCard()

    await screen.findByTestId('create-file-100')
    expect(screen.getByText('Files (150)')).toBeInTheDocument()
    expect(screen.queryByTestId('create-file-101')).toBeNull()

    act(() => loadMore.current?.())
    await screen.findByTestId('create-file-150')
    expect(mockListFiles).toHaveBeenLastCalledWith(5, expect.objectContaining({ cursor: 100 }))
    await waitFor(() => expect(screen.getAllByTestId(/^create-file-\d+$/)).toHaveLength(150))
  })
})

const profile = makeFilterProfile
const builtins = [
  profile(1, 'json_i18n', 'Default', 'builtin:json_i18n-default@1'),
  profile(2, 'json_i18n', 'Plain text strings', 'builtin:json_i18n-plain@1'),
]

/** Waits until the card holds the org's filter profiles. */
async function profilesLoaded() {
  await waitFor(() => expect(mockListFilterProfiles).toHaveBeenCalled())
  await act(async () => { await mockListFilterProfiles.mock.results[0].value })
}

/** Drops `files` on the card, through its file input. */
function drop(files: File[]) {
  const input = screen.getByTestId('create-file-dropzone').querySelector('input[type="file"]')!
  fireEvent.change(input, { target: { files } })
}

describe('ProjectCreateFilesCard — filter profiles', () => {
  it('uploads straight away when the format has only built-ins', async () => {
    mockListFiles.mockResolvedValue(page(1, 0, false))
    mockListFilterProfiles.mockResolvedValue(builtins)
    mockUploadFile.mockResolvedValue({ data: {} })
    renderCard()
    await profilesLoaded()

    drop([new File(['{}'], 'strings.json')])
    await waitFor(() => expect(mockUploadFile).toHaveBeenCalledOnce())
    expect(screen.queryByTestId('upload-filter-dialog')).toBeNull()
    expect(mockUploadFile.mock.calls[0][3].filterProfileId).toBeUndefined()
  })

  it('asks when the org has its own profile, starting from the form\'s pick, and sends a changed one', async () => {
    const user = userEvent.setup()
    mockListFiles.mockResolvedValue(page(1, 0, false))
    mockListFilterProfiles.mockResolvedValue([...builtins, profile(70, 'json_i18n', 'App strings')])
    mockUploadFile.mockResolvedValue({ data: {} })
    renderCard([{ format: 'json_i18n', id: 2, name: 'Plain text strings' }])
    await profilesLoaded()

    drop([new File(['{}'], 'strings.json')])
    const picker = await screen.findByTestId('upload-filter-strings.json')
    await waitFor(() => expect(picker).toHaveTextContent('Plain text strings'))
    expect(mockUploadFile).not.toHaveBeenCalled()

    await user.click(picker)
    await user.click(screen.getByTestId('upload-filter-strings.json-option-70'))
    await user.click(screen.getByTestId('upload-filter-confirm'))
    await waitFor(() => expect(mockUploadFile).toHaveBeenCalledOnce())
    expect(mockUploadFile.mock.calls[0][0]).toBe(5)
    expect(mockUploadFile.mock.calls[0][3]).toMatchObject({ filterProfileId: 70 })
  })

  // Only a pick the user changed is sent: a .xml may be Android string
  // resources, which have no filter profiles, and the preselected XML
  // Default means nothing for one.
  it('sends no pick for a file whose profile was left as preselected', async () => {
    const user = userEvent.setup()
    mockListFiles.mockResolvedValue(page(1, 0, false))
    mockListFilterProfiles.mockResolvedValue([...builtins, profile(3, 'xml', 'Default', 'builtin:xml-default@1'), profile(80, 'xml', 'Docs XML')])
    mockUploadFile.mockResolvedValue({ data: {} })
    renderCard()
    await profilesLoaded()

    drop([new File(['<resources/>'], 'strings.xml')])
    await screen.findByTestId('upload-filter-dialog')
    await user.click(screen.getByTestId('upload-filter-confirm'))
    await waitFor(() => expect(mockUploadFile).toHaveBeenCalledOnce())
    expect(mockUploadFile.mock.calls[0][3].filterProfileId).toBeUndefined()
  })

  it('says so when the draft cannot be saved, and uploads nothing', async () => {
    mockListFiles.mockResolvedValue(page(1, 0, false))
    mockListFilterProfiles.mockResolvedValue(builtins)
    renderCard(undefined, () => Promise.reject(new Error('offline')))
    await profilesLoaded()

    drop([new File(['{}'], 'strings.json')])
    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith(expect.stringContaining('offline')))
    expect(mockUploadFile).not.toHaveBeenCalled()
  })

  it('uploads nothing when the question is cancelled', async () => {
    const user = userEvent.setup()
    mockListFiles.mockResolvedValue(page(1, 0, false))
    mockListFilterProfiles.mockResolvedValue([...builtins, profile(70, 'json_i18n', 'App strings')])
    renderCard()
    await profilesLoaded()

    drop([new File(['{}'], 'strings.json')])
    await screen.findByTestId('upload-filter-dialog')
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByTestId('upload-filter-dialog')).toBeNull())
    expect(mockUploadFile).not.toHaveBeenCalled()
  })
})
