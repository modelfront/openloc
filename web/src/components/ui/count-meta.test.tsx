import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { CountMeta } from './count-meta'

describe('CountMeta', () => {
  it('thousands-separates counts', () => {
    const { container } = render(<CountMeta items={[{ count: 1145, noun: 'word' }]} />)
    expect(container.textContent).toBe('1,145 words')
  })

  it('joins multiple units with a middot', () => {
    const { container } = render(
      <CountMeta items={[{ count: 31, noun: 'segment' }, { count: 145, noun: 'word' }]} />,
    )
    expect(container.textContent).toBe('31 segments·145 words')
  })

  it('uses the singular noun when the count is exactly 1', () => {
    const { container } = render(<CountMeta items={[{ count: 1, noun: 'segment' }]} />)
    expect(container.textContent).toBe('1 segment')
  })

  it('honors an explicit plural override', () => {
    const { container } = render(<CountMeta items={[{ count: 2, noun: 'match', plural: 'matches' }]} />)
    expect(container.textContent).toBe('2 matches')
  })

  it('applies tabular-nums and merges an extra className', () => {
    const { container } = render(<CountMeta items={[{ count: 3, noun: 'char' }]} className="text-xs" />)
    const el = container.firstChild as HTMLElement
    expect(el.className).toContain('tabular-nums')
    expect(el.className).toContain('text-xs')
  })
})
