// The legend under a full-size chart. recharts draws nothing at jsdom's 0×0, so
// these assert on the legend, which is plain HTML, and on what it tells the
// reader: shares, totals, and which entries are hidden.
import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ChartSpec } from '@/api'
import { SpecChart } from './SpecChart'

const pie: ChartSpec = {
  type: 'pie', x_key: 'target_language', series: ['words'], units: { words: 'words', segments: 'segments' },
  // Not in size order: the legend puts the biggest first.
  data: [
    { target_language: 'fr', words: 30_000 },
    { target_language: 'es', words: 10_000 },
    { target_language: 'de', words: 60_000 },
  ],
}

function entry(name: string) {
  return screen.getAllByTestId('chart-legend-item').find(b => within(b).queryByText(name, { exact: true }))!
}

describe('SpecChart legend', () => {
  it('names every slice, biggest first, with no share (the chart tooltip has it)', () => {
    render(<SpecChart spec={pie} />)
    expect(screen.getAllByTestId('chart-legend-item').map(b => b.textContent)).toEqual(['de', 'fr', 'es'])
  })

  // A click keeps the entry it is on and strikes the others; later clicks add
  // entries back, and taking out the last one picked shows everything again.
  it('keeps the clicked slice, adds more on later clicks, and resets from the last one', () => {
    render(<SpecChart spec={pie} />)
    const pressed = () => ['de', 'fr', 'es'].map(n => entry(n).getAttribute('aria-pressed'))
    fireEvent.click(entry('de'))
    expect(pressed()).toEqual(['true', 'false', 'false'])
    fireEvent.click(entry('es'))
    expect(pressed()).toEqual(['true', 'false', 'true'])
    fireEvent.click(entry('de'))
    expect(pressed()).toEqual(['false', 'false', 'true'])
    fireEvent.click(entry('es'))
    expect(pressed()).toEqual(['true', 'true', 'true'])
  })

  it('shows a slice\'s value in a tooltip, leaving the entry as it is', async () => {
    render(<SpecChart spec={pie} />)
    await userEvent.hover(entry('fr'))
    expect(entry('fr').textContent).toBe('fr')
    await waitFor(() => expect(screen.getByTestId('chart-legend-tooltip')).toHaveTextContent('30K words (30%)'))
  })

  it('orders a split chart\'s series biggest first and shows a total in the tooltip', async () => {
    const bars: ChartSpec = {
      type: 'bar', x_key: 'week', series: ['words'], series_key: 'target_language', units: { words: 'words' },
      data: [
        { week: '2026-10-05', target_language: 'de', words: 40_000 },
        { week: '2026-10-12', target_language: 'de', words: 15_000 },
        { week: '2026-10-12', target_language: 'fr', words: 2_000 },
        { week: '2026-10-12', target_language: 'es', words: 9_000 },
      ],
    }
    render(<SpecChart spec={bars} />)
    expect(screen.getAllByTestId('chart-legend-item').map(b => b.textContent)).toEqual(['de', 'es', 'fr'])
    await userEvent.hover(entry('de'))
    expect(entry('de').textContent).toBe('de')
    await waitFor(() => expect(screen.getByTestId('chart-legend-tooltip')).toHaveTextContent('Total: 55K words (83%)'))
  })

  it('shows no total for averages, which do not add up, and ranks them by their mean', async () => {
    const effort: ChartSpec = {
      type: 'bar', x_key: 'week', series: ['avg_edit_distance'], series_key: 'user', averages: ['avg_edit_distance'],
      data: [
        { week: '2026-10-05', user: 'Ann', avg_edit_distance: 3 },
        { week: '2026-10-12', user: 'Ann', avg_edit_distance: 5 },
        { week: '2026-10-12', user: 'Bo', avg_edit_distance: 6 },
      ],
    }
    render(<SpecChart spec={effort} />)
    // Ann's weeks add up to 8, but her mean (4) is below Bo's 6.
    expect(screen.getAllByTestId('chart-legend-item').map(b => b.textContent)).toEqual(['Bo', 'Ann'])
    // No tooltip at all: the entry is a plain button, not a tooltip trigger.
    expect(entry('Ann')).toHaveAttribute('data-slot', 'button')
  })

  it('makes an entry with a number a tooltip trigger', () => {
    render(<SpecChart spec={pie} />)
    expect(entry('fr')).toHaveAttribute('data-slot', 'tooltip-trigger')
  })

  it('names the one band of a split, an unstacked average split too', () => {
    const effort: ChartSpec = {
      type: 'bar', x_key: 'week', series: ['avg_edit_distance'], series_key: 'user', averages: ['avg_edit_distance'],
      data: [{ week: '2026-10-05', user: 'Ann', avg_edit_distance: 3 }],
    }
    render(<SpecChart spec={effort} />)
    expect(screen.getAllByTestId('chart-legend-item').map(b => b.textContent)).toEqual(['Ann'])
  })

  it('gives no share when the series\' units are unknown, as on an assistant chart', async () => {
    const bars: ChartSpec = {
      type: 'bar', x_key: 'engine', series: ['words', 'segments'],
      data: [{ engine: 'A', words: 400, segments: 40 }],
    }
    render(<SpecChart spec={bars} />)
    await userEvent.hover(entry('words'))
    await waitFor(() => expect(screen.getByTestId('chart-legend-tooltip')).toHaveTextContent('Total: 400'))
    expect(screen.getByTestId('chart-legend-tooltip').textContent).not.toContain('%')
  })

  it('gives no share when a column that sums others is shown beside them', async () => {
    const tokens: ChartSpec = {
      type: 'bar', x_key: 'engine', series: ['input_tokens', 'output_tokens', 'total_tokens'],
      units: { input_tokens: 'tokens', output_tokens: 'tokens', total_tokens: 'tokens' }, derived: ['total_tokens'],
      data: [{ engine: 'A', input_tokens: 600, output_tokens: 400, total_tokens: 1000 }],
    }
    render(<SpecChart spec={tokens} />)
    await userEvent.hover(entry('input_tokens'))
    await waitFor(() => expect(screen.getByTestId('chart-legend-tooltip')).toHaveTextContent('Total: 600 tokens'))
    expect(screen.getByTestId('chart-legend-tooltip').textContent).not.toContain('%')
  })

  it('caps a pie at the biggest slices and Other', () => {
    const many: ChartSpec = {
      type: 'pie', x_key: 'project', series: ['words'], units: { words: 'words' },
      data: Array.from({ length: 12 }, (_, i) => ({ project: `p${i}`, words: 100 - i })),
    }
    render(<SpecChart spec={many} />)
    const names = screen.getAllByTestId('chart-legend-item').map(b => b.textContent)
    expect(names).toHaveLength(8)
    expect(names[7]).toBe('Other')
  })

  it('draws no legend for a single value column, or for a preview', () => {
    const line: ChartSpec = { type: 'line', x_key: 'week', series: ['words'], data: [{ week: '2026-10-05', words: 1 }] }
    const { unmount } = render(<SpecChart spec={line} />)
    expect(screen.queryByTestId('chart-legend')).toBeNull()
    unmount()
    render(<SpecChart spec={pie} compact />)
    expect(screen.queryByTestId('chart-legend')).toBeNull()
  })
})
