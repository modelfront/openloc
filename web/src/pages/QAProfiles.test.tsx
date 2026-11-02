import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import QAProfiles from './QAProfiles'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const languages = [{ language: 'es', decimal: '.', group: ',' }]
const numberFormatRule = { id: 5, profile_id: 1, check_type: 'number_format', enabled: true, severity: 'warning', config: { mode: 'require', languages } }
const mockUpsert = vi.fn()
const mockGetProfile = vi.fn()
vi.mock('../api', () => ({
  listQAProfiles: () => Promise.resolve([{ id: 1, name: 'Strict', description: '', created_at: '2026-10-01T00:00:00Z' }]),
  getQAProfile: () => mockGetProfile(),
  upsertQARule: (...args: unknown[]) => mockUpsert(...args),
  createQAProfile: vi.fn(),
  updateQAProfile: vi.fn(),
  deleteQAProfile: vi.fn(),
  exportQAProfile: vi.fn(),
  importQAProfile: vi.fn(),
  replaceQAProfileRules: vi.fn(),
  saveQARegexRule: vi.fn(),
  deleteQARule: vi.fn(),
  listLanguages: () => Promise.resolve([{ code: 'es', name: 'Spanish' }, { code: 'fr', name: 'French' }]),
}))
vi.mock('@/components/ui/combobox', async () => (await import('@/test/combobox-mock')).comboboxSelectMock)

// The server: a save stores the rule and answers with it; a fetch returns what is stored.
beforeEach(() => {
  vi.clearAllMocks()
  let stored = numberFormatRule
  mockUpsert.mockImplementation((_profileId: number, _checkType: string, enabled: boolean, severity: string, config: Record<string, unknown>) => {
    stored = { ...stored, enabled, severity, config: config as typeof numberFormatRule.config }
    return Promise.resolve(stored)
  })
  mockGetProfile.mockImplementation(() => Promise.resolve({ profile: { id: 1, name: 'Strict' }, rules: [stored] }))
})

async function openNumberFormatRow() {
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter><QAProfiles /></MemoryRouter>
    </QueryClientProvider>,
  )
  await user.click(await screen.findByText('Strict'))
  return { user, row: await screen.findByTestId('qa-rule-number_format') }
}

describe('QA profile Number format rule', () => {
  it('keeps the language formats when the severity or the mode changes, each save starting from the last', async () => {
    const { user, row } = await openNumberFormatRow()
    expect(within(row).getByTestId('qa-number-format-languages')).toBeInTheDocument()
    // The controls are held still while a save runs.
    const settled = () => waitFor(() => expect(within(row).getByText('Info')).not.toBeDisabled())

    await user.click(within(row).getByText('Error'))
    await waitFor(() => expect(mockUpsert).toHaveBeenCalledTimes(1))
    expect(mockUpsert).toHaveBeenLastCalledWith(1, 'number_format', true, 'error', { mode: 'require', languages })
    await settled()

    await user.click(within(row).getByRole('radio', { name: 'Source or target format' }))
    await waitFor(() => expect(mockUpsert).toHaveBeenCalledTimes(2))
    expect(mockUpsert).toHaveBeenLastCalledWith(1, 'number_format', true, 'error', { mode: 'allow', languages })
    await settled()

    await user.click(within(row).getByText('Disabled'))
    await waitFor(() => expect(mockUpsert).toHaveBeenCalledTimes(3))
    expect(mockUpsert).toHaveBeenLastCalledWith(1, 'number_format', false, 'error', { mode: 'allow', languages })
    // Off: the list is hidden but kept, and back on it shows again.
    await waitFor(() => expect(within(row).queryByTestId('qa-number-format-languages')).toBeNull())
    await settled()

    await user.click(within(row).getByText('Warning'))
    await waitFor(() => expect(mockUpsert).toHaveBeenCalledTimes(4))
    expect(mockUpsert).toHaveBeenLastCalledWith(1, 'number_format', true, 'warning', { mode: 'allow', languages })
    expect(await within(row).findByTestId('qa-number-format-language-es')).toBeInTheDocument()
  })

  it('builds the next save on the one just saved, before the profile is fetched again', async () => {
    const { user, row } = await openNumberFormatRow()
    const withFrench = [...languages, { language: 'fr', decimal: ',', group: ' ' }]
    // The refetch after the save has not come back yet.
    mockGetProfile.mockReturnValue(new Promise(() => {}))

    await user.type(within(row).getByTestId('lang'), 'fr')
    await user.selectOptions(within(row).getByTestId('qa-number-format-new-format'), JSON.stringify([',', ' ']))
    await user.click(within(row).getByTestId('qa-number-format-add'))
    await waitFor(() => expect(mockUpsert).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(within(row).getByTestId('qa-number-format-language-fr')).toBeInTheDocument())

    await user.click(within(row).getByRole('radio', { name: 'Source or target format' }))
    await waitFor(() => expect(mockUpsert).toHaveBeenCalledTimes(2))
    expect(mockUpsert).toHaveBeenLastCalledWith(1, 'number_format', true, 'warning', { mode: 'allow', languages: withFrench })
  })

  it('keeps the rule controls still while a save is in flight', async () => {
    const { user, row } = await openNumberFormatRow()
    mockUpsert.mockReturnValue(new Promise(() => {}))
    await user.click(within(row).getByText('Error'))
    await waitFor(() => expect(within(row).getByRole('radio', { name: 'Source or target format' })).toBeDisabled())
    expect(within(row).getByText('Info')).toBeDisabled()
  })
})

