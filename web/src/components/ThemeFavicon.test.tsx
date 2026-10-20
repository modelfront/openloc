import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from '@testing-library/react'
import { ThemeFavicon } from './ThemeFavicon'

const theme = vi.hoisted(() => ({ resolvedTheme: 'dark' as string | undefined }))
vi.mock('next-themes', () => ({ useTheme: () => theme }))

describe('ThemeFavicon', () => {
  beforeEach(() => {
    const icon = document.createElement('link')
    icon.rel = 'icon'
    icon.dataset.themeFavicon = ''
    icon.href = '/favicon.svg'
    document.head.appendChild(icon)
  })
  afterEach(() => document.querySelector('link[data-theme-favicon]')?.remove())

  it('switches the favicon with the app theme in both directions', () => {
    theme.resolvedTheme = 'dark'
    const { rerender } = render(<ThemeFavicon />)
    const icon = document.querySelector('link[data-theme-favicon]')!
    expect(icon.getAttribute('href')).toBe('/favicon-dark.svg')
    theme.resolvedTheme = 'light'
    rerender(<ThemeFavicon />)
    expect(icon.getAttribute('href')).toBe('/favicon.svg')
    theme.resolvedTheme = 'dark'
    rerender(<ThemeFavicon />)
    expect(icon.getAttribute('href')).toBe('/favicon-dark.svg')
  })
})
