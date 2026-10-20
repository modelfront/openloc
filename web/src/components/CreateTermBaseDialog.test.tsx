import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreateTermBaseDialog } from './CreateTermBaseDialog'
import { queryKeys } from '../lib/query-keys'
import type { TermBase } from '../api/terminology'

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))

const mockCreateTermBase = vi.fn()
const mockListLanguages = vi.fn()
vi.mock('../api', () => ({
  createTermBase: (...args: unknown[]) => mockCreateTermBase(...args),
  listLanguages: (...args: unknown[]) => mockListLanguages(...args),
  // The dialog reads `me` to stamp created_by_name on its optimistic row
  getMe: () => Promise.resolve({ id: 1, name: 'Test User', email: 'test@example.com' }),
}))

function renderDialog(props: Partial<React.ComponentProps<typeof CreateTermBaseDialog>> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const defaultProps = {
    open: true,
    onClose: vi.fn(),
    onCreated: vi.fn(),
  }
  return {
    qc,
    ...render(
      <QueryClientProvider client={qc}>
        <CreateTermBaseDialog {...defaultProps} {...props} />
      </QueryClientProvider>,
    ),
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  mockListLanguages.mockResolvedValue([
    { code: 'en', name: 'English' },
    { code: 'es', name: 'Spanish' },
    { code: 'de', name: 'German' },
  ])
})

describe('CreateTermBaseDialog', () => {
  it('renders name input and Create button', async () => {
    renderDialog()
    expect(screen.getByPlaceholderText(/term base name/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /create/i })).toBeInTheDocument()
  })

  it('calls createTermBase with name + source language on submit', async () => {
    mockCreateTermBase.mockResolvedValue({ id: 77, name: 'Legal TB', source_language: 'en', target_languages: [] })
    const user = userEvent.setup()
    renderDialog({ initialSrcLang: 'en' })

    await user.type(screen.getByPlaceholderText(/term base name/i), 'Legal TB')
    await user.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => {
      expect(mockCreateTermBase).toHaveBeenCalledWith('Legal TB', '', 'en')
    })
  })

  it('pre-fills source language from props', async () => {
    renderDialog({ initialSrcLang: 'en' })
    // The source lang field should show the pre-filled value ("en — English").
    await waitFor(() => {
      expect(screen.getByText(/English/i)).toBeInTheDocument()
    })
  })

  it('calls onCreated with the new TB id on success', async () => {
    mockCreateTermBase.mockResolvedValue({ id: 77, name: 'Legal TB', source_language: 'en', target_languages: [] })
    const onCreated = vi.fn()
    const user = userEvent.setup()
    renderDialog({ initialSrcLang: 'en', onCreated })

    await user.type(screen.getByPlaceholderText(/term base name/i), 'Legal TB')
    await user.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => {
      expect(onCreated).toHaveBeenCalledWith(77)
    })
  })

  it('does not submit when name is empty', async () => {
    const user = userEvent.setup()
    renderDialog({ initialSrcLang: 'en', initialTgtLang: 'es' })

    await user.click(screen.getByRole('button', { name: /^create$/i }))

    expect(mockCreateTermBase).not.toHaveBeenCalled()
  })

  // POST /api/terminology returns the raw term_bases row —
  // it has no `languages` and no `entry_count`, because those are derived by
  // the LIST query. TerminologyList renders `base.languages.length` and the
  // concept count, so an optimistic row copied straight from the create
  // response blows the page up for anyone who lands on the list before the
  // invalidation refetch replaces it. Assert the cached row is renderable.
  it('writes a list-shaped row into the cache (not the bare create response)', async () => {
    mockCreateTermBase.mockResolvedValue({ id: 77, name: 'Legal TB', source_language: 'en', target_languages: [] })
    const user = userEvent.setup()
    const { qc } = renderDialog({ initialSrcLang: 'en' })

    await user.type(screen.getByPlaceholderText(/term base name/i), 'Legal TB')
    await user.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => {
      expect(qc.getQueryData<TermBase[]>(queryKeys.terminology.all)).toBeTruthy()
    })
    const row = qc.getQueryData<TermBase[]>(queryKeys.terminology.all)![0]
    expect(Array.isArray(row.languages)).toBe(true)
    expect(typeof row.entry_count).toBe('number')
  })

  it('calls onClose when cancelled', async () => {
    const onClose = vi.fn()
    const user = userEvent.setup()
    renderDialog({ onClose })

    await user.click(screen.getByRole('button', { name: /cancel/i }))
    expect(onClose).toHaveBeenCalled()
  })
})
