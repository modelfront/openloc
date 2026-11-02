import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, screen, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import TMList from './TMList'
import { listTMCollections, listTMProfiles, getMe } from '../api'
import { renderAtRoute } from '../test/router'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('../api', async importOriginal => ({
  ...(await importOriginal<typeof import('../api')>()),
  listTMCollections: vi.fn(),
  listTMProfiles: vi.fn(),
  getMe: vi.fn(),
}))

type TMs = Awaited<ReturnType<typeof listTMCollections>>
const tm = (id: number, name: string, createdBy: number, createdByName: string) =>
  ({ id, name, created_by: createdBy, created_by_name: createdByName, created_at: '2026-01-01T00:00:00Z' }) as unknown as TMs[number]

const TMS = [
  tm(1, 'Médical EN-DE', 7, 'Ana'),
  tm(2, 'Legal EN-DE', 7, 'Ana'),
  tm(3, 'Legal EN-FR', 8, 'Ben'),
]

const renderPage = (url: string) => renderAtRoute(<TMList />, { url, path: '/memories' })

const shownIds = () => screen.queryAllByTestId(/^tm-row-/).map(el => el.getAttribute('data-testid'))

afterEach(() => { vi.useRealTimers() })

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(listTMCollections).mockResolvedValue(TMS)
  vi.mocked(getMe).mockResolvedValue({ id: 7, role: 'owner' } as Awaited<ReturnType<typeof getMe>>)
})

describe('TMList name search', () => {
  it('typing narrows the cards by name, ignoring case and accents, and writes ?q=', async () => {
    renderPage('/memories')
    await screen.findByText('Legal EN-FR')

    fireEvent.change(screen.getByTestId('tm-list-search'), { target: { value: 'MEDICAL' } })

    expect(shownIds()).toEqual(['tm-row-1'])
    expect(screen.getByTestId('tm-filter-clear')).toHaveTextContent('Clear filters (1)')
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('?q=MEDICAL'))
  })

  it('typing in the middle of the search keeps the cursor where it is', async () => {
    const user = userEvent.setup()
    renderPage('/memories?q=lgal')
    const input = await screen.findByTestId('tm-list-search') as HTMLInputElement

    await user.type(input, 'e', { initialSelectionStart: 1, initialSelectionEnd: 1 })

    expect(input).toHaveValue('legal')
    expect(input.selectionStart).toBe(2)
  })

  it('typing fast keeps every key', async () => {
    const user = userEvent.setup()
    renderPage('/memories')
    const input = await screen.findByTestId('tm-list-search')

    await user.type(input, 'legal en-fr')

    expect(input).toHaveValue('legal en-fr')
    expect(shownIds()).toEqual(['tm-row-3'])
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('?q=legal+en-fr'))
  })

  it('?q= and created_by in the starting URL both apply', async () => {
    renderPage('/memories?q=legal&created_by=7')
    await screen.findByText('Legal EN-DE')

    expect(screen.getByTestId('tm-list-search')).toHaveValue('legal')
    expect(shownIds()).toEqual(['tm-row-2'])
  })

  it('a search with no match says so, and Clear filters brings every TM back', async () => {
    renderPage('/memories?q=nothing&created_by=8')
    expect(await screen.findByText('No translation memories match the current filter.')).toBeInTheDocument()
    expect(screen.getByTestId('tm-filter-clear')).toHaveTextContent('Clear filters (2)')

    fireEvent.click(screen.getByTestId('tm-filter-clear'))

    await waitFor(() => expect(shownIds()).toHaveLength(3))
    expect(screen.getByTestId('where')).toBeEmptyDOMElement()
    expect(screen.getByTestId('tm-list-search')).toHaveValue('')
  })

  it('Clear filters empties a search that has not reached the URL yet', async () => {
    renderPage('/memories')
    await screen.findByText('Legal EN-FR')
    vi.useFakeTimers()

    fireEvent.change(screen.getByTestId('tm-list-search'), { target: { value: 'legal' } })
    fireEvent.click(screen.getByTestId('tm-filter-clear'))
    act(() => { vi.advanceTimersByTime(300) })

    expect(screen.getByTestId('tm-list-search')).toHaveValue('')
    expect(shownIds()).toHaveLength(3)
    expect(screen.getByTestId('where')).toBeEmptyDOMElement()
  })

  it('the TM profiles tab reads and writes the same ?q=', async () => {
    vi.mocked(listTMProfiles).mockResolvedValue([
      { id: 1, name: 'Legal default', description: '', tm_count: 1, fuzzy_threshold: 0.75, usage_count: 0 },
    ] as Awaited<ReturnType<typeof listTMProfiles>>)
    renderAtRoute(<TMList />, { url: '/memories/profiles?q=nothing', path: '/memories/profiles' })

    expect(await screen.findByText('No TM profiles match the current filter.')).toBeInTheDocument()
    fireEvent.change(screen.getByTestId('tm-profile-list-search'), { target: { value: 'legal' } })

    expect(await screen.findByTestId('tm-profile-row-1')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('?q=legal'))
  })

  it('an account with no TMs shows only the empty state, without a search box', async () => {
    vi.mocked(listTMCollections).mockResolvedValue([])
    renderPage('/memories')

    expect(await screen.findByText(/No translation memories yet/)).toBeInTheDocument()
    expect(screen.queryByTestId('tm-list-search')).not.toBeInTheDocument()
  })
})
