import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { PaletteProvider } from './PaletteProvider'
import { buildTokens, BUILTIN_PALETTES, cacheAppearance } from '../lib/palettes'

vi.mock('next-themes', () => ({ useTheme: () => ({ resolvedTheme: 'light' }) }))
// The server never answers, as on a slow connection.
vi.mock('../api/appearance', () => ({ getAppearance: () => new Promise(() => {}) }))

const mist = BUILTIN_PALETTES.find(p => p.id === 'mist')!

describe('PaletteProvider', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.removeAttribute('style')
  })

  it('keeps the colors saved in this browser while the appearance loads', () => {
    cacheAppearance({ light_palette: 'mist', dark_palette: 'dim', custom_palettes: [] })
    render(
      <QueryClientProvider client={new QueryClient()}>
        <PaletteProvider />
      </QueryClientProvider>,
    )
    expect(document.documentElement.style.getPropertyValue('--background')).toBe(buildTokens(mist)['--background'])
  })
})
