// The builder's narrowing rules. Both are pure functions,
// so the decisions are testable even though the dialog around them is not
// worth mounting for every case.
import { describe, it, expect } from 'vitest'
import { axisOptions, toggleValue } from './ChartBuilderDialog'
import type { CatalogMeasure } from '@/api'

const GRAINS = ['day', 'week', 'month']

const QA: CatalogMeasure = {
  name: 'qa_issues',
  definition: 'QA findings.',
  dimensions: ['check_type', 'severity'],
  values: [{ name: 'issues', unit: 'issues' }],
  filters: ['date_range'],
}

const TOKENS: CatalogMeasure = {
  name: 'ai_token_usage',
  definition: 'Tokens.',
  dimensions: ['provider'],
  values: [
    { name: 'input_tokens', unit: 'tokens' },
    { name: 'output_tokens', unit: 'tokens' },
    { name: 'calls', unit: 'calls' },
  ],
  filters: ['date_range'],
}

const VOLUME: CatalogMeasure = {
  name: 'words_processed',
  definition: 'Words.',
  dimensions: ['target_language'],
  values: [
    { name: 'words', unit: 'words' },
    { name: 'segments', unit: 'segments' },
  ],
  filters: ['date_range'],
}

const keys = (m: CatalogMeasure | undefined, type: string) =>
  axisOptions(m, GRAINS, type).map(o => o.value)

describe('axisOptions', () => {
  // Line and area imply continuity: a category axis draws a trend across
  // things that have no order.
  it('offers only dates on line and area', () => {
    expect(keys(QA, 'line')).toEqual(['day', 'week', 'month'])
    expect(keys(QA, 'area')).toEqual(['day', 'week', 'month'])
  })

  it('offers dates and categories on bar', () => {
    expect(keys(QA, 'bar')).toEqual(['day', 'week', 'month', 'check_type', 'severity'])
  })

  // Slices are parts of one whole; time is not.
  it('offers only categories on pie', () => {
    expect(keys(QA, 'pie')).toEqual(['check_type', 'severity'])
  })

  it('never offers a dimension the measure does not have', () => {
    expect(keys(QA, 'bar')).not.toContain('provider')
    expect(keys(TOKENS, 'bar')).not.toContain('check_type')
  })

  it('offers nothing before a measure is chosen on a pie', () => {
    expect(keys(undefined, 'pie')).toEqual([])
  })
})

describe('toggleValue', () => {
  it('combines values that count the same thing', () => {
    expect(toggleValue(TOKENS.values, ['input_tokens'], 'output_tokens', false))
      .toEqual(['input_tokens', 'output_tokens'])
  })

  // Words plus segments on one axis is a number of nothing, so the pick
  // REPLACES rather than adding.
  it('replaces the selection when the units differ', () => {
    expect(toggleValue(VOLUME.values, ['words'], 'segments', false)).toEqual(['segments'])
    expect(toggleValue(TOKENS.values, ['input_tokens'], 'calls', false)).toEqual(['calls'])
  })

  it('unticks a value it already holds', () => {
    expect(toggleValue(TOKENS.values, ['input_tokens', 'output_tokens'], 'input_tokens', false))
      .toEqual(['output_tokens'])
  })

  // Something has to be plotted; emptying the last one would leave a chart of
  // nothing and disable Create with no way back except re-ticking.
  it('keeps the last value rather than emptying the selection', () => {
    expect(toggleValue(TOKENS.values, ['input_tokens'], 'input_tokens', false)).toEqual(['input_tokens'])
  })

  // A split already draws one series per category value, reading a single
  // column — the pivot would drop the rest.
  it('collapses to one value while splitting', () => {
    expect(toggleValue(TOKENS.values, ['input_tokens'], 'output_tokens', true)).toEqual(['output_tokens'])
  })
})
