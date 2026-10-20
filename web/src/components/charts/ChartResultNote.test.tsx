import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ChartResultNote } from './ChartResultNote'

describe('ChartResultNote', () => {
  // The common case is that nothing is wrong; a marker on every chart would
  // stop meaning anything.
  it('shows nothing when the answer is exactly what was asked for', () => {
    const { container } = render(<ChartResultNote grain="week" requestedGrain="week" />)
    expect(container).toBeEmptyDOMElement()
  })

  it('shows nothing when the grain is unknown', () => {
    const { container } = render(<ChartResultNote />)
    expect(container).toBeEmptyDOMElement()
  })

  // Rows were dropped and the chart looks complete — the one case that has to
  // be a warning rather than a note.
  it('warns when the result was truncated', () => {
    render(<ChartResultNote truncated />)
    expect(screen.getByTestId('chart-truncated-note')).toBeInTheDocument()
    expect(screen.getByLabelText('Incomplete result')).toBeInTheDocument()
  })

  it('notes a widened grain, naming both intervals', async () => {
    render(<ChartResultNote grain="month" requestedGrain="day" />)
    await userEvent.hover(screen.getByTestId('chart-grain-note'))
    // The tooltip is portalled and mounts after the hover; the longer budget
    // keeps the wait from losing under full-suite load.
    expect(await screen.findByText(/Shown monthly.*chart daily/, undefined, { timeout: 3000 })).toBeInTheDocument()
    expect(screen.queryByTestId('chart-truncated-note')).not.toBeInTheDocument()
  })

  // Truncation is the more serious of the two: it means the numbers are wrong,
  // not merely coarse.
  it('prefers the warning when both happened', () => {
    render(<ChartResultNote truncated grain="month" requestedGrain="day" />)
    expect(screen.getByTestId('chart-truncated-note')).toBeInTheDocument()
    expect(screen.queryByTestId('chart-grain-note')).not.toBeInTheDocument()
  })
})
