import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { NavRail, navItemsForRole } from './NavRail'

// The role switcher fetches the dev role list, which is not what these tests
// are about.
vi.mock('../RoleSwitcher', () => ({ RoleSwitcher: () => <div>Role switcher</div> }))

const EDITOR_PATH = '/projects/7/jobs/42/edit'

function renderRail(path: string, role = 'admin', isPlatformAdmin = false) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <NavRail role={role} isPlatformAdmin={isPlatformAdmin} />
    </MemoryRouter>,
  )
}

describe('navItemsForRole', () => {
  it('gives a linguist only the Tasks tab', () => {
    expect(navItemsForRole('linguist', false).map(i => i.label)).toEqual(['Tasks'])
  })

  it('appends the operator console only for a platform admin', () => {
    expect(navItemsForRole('admin', false).map(i => i.label)).not.toContain('Organizations')
    expect(navItemsForRole('admin', true).map(i => i.label)).toContain('Organizations')
  })
})

describe('NavRail', () => {
  beforeEach(() => localStorage.clear())

  it.each(['/projects', EDITOR_PATH])('collapses to an icon strip on %s, keeping every link', async path => {
    renderRail(path)
    const labelCount = navItemsForRole('admin', false).length
    expect(screen.getByTestId('nav-rail')).toHaveAttribute('data-collapsed', 'false')
    // Every nav row plus the wordmark.
    expect(screen.getAllByRole('link')).toHaveLength(labelCount + 1)

    await userEvent.click(screen.getByRole('button', { name: 'Collapse navigation' }))

    expect(screen.getByTestId('nav-rail')).toHaveAttribute('data-collapsed', 'true')
    // The visible labels are gone, but every route stays reachable AND named —
    // a collapsed row carries its label as `aria-label`.
    expect(screen.queryByText('Projects')).toBeNull()
    expect(screen.getAllByRole('link')).toHaveLength(labelCount)
    expect(screen.getByRole('link', { name: 'Projects' })).toHaveAttribute('href', '/projects')
    expect(localStorage.getItem('nav-rail-collapsed')).toBe('true')
  })

  it('reopens from the collapsed strip', async () => {
    localStorage.setItem('nav-rail-collapsed', 'true')
    renderRail('/projects')
    expect(screen.getByTestId('nav-rail')).toHaveAttribute('data-collapsed', 'true')

    await userEvent.click(screen.getByRole('button', { name: 'Expand navigation' }))
    expect(screen.getByTestId('nav-rail')).toHaveAttribute('data-collapsed', 'false')
    expect(screen.getByText('Projects')).toBeInTheDocument()
    expect(localStorage.getItem('nav-rail-collapsed')).toBe('false')
  })

  it('links the wordmark to the home page', () => {
    renderRail(EDITOR_PATH)
    expect(screen.getByRole('link', { name: 'OpenLoc' })).toHaveAttribute('href', '/')
  })

  it('keeps the wordmark and the toggle in one row, and shows only the toggle when collapsed', async () => {
    renderRail('/projects')
    const toggle = screen.getByTestId('nav-rail-toggle')
    const wordmark = screen.getByTestId('nav-rail-wordmark')
    expect(wordmark).toHaveTextContent('OpenLoc')
    expect(toggle.parentElement).toBe(wordmark.parentElement)

    await userEvent.click(toggle)
    expect(screen.queryByText('OpenLoc')).toBeNull()
    expect(screen.queryByRole('img', { name: 'OpenLoc' })).toBeNull()
    // The brand row now holds the expand control and nothing else.
    expect(screen.getByTestId('nav-rail-toggle').parentElement!.children).toHaveLength(1)
  })

  it('moves a collapsed row label into its tooltip', async () => {
    localStorage.setItem('nav-rail-collapsed', 'true')
    renderRail(EDITOR_PATH)
    const projects = screen.getByRole('link', { name: 'Projects' })
    // Nothing renders the label as text while collapsed…
    expect(screen.queryByText('Projects')).toBeNull()

    await userEvent.hover(projects)
    // …so the tooltip is what shows it to a sighted user. Finding it also
    // proves the Tooltip `render=` merge left the link intact.
    expect(await screen.findByText('Projects')).toBeInTheDocument()
    expect(projects).toHaveAttribute('href', '/projects')
  })

  it('marks the active route', () => {
    renderRail('/terminology')
    expect(screen.getByRole('link', { name: 'Terminology' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: 'Projects' })).not.toHaveAttribute('aria-current')
  })
})

describe('NavRail on a phone-width screen', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: query === '(max-width: 767px)', media: query,
      addEventListener: () => {}, removeEventListener: () => {}, addListener: () => {}, removeListener: () => {},
    }))
  })
  afterEach(() => vi.unstubAllGlobals())

  it('opens collapsed even when the saved choice is expanded, and never saves', async () => {
    localStorage.setItem('nav-rail-collapsed', 'false')
    renderRail('/projects')
    expect(screen.getByTestId('nav-rail')).toHaveAttribute('data-collapsed', 'true')

    await userEvent.click(screen.getByRole('button', { name: 'Expand navigation' }))
    expect(screen.getByTestId('nav-rail')).toHaveAttribute('data-collapsed', 'false')
    expect(localStorage.getItem('nav-rail-collapsed')).toBe('false')
  })

  it('collapses again after a page is picked', async () => {
    renderRail('/projects')
    await userEvent.click(screen.getByRole('button', { name: 'Expand navigation' }))
    await userEvent.click(screen.getByText('Terminology'))
    expect(screen.getByTestId('nav-rail')).toHaveAttribute('data-collapsed', 'true')
  })

  it('lies over the page when expanded, and a tap on the backdrop collapses it', async () => {
    renderRail('/projects')
    expect(screen.queryByTestId('nav-rail-backdrop')).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Expand navigation' }))
    expect(screen.getByTestId('nav-rail')).toHaveClass('fixed')

    await userEvent.click(screen.getByTestId('nav-rail-backdrop'))
    expect(screen.getByTestId('nav-rail')).toHaveAttribute('data-collapsed', 'true')
    expect(screen.queryByTestId('nav-rail-backdrop')).toBeNull()
  })
})
