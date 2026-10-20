import { describe, expect, it } from 'vitest'
import { formatCount, formatDurationMs, formatShare } from './duration'

// This formatter exists alongside components/debug/format.ts's formatDuration
// on purpose — that one caps at seconds by contract (61500 → '61.5s') because
// it labels LLM request latencies. Job stages run for minutes, so this one
// carries through. The 61500 case below is the deliberate divergence.
describe('formatDurationMs', () => {
  it('shows milliseconds under a second', () => {
    expect(formatDurationMs(0)).toBe('0ms')
    expect(formatDurationMs(618)).toBe('618ms')
    expect(formatDurationMs(999)).toBe('999ms')
  })

  it('switches to seconds from 1000ms', () => {
    expect(formatDurationMs(1000)).toBe('1.0s')
    expect(formatDurationMs(2569)).toBe('2.6s')
    expect(formatDurationMs(59_999)).toBe('60.0s')
  })

  it('switches to minutes from 60s — the divergence from the debug formatter', () => {
    expect(formatDurationMs(60_000)).toBe('1.0m')
    expect(formatDurationMs(61_500)).toBe('1.0m')
    expect(formatDurationMs(252_000)).toBe('4.2m')
  })

  it('switches to hours from 60m', () => {
    expect(formatDurationMs(3_600_000)).toBe('1.0h')
    expect(formatDurationMs(4_680_000)).toBe('1.3h')
  })

  it('renders a dash for nonsense rather than NaN', () => {
    expect(formatDurationMs(NaN)).toBe('—')
    expect(formatDurationMs(-5)).toBe('—')
  })
})

describe('formatShare', () => {
  it('rounds to whole percent', () => {
    expect(formatShare(0.9091)).toBe('91%')
    expect(formatShare(1)).toBe('100%')
  })

  it('floors a sub-1% share to <1%, never 0%', () => {
    // A stage that ran must not read as free — 0% would say "didn't happen".
    expect(formatShare(0.004)).toBe('<1%')
  })

  it('reports a genuine zero as 0%', () => {
    expect(formatShare(0)).toBe('0%')
  })
})

describe('formatCount', () => {
  it('groups thousands', () => {
    expect(formatCount(1234)).toBe('1,234')
    expect(formatCount(7)).toBe('7')
  })
})
