import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { SectionPageFrame } from './SectionPageFrame'

const tabs = [
  { to: '/mt', label: 'Routers', end: true },
  { to: '/mt/engines', label: 'Engines' },
]

describe('SectionPageFrame', () => {
  it('renders the title, the actions, one tab per route and the current tab', () => {
    render(
      <MemoryRouter initialEntries={['/mt/engines']}>
        <SectionPageFrame title="MT" actions={<button>New</button>} tabs={tabs} dataGuide="mt.tabs">
          <p>Engines list</p>
        </SectionPageFrame>
      </MemoryRouter>,
    )
    expect(screen.getByRole('heading', { name: 'MT' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'New' })).toBeInTheDocument()
    expect(screen.getByRole('navigation', { name: 'Section' })).toHaveAttribute('data-guide', 'mt.tabs')
    expect(screen.getByRole('link', { name: 'Engines' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: 'Routers' })).not.toHaveAttribute('aria-current')
    expect(screen.getByText('Engines list')).toBeInTheDocument()
  })

  it('has no tab row without tabs', () => {
    render(
      <MemoryRouter>
        <SectionPageFrame title="Organization"><p>Vendors</p></SectionPageFrame>
      </MemoryRouter>,
    )
    expect(screen.queryByRole('navigation', { name: 'Section' })).not.toBeInTheDocument()
    expect(screen.getByText('Vendors')).toBeInTheDocument()
  })
})
