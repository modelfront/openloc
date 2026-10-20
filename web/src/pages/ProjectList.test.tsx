// A failed project list must say so: "No projects yet" for a server error
// reads as "your projects are gone".
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  return {
    ...actual,
    listProjectsFilteredPage: vi.fn(),
    getMe: vi.fn().mockResolvedValue({ id: 1, role: 'owner' }),
    listLabels: vi.fn().mockResolvedValue([]),
    listLanguages: vi.fn().mockResolvedValue([]),
    listProjectCreators: vi.fn().mockResolvedValue([]),
  }
})

import { listProjectsFilteredPage, type Project } from '../api'
import ProjectList from './ProjectList'

const listMock = vi.mocked(listProjectsFilteredPage)

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/projects']}>
        <ProjectList />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('ProjectList when the list fails to load', () => {
  it('shows the error with a Retry, not "No projects yet"', async () => {
    listMock
      .mockRejectedValueOnce(new Error('server unavailable'))
      .mockResolvedValueOnce({
        data: [{ id: 5, name: 'Brochure', status: 'in_progress', source_language: 'en', target_languages: ['de'] } as Project],
        cursor: 0,
        has_more: false,
      })

    renderPage()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load the projects: server unavailable")
    expect(screen.queryByText(/No projects yet/)).not.toBeInTheDocument()

    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByTestId('project-row-5')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})

describe('ProjectList row actions', () => {
  it('keeps Duplicate and Delete in their own column, apart from the row text', async () => {
    listMock.mockResolvedValueOnce({
      data: [
        { id: 5, name: 'Brochure', status: 'in_progress', source_language: 'en', target_languages: ['de'] } as Project,
        { id: 6, name: 'Leaflet', status: 'draft', source_language: 'en', target_languages: ['fr'] } as Project,
      ],
      cursor: 0,
      has_more: false,
    })

    renderPage()

    const actions = await screen.findByTestId('project-actions-5')
    expect(within(actions).getAllByRole('button').map(b => b.getAttribute('aria-label')))
      .toEqual(['Duplicate', 'Delete'])
    // Nothing but the buttons: the dates and metadata live in the text column.
    expect(actions).toHaveTextContent(/^$/)
    expect(actions.previousElementSibling).toHaveTextContent('Brochure')
    expect(actions.previousElementSibling).toHaveTextContent('Created by:')

    // A draft can't be duplicated: an empty slot takes Duplicate's place, so
    // Delete stays in the same position as on the row above.
    const draftActions = screen.getByTestId('project-actions-6')
    expect(within(draftActions).getAllByRole('button').map(b => b.getAttribute('aria-label')))
      .toEqual(['Delete'])
    expect(draftActions.children).toHaveLength(actions.children.length)
    expect(draftActions.children[0]).toHaveAttribute('aria-hidden')
    expect(draftActions.children[0].querySelector('button')).toBeNull()
  })
})
