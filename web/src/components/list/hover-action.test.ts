import { describe, it, expect } from 'vitest'
import { hoverActionClass } from './hover-action'

describe('hoverActionClass', () => {
  it('includes the shared muted, always-visible classes for every intent', () => {
    const shared = ['transition-colors', 'text-gray-400', 'dark:text-gray-500']
    for (const intent of ['muted', 'danger', 'accent'] as const) {
      const cls = hoverActionClass(intent)
      for (const token of shared) {
        expect(cls).toContain(token)
      }
      // Pins always-visible muted styling — no hover-reveal.
      expect(cls).not.toContain('opacity-0')
      expect(cls).not.toContain('group-hover:opacity-100')
    }
  })

  it('uses neutral gray hover for the "muted" intent (rename/edit buttons)', () => {
    const cls = hoverActionClass('muted')
    expect(cls).toContain('hover:text-gray-600')
    expect(cls).toContain('dark:hover:text-gray-300')
    // Must NOT pick up a danger color
    expect(cls).not.toContain('hover:text-red-600')
  })

  it('uses red hover for the "danger" intent (delete buttons)', () => {
    const cls = hoverActionClass('danger')
    expect(cls).toContain('hover:text-red-600')
    expect(cls).toContain('dark:hover:text-red-400')
  })

  it('uses default (blue) hover for the "accent" intent (inline edit)', () => {
    const cls = hoverActionClass('accent')
    expect(cls).toContain('hover:text-blue-600')
    expect(cls).toContain('dark:hover:text-blue-400')
  })

  it('defaults to "muted" when no intent is passed', () => {
    expect(hoverActionClass()).toBe(hoverActionClass('muted'))
  })

  it('merges caller-supplied extra classes for one-off overrides', () => {
    const cls = hoverActionClass('danger', 'ml-2')
    expect(cls).toContain('ml-2')
    expect(cls).toContain('hover:text-red-600')
  })
})
