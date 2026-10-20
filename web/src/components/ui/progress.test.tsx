import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { Progress } from './progress'

function parts(bar: HTMLElement) {
  return {
    track: bar.querySelector<HTMLElement>('[data-slot="progress-track"]')!,
    fill: bar.querySelector<HTMLElement>('[data-slot="progress-indicator"]')!,
  }
}

describe('Progress', () => {
  it('is a named progressbar with its value, min and max, and a fill that wide', () => {
    render(<Progress value={45} aria-label="Upload" />)
    const bar = screen.getByRole('progressbar', { name: 'Upload' })
    expect(bar).toHaveAttribute('aria-valuenow', '45')
    expect(bar).toHaveAttribute('aria-valuemin', '0')
    expect(bar).toHaveAttribute('aria-valuemax', '100')
    expect(parts(bar).fill.style.width).toBe('45%')
  })

  it('clamps a value outside 0–100 and reads a non-number as 0', () => {
    const { rerender } = render(<Progress value={140} aria-label="p" />)
    const bar = screen.getByRole('progressbar')
    expect(bar).toHaveAttribute('aria-valuenow', '100')
    expect(parts(bar).fill.style.width).toBe('100%')

    rerender(<Progress value={-5} aria-label="p" />)
    expect(bar).toHaveAttribute('aria-valuenow', '0')
    expect(parts(bar).fill.style.width).toBe('0%')

    rerender(<Progress value={NaN} aria-label="p" />)
    expect(bar).toHaveAttribute('aria-valuenow', '0')
  })

  it('null is indeterminate: no aria-valuenow, a full pulsing fill', () => {
    render(<Progress value={null} aria-label="Importing" />)
    const bar = screen.getByRole('progressbar', { name: 'Importing' })
    expect(bar).not.toHaveAttribute('aria-valuenow')
    const { fill } = parts(bar)
    expect(fill).toHaveAttribute('data-indeterminate')
    expect(fill.className).toContain('data-[indeterminate]:w-full')
    expect(fill.className).toContain('data-[indeterminate]:animate-pulse')
  })

  it('passes aria-labelledby, aria-valuetext, title and data-testid through', () => {
    render(
      <>
        <span id="lbl">Spell check</span>
        <Progress value={30} aria-labelledby="lbl" aria-valuetext="3 of 10 segments" title="30% done" data-testid="sc-bar" />
      </>,
    )
    const bar = screen.getByRole('progressbar', { name: 'Spell check' })
    expect(bar).toHaveAttribute('aria-valuetext', '3 of 10 segments')
    expect(bar).toHaveAttribute('title', '30% done')
    expect(bar).toBe(screen.getByTestId('sc-bar'))
  })

  it('is neutral by default and takes a chip state colour with tone', () => {
    const { rerender } = render(<Progress value={50} aria-label="p" />)
    const bar = screen.getByRole('progressbar')
    expect(parts(bar).track.className).toContain('bg-muted')
    expect(parts(bar).fill.className).toContain('bg-primary')

    rerender(<Progress value={50} tone="green" aria-label="p" />)
    expect(parts(bar).track.className).toContain('bg-emerald-100')
    expect(parts(bar).fill.className).toContain('bg-emerald-500')
    expect(parts(bar).fill.className).not.toContain('bg-primary')
  })

  it('sizes the track: sm is thinner than default', () => {
    const { rerender } = render(<Progress value={10} aria-label="p" />)
    const bar = screen.getByRole('progressbar')
    expect(parts(bar).track.className).toContain('h-2')
    rerender(<Progress value={10} size="sm" aria-label="p" />)
    expect(parts(bar).track.className).toContain('h-1.5')
    expect(parts(bar).track.className).not.toMatch(/\bh-2\b/)
  })
})
