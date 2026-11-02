import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { toast } from 'sonner'
import { QARegexRuleDialog } from './QARegexRuleDialog'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const mockSave = vi.fn()
vi.mock('../api', () => ({
  saveQARegexRule: (...args: unknown[]) => mockSave(...args),
  listLanguages: () => Promise.resolve([{ code: 'fr', name: 'French' }]),
}))
vi.mock('@/components/ui/combobox', async () => (await import('@/test/combobox-mock')).comboboxSelectMock)

function renderDialog(rule: React.ComponentProps<typeof QARegexRuleDialog>['rule'] = null) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const onOpenChange = vi.fn()
  render(
    <QueryClientProvider client={qc}>
      <QARegexRuleDialog profileId={3} rule={rule} open onOpenChange={onOpenChange} />
    </QueryClientProvider>,
  )
  return { onOpenChange }
}

beforeEach(() => {
  vi.clearAllMocks()
  mockSave.mockResolvedValue({ id: 9 })
})

describe('QARegexRuleDialog', () => {
  it('inserts special spaces at the caret and sends the text exactly as typed', async () => {
    const user = userEvent.setup()
    const { onOpenChange } = renderDialog()
    await user.type(screen.getByTestId('regex-name'), 'Unit space')
    const pattern = screen.getByTestId('regex-pattern')
    await user.type(pattern, '\\d(kg)')
    // Caret after "\d": the NBSP goes between the digit and the unit.
    ;(pattern as HTMLInputElement).setSelectionRange(2, 2)
    await user.click(screen.getAllByRole('button', { name: 'NBSP' })[0])
    await waitFor(() => expect(pattern).toHaveFocus())
    expect((pattern as HTMLInputElement).selectionStart).toBe(3)
    await user.type(screen.getByTestId('regex-message'), 'Use a non-breaking space ')
    await user.click(screen.getByTestId('regex-save'))

    await waitFor(() => expect(mockSave).toHaveBeenCalledTimes(1))
    const [profileId, rule] = mockSave.mock.calls[0]
    expect(profileId).toBe(3)
    expect(rule.config).toEqual({
      name: 'Unit space',
      mode: 'target_must_not_match',
      pattern: '\\d\u00a0(kg)',
      message: 'Use a non-breaking space ',
      target_languages: [],
      ignore_case: false,
    })
    expect(rule.severity).toBe('error')
    expect(screen.getByTestId('special-space-preview')).toHaveTextContent('NBSP')
    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
  })

  it('shows a server rejection under the field it names, without a toast', async () => {
    mockSave.mockRejectedValue(new Error('pattern: missing closing ): `(`'))
    const user = userEvent.setup()
    renderDialog({ id: 4, severity: 'warning', enabled: true, config: { name: 'n', mode: 'target_must_match', pattern: 'x', message: 'm', target_languages: ['fr'], ignore_case: true } })
    await user.clear(screen.getByTestId('regex-pattern'))
    await user.type(screen.getByTestId('regex-pattern'), '(')
    await user.click(screen.getByTestId('regex-save'))

    expect(await screen.findByText('Missing closing ): `(`.')).toBeInTheDocument()
    expect(mockSave.mock.calls[0][1]).toMatchObject({ ruleId: 4, severity: 'warning', config: { target_languages: ['fr'], ignore_case: true } })
    expect(toast.error).not.toHaveBeenCalled()
  })

  it('requires a source pattern in the source-and-target mode', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.type(screen.getByTestId('regex-name'), 'Brand kept')
    await user.selectOptions(screen.getByTestId('regex-mode'), 'source_match_target_not')
    await user.type(screen.getByTestId('regex-pattern'), 'ACME')
    await user.type(screen.getByTestId('regex-message'), 'Keep the brand name.')
    await user.click(screen.getByTestId('regex-save'))

    expect(await screen.findByText('Source pattern is required')).toBeInTheDocument()
    expect(mockSave).not.toHaveBeenCalled()
  })
})
