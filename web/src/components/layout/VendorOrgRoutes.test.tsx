import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'

// Each page is a stub naming itself: these tests are about which page an
// address reaches.
vi.mock('@/pages/ProjectList', () => ({ default: () => <p>Projects page</p> }))
vi.mock('@/pages/TasksPage', () => ({ default: () => <p>Tasks page</p> }))
vi.mock('@/pages/SettingsPage', () => ({ default: () => <p>Settings page</p> }))
vi.mock('@/pages/org/OrgMembersPage', () => ({ default: () => <p>Members page</p> }))
vi.mock('@/pages/issues/IssuesPage', () => ({ default: () => <p>Issues page</p> }))
vi.mock('@/pages/issues/IssueDetailPage', () => ({ default: () => <p>Issue page</p> }))

import { VendorOrgRoutes } from './VendorOrgRoutes'

function Where() {
  return <span data-testid="path">{useLocation().pathname}</span>
}

function renderAt(path: string, role: string) {
  render(
    <MemoryRouter initialEntries={[path]}>
      <VendorOrgRoutes role={role} />
      <Where />
    </MemoryRouter>,
  )
}

describe('VendorOrgRoutes', () => {
  it.each([
    ['admin', '/org', '/org', 'Members page'],
    ['admin', '/projects', '/projects', 'Projects page'],
    ['admin', '/memories', '/projects', 'Projects page'],
    ['admin', '/org/settings', '/projects', 'Projects page'],
    ['admin', '/ai-settings', '/projects', 'Projects page'],
    ['admin', '/projects/new', '/projects', 'Projects page'],
    ['project_manager', '/org', '/projects', 'Projects page'],
    ['project_manager', '/tasks', '/tasks', 'Tasks page'],
    ['linguist', '/', '/tasks', 'Tasks page'],
    ['linguist', '/projects', '/tasks', 'Tasks page'],
    ['linguist', '/org', '/tasks', 'Tasks page'],
    ['linguist', '/settings', '/settings', 'Settings page'],
    ['linguist', '/issues', '/issues', 'Issues page'],
  ])('sends a %s opening %s to %s', (role, from, to, page) => {
    renderAt(from, role)
    expect(screen.getByTestId('path')).toHaveTextContent(new RegExp(`^${to}$`))
    expect(screen.getByText(page)).toBeInTheDocument()
  })
})