describe('QA profile Placeables rule', () => {
  it('shows its three sub-checks while on, and saves each change with all three', async () => {
    let stored = { id: 7, profile_id: 1, check_type: 'placeables', enabled: false, severity: 'warning', config: {} as Record<string, unknown> }
    mockUpsert.mockImplementation((_p: number, _c: string, enabled: boolean, severity: string, config: Record<string, unknown>) => {
      stored = { ...stored, enabled, severity, config }
      return Promise.resolve(stored)
    })
    mockGetProfile.mockImplementation(() => Promise.resolve({ profile: { id: 1, name: 'Strict' }, rules: [stored] }))
    const user = userEvent.setup()
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MemoryRouter><QAProfiles /></MemoryRouter>
      </QueryClientProvider>,
    )
    await user.click(await screen.findByText('Strict'))
    const row = await screen.findByTestId('qa-rule-placeables')
    expect(within(row).queryByTestId('qa-sub-checks-placeables')).toBeNull()

    await user.click(within(row).getByText('Warning'))
    await waitFor(() => expect(mockUpsert).toHaveBeenCalledTimes(1))
    expect(mockUpsert).toHaveBeenLastCalledWith(1, 'placeables', true, 'warning', { urls_emails: true, codes: true, variables: true })
    const subs = await within(row).findByTestId('qa-sub-checks-placeables')
    expect(within(subs).getAllByRole('checkbox').map(c => (c as HTMLInputElement).checked)).toEqual([true, true, true])

    await user.click(within(subs).getByLabelText(/Alphanumerics/))
    await waitFor(() => expect(mockUpsert).toHaveBeenCalledTimes(2))
    expect(mockUpsert).toHaveBeenLastCalledWith(1, 'placeables', true, 'warning', { urls_emails: true, codes: false, variables: true })
  })
})


describe('QA profile create form', () => {
  it('keeps Create disabled until the name has more than spaces, and says why', async () => {
    const user = userEvent.setup()
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MemoryRouter><QAProfiles /></MemoryRouter>
      </QueryClientProvider>,
    )
    await user.click(await screen.findByRole('button', { name: 'New QA profile' }))
    // Re-query each time: the button is remounted when its disabled reason comes or goes.
    const create = () => screen.getByRole('button', { name: 'Create' })
    await waitFor(() => expect(create()).toHaveAttribute('aria-disabled', 'true'))
    await user.hover(create())
    expect(await screen.findByText('Fill in the required fields first.')).toBeInTheDocument()

    const name = screen.getByPlaceholderText('e.g. Strict QA')
    await user.type(name, '   ')
    await waitFor(() => expect(create()).toHaveAttribute('aria-disabled', 'true'))
    await user.type(name, 'Medical')
    await waitFor(() => expect(create()).not.toHaveAttribute('aria-disabled'))
  })
})
