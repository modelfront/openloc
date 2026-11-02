import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { QuickAddTermDialog } from './QuickAddTermDialog'
import { getTermLookupVersion, termLookupCache } from './SegmentRow.shared'
import type { JobTermBase, JobTermBases } from '../api'

vi.mock('@/components/ui/combobox', async () => (await import('@/test/combobox-mock')).comboboxSelectMock)
vi.mock('../api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api')>()),
  listJobTermBases: vi.fn(),
  quickAddJobTerm: vi.fn(),
}))

const { listJobTermBases, quickAddJobTerm } = await import('../api')
const mockList = vi.mocked(listJobTermBases)
const mockAdd = vi.mocked(quickAddJobTerm)

const JOB_ID = 7
const PROJECT_ID = 3
const MAIN: JobTermBase = { id: 11, name: 'Main' }
const LEGAL: JobTermBase = { id: 12, name: 'Legal' }
const bases = (...termBases: JobTermBase[]): JobTermBases => ({ term_bases: termBases, can_add: true, linguist_status: 'pending' })

function renderDialog(props: Partial<{ isManager: boolean; source: string; target: string; onClose: () => void }> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <QuickAddTermDialog
        open
        onClose={props.onClose ?? (() => {})}
        jobId={JOB_ID}
        projectId={PROJECT_ID}
        isManager={props.isManager ?? false}
        initialSource={props.source ?? 'password'}
        initialTarget={props.target ?? 'Passwort'}
      />
    </QueryClientProvider>,
  )
}

// The save button is aria-disabled, not disabled, until the term bases load.
async function saveReady() {
  await waitFor(() => expect(screen.getByTestId('quick-add-save')).not.toHaveAttribute('aria-disabled', 'true'))
}

beforeEach(() => {
  mockList.mockReset()
  mockAdd.mockReset()
  mockAdd.mockImplementation(async (_job, input) => ({
    concept_id: 1, term_id: 2, source_term: input.source_term, target_term: input.target_term,
    status: input.status ?? 'pending', created_concept: true,
  }))
})

describe('QuickAddTermDialog', () => {
  it('prefills the selection and saves a linguist term to the only base, with no status', async () => {
    mockList.mockResolvedValue(bases(MAIN))
    const onClose = vi.fn()
    renderDialog({ onClose })

    expect(await screen.findByTestId('quick-add-source')).toHaveValue('password')
    expect(screen.getByTestId('quick-add-target')).toHaveValue('Passwort')
    expect(screen.queryByText('Term base')).toBeNull()
    expect(screen.queryByText('Status')).toBeNull()
    expect(screen.getByText('The term is saved as pending until a project manager approves it.')).toBeInTheDocument()

    await userEvent.type(screen.getByTestId('quick-add-notes'), 'IT sense')
    await saveReady()
    await userEvent.click(screen.getByTestId('quick-add-save'))

    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(mockAdd).toHaveBeenCalledWith(JOB_ID, {
      term_base_id: MAIN.id, source_term: 'password', target_term: 'Passwort',
      status: undefined, definition: '', notes: 'IT sense',
    })
  })

  it('keeps what the user typed when the term bases arrive later', async () => {
    let resolveBases: (value: JobTermBases) => void = () => {}
    mockList.mockReturnValue(new Promise(resolve => { resolveBases = resolve }))
    renderDialog({ target: '' })

    await userEvent.type(screen.getByTestId('quick-add-target'), 'Zurücksetzen')
    resolveBases(bases(MAIN))
    await saveReady()
    expect(screen.getByTestId('quick-add-target')).toHaveValue('Zurücksetzen')
    await userEvent.click(screen.getByTestId('quick-add-save'))

    await waitFor(() => expect(mockAdd).toHaveBeenCalled())
    expect(mockAdd.mock.calls[0][1]).toMatchObject({ term_base_id: MAIN.id, target_term: 'Zurücksetzen' })
  })

  it('tells a linguist their term is saved as approved when the project says so', async () => {
    mockList.mockResolvedValue({ term_bases: [MAIN], can_add: true, linguist_status: 'approved' })
    renderDialog()
    expect(await screen.findByText('The term is saved as approved.')).toBeInTheDocument()
    expect(screen.queryByText(/saved as pending/)).toBeNull()
  })

  it('lets a manager pick the base and the status', async () => {
    mockList.mockResolvedValue(bases(MAIN, LEGAL))
    renderDialog({ isManager: true })

    await waitFor(() => expect(screen.getAllByRole('combobox')).toHaveLength(2))
    const pickers = screen.getAllByRole('combobox')
    await userEvent.selectOptions(pickers[0], String(LEGAL.id))
    await userEvent.selectOptions(pickers[1], 'forbidden')
    await userEvent.click(screen.getByTestId('quick-add-save'))

    await waitFor(() => expect(mockAdd).toHaveBeenCalled())
    expect(mockAdd.mock.calls[0][1]).toMatchObject({ term_base_id: LEGAL.id, status: 'forbidden' })
  })

  it('refuses an empty target and saves nothing', async () => {
    mockList.mockResolvedValue(bases(MAIN))
    renderDialog({ target: '' })
    await saveReady()
    await userEvent.click(screen.getByTestId('quick-add-save'))
    expect(await screen.findByText('Target term is required')).toBeInTheDocument()
    expect(mockAdd).not.toHaveBeenCalled()
  })

  it('says so when the term bases fail to load, instead of loading forever', async () => {
    mockList.mockRejectedValue(new Error('boom'))
    renderDialog()
    expect(await screen.findByText('The term bases could not be loaded. Try again.')).toBeInTheDocument()
    expect(screen.queryByTestId('quick-add-save')).toBeNull()
  })

  it('tells a linguist when the project does not let linguists add terms', async () => {
    mockList.mockResolvedValue({ term_bases: [MAIN], can_add: false, linguist_status: 'pending' })
    renderDialog()
    expect(await screen.findByText('Adding terms is turned off for this project. Ask a project manager to turn on Linguists can add terms.')).toBeInTheDocument()
    expect(screen.queryByTestId('quick-add-save')).toBeNull()
  })

  it('says so when the project uses no term base for the pair', async () => {
    mockList.mockResolvedValue(bases())
    renderDialog()
    expect(await screen.findByText(/No term base on this project takes new terms for this language pair\./)).toBeInTheDocument()
    expect(screen.queryByTestId('quick-add-save')).toBeNull()
  })

  it('drops the project\'s cached term lookups after a save', async () => {
    mockList.mockResolvedValue(bases(MAIN))
    termLookupCache.set(`${PROJECT_ID}:en:de:Reset your password`, [])
    termLookupCache.set(`99:en:de:Reset your password`, [])
    const before = getTermLookupVersion()
    renderDialog()
    await saveReady()
    await userEvent.click(screen.getByTestId('quick-add-save'))

    await waitFor(() => expect(getTermLookupVersion()).toBe(before + 1))
    expect(termLookupCache.has(`${PROJECT_ID}:en:de:Reset your password`)).toBe(false)
    expect(termLookupCache.has(`99:en:de:Reset your password`)).toBe(true)
  })

  it('warns that a long source term is not highlighted in segments', async () => {
    mockList.mockResolvedValue(bases(MAIN))
    renderDialog({ source: 'reset your account password now' })
    expect(await screen.findByText('Terms of more than 4 words are not highlighted in segments.')).toBeInTheDocument()
  })
})
