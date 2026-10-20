import { describe, expect, it } from 'vitest'
import { renderHook } from '@testing-library/react'
import { BrowserRouter, useLocation } from 'react-router-dom'
import type { ReactNode } from 'react'
import { useOpenFilesWhenEmpty } from './use-open-files-when-empty'

const wrapper = ({ children }: { children: ReactNode }) => <BrowserRouter>{children}</BrowserRouter>

function useHarness(noFiles: boolean) {
  const location = useLocation()
  const activeTab = location.pathname.endsWith('/settings') ? 'settings' : location.pathname.endsWith('/files') ? 'files' : 'tasks'
  useOpenFilesWhenEmpty({ pid: 7, noFiles, isDraft: false, activeTab, pathname: location.pathname, search: location.search })
  return location.pathname
}

describe('useOpenFilesWhenEmpty', () => {
  it('opens Files when a project without files lands on Tasks', () => {
    window.history.replaceState({}, '', '/projects/7')
    const { result, rerender } = renderHook(({ noFiles }) => useHarness(noFiles), { wrapper, initialProps: { noFiles: false } })
    expect(result.current).toBe('/projects/7')
    rerender({ noFiles: true })
    expect(window.location.pathname).toBe('/projects/7/files')
  })

  it('opens Files from any path the router reads as Tasks, a trailing slash included', () => {
    window.history.replaceState({}, '', '/projects/7/')
    const { rerender } = renderHook(({ noFiles }) => useHarness(noFiles), { wrapper, initialProps: { noFiles: false } })
    rerender({ noFiles: true })
    expect(window.location.pathname).toBe('/projects/7/files')
  })

  // A tab click moves the browser before the router renders it. A redirect
  // decided on the render before must not undo the click.
  it('leaves a tab the user moved to alone', () => {
    window.history.replaceState({}, '', '/projects/7')
    const { rerender } = renderHook(({ noFiles }) => useHarness(noFiles), { wrapper, initialProps: { noFiles: false } })
    window.history.pushState({}, '', '/projects/7/settings')
    rerender({ noFiles: true })
    expect(window.location.pathname).toBe('/projects/7/settings')
  })
})
