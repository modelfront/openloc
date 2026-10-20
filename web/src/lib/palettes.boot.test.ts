import { describe, it, expect, beforeEach } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { applyPalette, buildTokens, BUILTIN_PALETTES, TOKENS_KEY } from './palettes'

// The inline script in index.html colors the page before the app code loads.
// These tests run that script against what applyPalette leaves in storage.
const html = readFileSync(resolve(__dirname, '../../index.html'), 'utf8')
const bootScript = html.match(/<script>([\s\S]*?)<\/script>/)![1]
const runBootScript = () => new Function(bootScript)()

const mist = BUILTIN_PALETTES.find(p => p.id === 'mist')!
const blackTrue = BUILTIN_PALETTES.find(p => p.id === 'black-true')!

function resetHtml() {
  const el = document.documentElement
  el.removeAttribute('style')
  el.className = ''
}

describe('index.html boot script', () => {
  beforeEach(() => {
    localStorage.clear()
    resetHtml()
  })

  it('applies the palette last used in this browser before the app runs', () => {
    localStorage.setItem('openloc-theme', 'light')
    applyPalette(mist)
    resetHtml()

    runBootScript()

    const el = document.documentElement
    expect(el.classList.contains('light')).toBe(true)
    expect(el.style.getPropertyValue('--background')).toBe(buildTokens(mist)['--background'])
  })

  it('picks the tokens for the saved mode, not the last one applied', () => {
    applyPalette(mist)
    applyPalette(blackTrue)
    localStorage.setItem('openloc-theme', 'light')
    resetHtml()

    runBootScript()

    expect(document.documentElement.style.getPropertyValue('--background')).toBe(buildTokens(mist)['--background'])
  })

  it('sets the dark class even when no palette was saved yet', () => {
    localStorage.setItem('openloc-theme', 'dark')

    runBootScript()

    const el = document.documentElement
    expect(el.classList.contains('dark')).toBe(true)
    expect(el.style.getPropertyValue('--background')).toBe('')
  })

  it('survives a corrupt token cache', () => {
    localStorage.setItem('openloc-theme', 'dark')
    localStorage.setItem(TOKENS_KEY, '{not json')

    expect(runBootScript).not.toThrow()
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })
})
