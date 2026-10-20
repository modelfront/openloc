import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { Breadcrumbs } from './Breadcrumbs'

describe('Breadcrumbs', () => {
  it('renders parent links and marks the current entity without adding a heading', () => {
    render(
      <MemoryRouter>
        <Breadcrumbs items={[
          { label: 'Translation memories', to: '/memories' },
          { label: 'Default TM' },
        ]} />
      </MemoryRouter>,
    )

    const breadcrumb = screen.getByRole('navigation', { name: 'Breadcrumb' })
    expect(breadcrumb).toHaveTextContent('Translation memories/Default TM')
    expect(screen.getByRole('link', { name: 'Translation memories' })).toHaveAttribute('href', '/memories')
    expect(screen.getByText('Default TM')).toHaveAttribute('aria-current', 'page')
    expect(screen.queryByRole('heading')).not.toBeInTheDocument()
  })

  it('puts actions on the same line, outside the breadcrumb nav', () => {
    render(
      <MemoryRouter>
        <Breadcrumbs
          items={[{ label: 'Projects', to: '/projects' }, { label: 'guide.docx' }]}
          actions={<button type="button">Download</button>}
        />
      </MemoryRouter>,
    )

    const nav = screen.getByRole('navigation', { name: 'Breadcrumb' })
    const action = screen.getByRole('button', { name: 'Download' })
    expect(nav).not.toContainElement(action)
    expect(nav.parentElement).toContainElement(action)
  })
})
