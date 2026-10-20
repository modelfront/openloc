import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { formatPageTitle, usePageTitle } from './usePageTitle'

describe('formatPageTitle', () => {
  it('puts the page first and the app name last', () => {
    expect(formatPageTitle('brochure.docx', 'Acme launch')).toBe('brochure.docx · Acme launch · OpenLoc')
  })

  it('skips empty and missing parts', () => {
    expect(formatPageTitle(undefined, '  ', null, 'Projects')).toBe('Projects · OpenLoc')
    expect(formatPageTitle()).toBe('OpenLoc')
  })
})

describe('usePageTitle', () => {
  beforeEach(() => {
    document.title = 'OpenLoc'
  })

  it('sets the tab title once the name has loaded', () => {
    const { rerender } = renderHook(({ name }) => usePageTitle(name), {
      initialProps: { name: undefined as string | undefined },
    })
    expect(document.title).toBe('OpenLoc')
    rerender({ name: 'Acme launch' })
    expect(document.title).toBe('Acme launch · OpenLoc')
  })

  it('resets the tab title on unmount', () => {
    const { unmount } = renderHook(() => usePageTitle('Projects'))
    expect(document.title).toBe('Projects · OpenLoc')
    unmount()
    expect(document.title).toBe('OpenLoc')
  })

  it('leaves the title alone when it has no parts', () => {
    document.title = 'Projects · OpenLoc'
    const { unmount } = renderHook(() => usePageTitle(undefined))
    expect(document.title).toBe('Projects · OpenLoc')
    unmount()
    expect(document.title).toBe('Projects · OpenLoc')
  })
})
