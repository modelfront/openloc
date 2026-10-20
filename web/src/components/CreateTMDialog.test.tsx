import { describe, it, expect, vi, beforeEach } from 'vitest'
import type { CustomerSelect } from './customer/CustomerSelect'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreateTMDialog } from './CreateTMDialog'

// Mock sonner
vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))

const mockCreateTMCollection = vi.fn()
vi.mock('../api', () => ({
  createTMCollection: (...args: unknown[]) => mockCreateTMCollection(...args),
  listLanguages: () => Promise.resolve([{ code: 'en', name: 'English' }, { code: 'es', name: 'Spanish' }]),
  getMe: () => Promise.resolve({ role: 'admin' }),
}))

// The real pickers use cmdk/Radix popovers that are hostile in jsdom. Swap them for plain inputs so we can drive the
// form and assert the submitted payload without fighting the popover.
vi.mock('@/components/ui/combobox', async () => (await import('@/test/combobox-mock')).comboboxInputMock)
vi.mock('./customer/CustomerSelect', () => ({
  CustomerSelect: ({ value, onChange }: Pick<React.ComponentProps<typeof CustomerSelect>, 'value' | 'onChange'>) => (
    <input
      aria-label="customer"
      value={value ?? ''}
      onChange={(e) => onChange(e.target.value ? Number(e.target.value) : null)}
    />
  ),
}))

function renderDialog(props: Partial<React.ComponentProps<typeof CreateTMDialog>> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const defaultProps = { open: true, onClose: vi.fn(), onCreated: vi.fn() }
  return render(
    <QueryClientProvider client={qc}>
      <CreateTMDialog {...defaultProps} {...props} />
    </QueryClientProvider>,
  )
}

// Fill name + source + one target — the minimum for a valid submit.
async function fillRequired(user: ReturnType<typeof userEvent.setup>, name = 'New TM') {
  await user.type(screen.getByPlaceholderText(/tm name/i), name)
  await user.type(screen.getByLabelText(/source language/i), 'en')
  await user.type(screen.getByLabelText(/add target languages/i), 'es')
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('CreateTMDialog', () => {
  it('renders name, source, target and customer fields', () => {
    renderDialog()
    expect(screen.getByPlaceholderText(/tm name/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/source language/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/add target languages/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /create/i })).toBeInTheDocument()
  })

  it('prefills source + target languages from the project when opened', () => {
    renderDialog({ initialSourceLanguage: 'en', initialTargetLanguages: ['es', 'fr'] })
    expect(screen.getByLabelText(/source language/i)).toHaveValue('en')
    expect(screen.getByLabelText(/add target languages/i)).toHaveValue('es,fr')
  })

  it('submits the prefilled languages when only a name is typed', async () => {
    mockCreateTMCollection.mockResolvedValue({ id: 5, name: 'Proj TM' })
    const user = userEvent.setup()
    renderDialog({ initialSourceLanguage: 'en', initialTargetLanguages: ['es', 'fr'] })

    await user.type(screen.getByPlaceholderText(/tm name/i), 'Proj TM')
    await user.click(screen.getByRole('button', { name: /create/i }))

    await waitFor(() => {
      expect(mockCreateTMCollection).toHaveBeenCalledWith({
        name: 'Proj TM',
        source_language: 'en',
        target_languages: ['es', 'fr'],
        customer_id: null,
      })
    })
  })

  it('submits name + source + targets + customer(null) with a trimmed name', async () => {
    mockCreateTMCollection.mockResolvedValue({ id: 99, name: 'New TM' })
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByPlaceholderText(/tm name/i), '  New TM  ')
    await user.type(screen.getByLabelText(/source language/i), 'en')
    await user.type(screen.getByLabelText(/add target languages/i), 'es')
    await user.click(screen.getByRole('button', { name: /create/i }))

    await waitFor(() => {
      expect(mockCreateTMCollection).toHaveBeenCalledWith({
        name: 'New TM',
        source_language: 'en',
        target_languages: ['es'],
        customer_id: null,
      })
    })
  })

  it('calls onCreated with the new TM id on success', async () => {
    mockCreateTMCollection.mockResolvedValue({ id: 99, name: 'New TM' })
    const onCreated = vi.fn()
    const user = userEvent.setup()
    renderDialog({ onCreated })

    await fillRequired(user)
    await user.click(screen.getByRole('button', { name: /create/i }))

    await waitFor(() => {
      expect(onCreated).toHaveBeenCalledWith(99)
    })
  })

  it('does not submit when name is empty (source/target set)', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/source language/i), 'en')
    await user.type(screen.getByLabelText(/add target languages/i), 'es')
    await user.click(screen.getByRole('button', { name: /create/i }))

    expect(mockCreateTMCollection).not.toHaveBeenCalled()
  })

  it('does not submit when no target language is chosen', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByPlaceholderText(/tm name/i), 'New TM')
    await user.type(screen.getByLabelText(/source language/i), 'en')
    await user.click(screen.getByRole('button', { name: /create/i }))

    expect(mockCreateTMCollection).not.toHaveBeenCalled()
  })

  it('calls onClose (not onCreated) when cancelled', async () => {
    const onClose = vi.fn()
    const onCreated = vi.fn()
    const user = userEvent.setup()
    renderDialog({ onClose, onCreated })

    await user.click(screen.getByRole('button', { name: /cancel/i }))

    expect(onClose).toHaveBeenCalled()
    expect(onCreated).not.toHaveBeenCalled()
  })

  it('shows error toast on API failure', async () => {
    const { toast } = await import('sonner')
    mockCreateTMCollection.mockRejectedValue(new Error('Server error'))
    const user = userEvent.setup()
    renderDialog()

    await fillRequired(user)
    await user.click(screen.getByRole('button', { name: /create/i }))

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalled()
    })
  })
})
