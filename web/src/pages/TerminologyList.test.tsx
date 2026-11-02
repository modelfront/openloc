import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import TerminologyList from './TerminologyList'
import { listTermBases, getMe } from '../api'
import { renderAtRoute } from '../test/router'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
vi.mock('../api', async importOriginal => ({
  ...(await importOriginal<typeof import('../api')>()),
  listTermBases: vi.fn(),
  getMe: vi.fn(),
}))

type Bases = Awaited<ReturnType<typeof listTermBases>>
const base = (id: number, name: string, createdBy: number, createdByName: string) =>
  ({
    id, name, created_by: createdBy, created_by_name: createdByName, created_at: '2026-01-01T00:00:00Z',
    source_language: 'en', target_languages: ['de'], languages: ['en', 'de'], entry_count: 1,
  }) as unknown as Bases[number]

const BASES = [
  base(1, 'Automotive glossary', 7, 'Ana'),
  base(2, 'Legal terms', 7, 'Ana'),
  base(3, 'Legal terms FR', 8, 'Ben'),
]

const renderPage = (url: string) => renderAtRoute(<TerminologyList />, { url, path: '/terminology' })

const shownIds = () => screen.queryAllByTestId(/^term-base-row-/).map(el => el.getAttribute('data-testid'))

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(listTermBases).mockResolvedValue(BASES)
  vi.mocked(getMe).mockResolvedValue({ id: 7, role: 'owner' } as Awaited<ReturnType<typeof getMe>>)
})

describe('TerminologyList name search', () => {
  it('typing narrows the cards by name and writes ?q=', async () => {
    renderPage('/terminology')
    await screen.findByText('Automotive glossary')

    fireEvent.change(screen.getByTestId('term-base-list-search'), { target: { value: 'legal' } })

    expect(shownIds()).toEqual(['term-base-row-2', 'term-base-row-3'])
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('?q=legal'))
  })

  it('?q= and created_by in the starting URL both apply', async () => {
    renderPage('/terminology?q=legal&created_by=7')
    await screen.findByText('Legal terms')

    expect(shownIds()).toEqual(['term-base-row-2'])
  })

  it('a search with no match says so, and Clear filters brings every term base back', async () => {
    renderPage('/terminology?q=nothing&created_by=8')
    expect(await screen.findByText('No term bases match the current filter.')).toBeInTheDocument()
    expect(screen.getByTestId('term-base-filter-clear')).toHaveTextContent('Clear filters (2)')

    fireEvent.click(screen.getByTestId('term-base-filter-clear'))

    await waitFor(() => expect(shownIds()).toHaveLength(3))
    expect(screen.getByTestId('where')).toBeEmptyDOMElement()
  })
})
