import { describe, it, expect, vi } from 'vitest'
import { act, render } from '@testing-library/react'
import { endReveal, revealAnchor, useGuideReveal } from './guide-reveal'

function Section({ onReveal }: { onReveal: () => void }) {
  useGuideReveal('ai_prompt.schema', onReveal)
  return null
}

describe('useGuideReveal', () => {
  it('opens a section that mounts after its step started', () => {
    revealAnchor('ai_prompt.schema')
    const onReveal = vi.fn()
    render(<Section onReveal={onReveal} />)
    expect(onReveal).toHaveBeenCalledTimes(1)
    endReveal()
  })

  it('leaves it closed once the tour is over or on another step', () => {
    revealAnchor('ai_prompt.schema')
    endReveal()
    const onReveal = vi.fn()
    render(<Section onReveal={onReveal} />)
    revealAnchor('ai_prompt.context')
    expect(onReveal).not.toHaveBeenCalled()
    endReveal()
  })

  it('tells the section when the tour moves on or ends', () => {
    const onReveal = vi.fn()
    const onLeave = vi.fn()
    function Builder() {
      useGuideReveal(['chart_builder.metrics', 'chart_builder.options'], onReveal, onLeave)
      return null
    }
    render(<Builder />)
    act(() => revealAnchor('chart_builder.metrics'))
    act(() => revealAnchor('chart_builder.options'))
    expect(onReveal).toHaveBeenCalledTimes(2)
    expect(onLeave).not.toHaveBeenCalled()
    act(() => revealAnchor('dashboard_chart.chart'))
    expect(onLeave).toHaveBeenCalledTimes(1)
    // Not revealed any more: a later step elsewhere says nothing.
    act(() => revealAnchor('docs.content'))
    expect(onLeave).toHaveBeenCalledTimes(1)

    act(() => revealAnchor('chart_builder.metrics'))
    act(() => endReveal())
    expect(onLeave).toHaveBeenCalledTimes(2)
  })
})
