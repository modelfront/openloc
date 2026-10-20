import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { attentionHref, drawAttention } from './attention'

beforeEach(() => {
  vi.useFakeTimers()
  document.body.innerHTML = '<section id="card"><button id="pick">pick</button></section>'
})
afterEach(() => {
  vi.useRealTimers()
  document.body.innerHTML = ''
})

describe('drawAttention', () => {
  it('scrolls the target into view, focuses the control, and rings the target for a moment', () => {
    const card = document.getElementById('card')!
    const scroll = vi.spyOn(card, 'scrollIntoView')
    drawAttention({ target: '#card', focus: '#pick' })

    expect(scroll).toHaveBeenCalledWith({ behavior: 'smooth', block: 'center' })
    expect(document.activeElement).toBe(document.getElementById('pick'))
    expect(card.dataset.attention).toBe('')
    // The keyframes and the removal run on the same numbers.
    expect(card.style.getPropertyValue('--attention-pulses')).toBe('3')
    expect(card.style.getPropertyValue('--attention-pulse-ms')).toBe('700ms')

    vi.advanceTimersByTime(3 * 700 - 1)
    expect(card.dataset.attention).toBe('')
    vi.advanceTimersByTime(1)
    expect(card.dataset.attention).toBeUndefined()
  })

  it('waits for a target that is still rendering', () => {
    document.body.innerHTML = ''
    drawAttention({ target: '#late' })
    document.body.innerHTML = '<div id="late"></div>'
    vi.advanceTimersToNextFrame()
    expect(document.getElementById('late')!.dataset.attention).toBe('')
  })

  it('stops waiting once its frame budget is spent', () => {
    document.body.innerHTML = ''
    drawAttention({ target: '#late', framesLeft: 2 })
    vi.advanceTimersToNextFrame()
    vi.advanceTimersToNextFrame()
    vi.advanceTimersToNextFrame()
    // A target that shows up after the budget is not ringed: the retry ended.
    document.body.innerHTML = '<div id="late"></div>'
    vi.advanceTimersToNextFrame()
    vi.advanceTimersToNextFrame()
    expect(document.getElementById('late')!.dataset.attention).toBeUndefined()
  })

  it('a second call restarts the ring, and the first call\'s timer does not cut it short', () => {
    const card = document.getElementById('card')!
    drawAttention({ target: '#card' })
    vi.advanceTimersByTime(1500)
    drawAttention({ target: '#card' })
    vi.advanceTimersByTime(1000)
    // 2500 ms after the first call: its timer would have fired at 2100.
    expect(card.dataset.attention).toBe('')
    vi.advanceTimersByTime(1100)
    expect(card.dataset.attention).toBeUndefined()
  })
})

describe('attentionHref', () => {
  it('adds the attention key to a path, keeping any query it already has', () => {
    expect(attentionHref('/ai', 'ai-router-12')).toBe('/ai?attention=ai-router-12')
    expect(attentionHref('/dashboards?tab=mine', 'chart-3')).toBe('/dashboards?tab=mine&attention=chart-3')
  })
})
