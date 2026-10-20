import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ChartTooltip } from './ChartTooltip'

const rows = [
  { name: 'mt', value: 55_000, color: '#6366f1' },
  { name: 'tm', value: 1, color: '#10b981' },
  { name: 'ape', value: null, color: '#f59e0b' },
]
const words = () => 'words'

describe('ChartTooltip', () => {
  it('prints each value with its unit, one in the singular, and skips a gap', () => {
    render(<ChartTooltip active payload={rows} label="2026-10-12" highlighted={null} unitOf={words} additive={false} />)
    expect(screen.getAllByTestId('chart-tooltip-row').map(r => r.textContent)).toEqual(['mt55K words', 'tm1 word'])
  })

  it('sets the highlighted row in bold and only that one', () => {
    render(<ChartTooltip active payload={rows} label="x" highlighted="tm" unitOf={words} additive={false} />)
    const [mt, tm] = screen.getAllByTestId('chart-tooltip-row')
    expect(tm).toHaveAttribute('data-highlighted', 'true')
    expect(tm.className).toContain('font-semibold')
    expect(mt).not.toHaveAttribute('data-highlighted')
    expect(mt.className).not.toContain('font-semibold')
  })

  it('formats the heading and shows the bare number when the unit is unknown', () => {
    render(<ChartTooltip active payload={[rows[0]]} label="2026-10-12" formatLabel={v => `Week of ${v}`} highlighted={null} unitOf={() => undefined} additive={false} />)
    expect(screen.getByText('Week of 2026-10-12')).toBeInTheDocument()
    expect(screen.getByTestId('chart-tooltip-row').textContent).toBe('mt55K')
  })

  it('does not repeat a unit the row is already named after', () => {
    render(<ChartTooltip active payload={[{ name: 'words', value: 400 }]} label="Engine A" highlighted={null} unitOf={words} additive={false} />)
    expect(screen.getByTestId('chart-tooltip-row').textContent).toBe('words400')
  })

  it('ends with the total of the rows when they add up and share a unit', () => {
    render(<ChartTooltip active payload={rows} label="x" highlighted={null} unitOf={words} additive />)
    expect(screen.getByTestId('chart-tooltip-total').textContent).toBe('Total55K words')
  })

  it('shows no total for averages, rows of different units, or a single row', () => {
    const { unmount } = render(<ChartTooltip active payload={rows} label="x" highlighted={null} unitOf={words} additive={false} />)
    expect(screen.queryByTestId('chart-tooltip-total')).toBeNull()
    unmount()
    const mixed = [{ name: 'segments', value: 40 }, { name: 'words', value: 400 }]
    const { unmount: u2 } = render(<ChartTooltip active payload={mixed} label="x" highlighted={null} unitOf={n => n} additive />)
    expect(screen.queryByTestId('chart-tooltip-total')).toBeNull()
    u2()
    render(<ChartTooltip active payload={[rows[0]]} label="x" highlighted={null} unitOf={words} additive />)
    expect(screen.queryByTestId('chart-tooltip-total')).toBeNull()
  })

  it('gives a pie three columns, name, value and share, with the total under the values', () => {
    const slices = [
      { name: 'error', value: 60, share: 0.6 },
      { name: 'warning', value: 30, share: 0.3 },
      { name: 'info', value: 10, share: 0.1 },
    ]
    render(<ChartTooltip active payload={slices} highlighted="warning" unitOf={() => 'issues'} additive />)
    const cells = (tr: HTMLElement) => [...tr.querySelectorAll('td')].map(td => td.textContent)
    const rows = screen.getAllByTestId('chart-tooltip-row')
    expect(rows.map(cells)).toEqual([['error', '60 issues', '60%'], ['warning', '30 issues', '30%'], ['info', '10 issues', '10%']])
    expect(rows[1]).toHaveAttribute('data-highlighted', 'true')
    expect(cells(screen.getByTestId('chart-tooltip-total'))).toEqual(['Total', '100 issues', ''])
  })

  it('has no share column for a chart without shares', () => {
    render(<ChartTooltip active payload={rows} label="x" highlighted={null} unitOf={words} additive={false} />)
    expect(screen.getAllByTestId('chart-tooltip-row')[0].querySelectorAll('td')).toHaveLength(2)
  })

  it('shows no total when the rows\' unit is unknown', () => {
    render(<ChartTooltip active payload={rows} label="x" highlighted={null} unitOf={() => undefined} additive />)
    expect(screen.queryByTestId('chart-tooltip-total')).toBeNull()
  })

  it('renders nothing while the pointer is off the chart', () => {
    const { container } = render(<ChartTooltip active={false} payload={rows} highlighted={null} unitOf={words} additive={false} />)
    expect(container).toBeEmptyDOMElement()
  })
})
