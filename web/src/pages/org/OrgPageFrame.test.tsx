import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor, act } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../../api', async () => {
  const actual = await vi.importActual<typeof import('../../api')>('../../api')
  return { ...actual, getMe: vi.fn() }
})

import { getMe, type UserProfile } from '../../api'
import { OrgPageFrame } from './OrgPageFrame'

function renderFrame(role: string, url = '/org/vendors') {
  vi.mocked(getMe).mockResolvedValue({ id: 1, role } as UserProfile)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[url]}>
        <OrgPageFrame><p>tab body</p></OrgPageFrame>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('OrgPageFrame', () => {
  it.each(['owner', 'admin'])('%s sees the Members, Settings and Vendors tabs', async role => {
    renderFrame(role)
    const tabs = await screen.findByRole('navigation', { name: 'Section' })
    expect([...tabs.querySelectorAll('a')].map(a => [a.textContent, a.getAttribute('href')])).toEqual([
      ['Members', '/org'],
      ['Settings', '/org/settings'],
      ['Vendors', '/org/vendors'],
    ])
    expect(screen.getByRole('link', { name: 'Vendors' })).toHaveAttribute('aria-current', 'page')
  })

  it('a project manager gets the page with no tab row: Vendors is all they may open', async () => {
    renderFrame('project_manager')
    // Wait for the role to arrive: before it does, no frame shows tabs.
    await waitFor(() => expect(getMe).toHaveBeenCalled())
    await act(async () => { await Promise.resolve() })
    expect(screen.getByText('tab body')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Organization' })).toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: 'Section' })).not.toBeInTheDocument()
  })
})
