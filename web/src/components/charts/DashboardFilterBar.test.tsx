import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { DashboardFilterBar, describeFilters, seriesForUnit } from './DashboardFilterBar'

describe('seriesForUnit', () => {
  it('plots the chosen unit when the measure returns that column', () => {
    expect(seriesForUnit('characters', ['week', 'words', 'segments', 'characters'], ['words']))
      .toEqual(['characters'])
  })

  // The load-bearing case: the unit control is dashboard-wide, but "QA issues"
  // returns no words. Its card must keep its own series rather than plot a
  // column that isn't in the rows.
  it("keeps the chart's own series when the measure has no such column", () => {
    expect(seriesForUnit('words', ['severity', 'issues'], ['issues'])).toEqual(['issues'])
  })

  it('falls back when no unit is chosen', () => {
    expect(seriesForUnit(undefined, ['week', 'words'], ['words'])).toEqual(['words'])
  })
})

describe('DashboardFilterBar', () => {
  const noop = vi.fn()

  it('renders nothing when the dashboard enables no controls', () => {
    const { container } = render(<DashboardFilterBar controls={[]} values={{}} onChange={noop} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('renders only the controls the dashboard enables', () => {
    render(<DashboardFilterBar controls={['date_range', 'unit']} values={{}} onChange={noop} />)
    expect(screen.getByTestId('dashboard-filter-date-range')).toBeInTheDocument()
    expect(screen.getByTestId('dashboard-filter-unit')).toBeInTheDocument()
    expect(screen.queryByTestId('dashboard-filter-granularity')).not.toBeInTheDocument()
  })

  it('labels every control', () => {
    render(<DashboardFilterBar controls={['date_range', 'unit', 'granularity', 'customer']} values={{}} onChange={noop} />)
    for (const label of ['Date range', 'Unit', 'Granularity', 'Customer']) {
      expect(screen.getByText(label)).toBeInTheDocument()
    }
  })

  // No "chart default" row: an untouched control shows the value it is actually
  // applying, so the bar never describes a state the charts aren't in.
  it('shows real defaults when nothing is selected', () => {
    render(<DashboardFilterBar controls={['date_range', 'unit', 'granularity']} values={{}} onChange={noop} />)
    expect(screen.getByTestId('dashboard-filter-date-range')).toHaveTextContent('Last 3 months')
    expect(screen.getByTestId('dashboard-filter-unit')).toHaveTextContent('Words')
    expect(screen.getByTestId('dashboard-filter-granularity')).toHaveTextContent('Weekly')
    expect(screen.queryByText(/chart default/i)).not.toBeInTheDocument()
  })

  // "Everything" IS a real answer for a dimension, unlike the three above —
  // so that row stays, and it's what an unselected dimension shows.
  it('defaults a dimension control to its all-values row', () => {
    render(
      <DashboardFilterBar
        controls={['target_language']}
        values={{}}
        options={{ target_language: ['de', 'fr'] }}
        onChange={noop}
      />,
    )
    expect(screen.getByTestId('dashboard-filter-target-language')).toHaveTextContent('All target languages')
  })

  it('renders the selected dimension value', () => {
    render(
      <DashboardFilterBar
        controls={['customer']}
        values={{ customer: 'Acme' }}
        options={{ customer: ['Acme', 'Globex'] }}
        onChange={noop}
      />,
    )
    expect(screen.getByTestId('dashboard-filter-customer')).toHaveTextContent('Acme')
  })

  // Options arrive asynchronously; the control must still render (with just its
  // all-values row) rather than crash or disappear while the query is in flight.
  it('renders a dimension control before its options load', () => {
    render(<DashboardFilterBar controls={['project']} values={{}} onChange={noop} />)
    expect(screen.getByTestId('dashboard-filter-project')).toHaveTextContent('All projects')
  })
})

// What a downloaded chart says it was narrowed to. It must match the filter
// bar's own wording, and never list a filter the chart ignored.
describe('describeFilters', () => {
  it('reads each control the way the filter bar labels it', () => {
    expect(describeFilters(
      { last_days: 30, grain: 'month', unit: 'segments', customer: 'Acme' },
      ['date_range', 'granularity', 'unit', 'customer'],
    )).toEqual([
      ['Date range', 'Last 30 days'],
      ['Granularity', 'Monthly'],
      ['Unit', 'Segments'],
      ['Customer', 'Acme'],
    ])
  })

  // A window the picker does not offer (a chart's own stored 60 days) still
  // has to read as a window, not a bare number.
  it('spells out a window the picker has no label for', () => {
    expect(describeFilters({ last_days: 60 }, ['date_range'])).toEqual([['Date range', 'Last 60 days']])
  })

  // The dashboard can set a customer the measure cannot take; the server drops
  // it for this chart, so the file must not claim it applied.
  it('omits selections the chart does not respond to', () => {
    expect(describeFilters({ last_days: 90, customer: 'Acme' }, ['date_range'])).toEqual([['Date range', 'Last 3 months']])
  })

  it('lists values frozen into the chart itself', () => {
    expect(describeFilters({}, [], { customer: 'Acme' })).toEqual([['Customer', 'Acme (fixed on this chart)']])
  })
})
