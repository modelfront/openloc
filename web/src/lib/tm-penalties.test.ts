import { describe, it, expect } from 'vitest'
import { tmPenaltyLineText, tmScoreBreakdown } from './tm-penalties'

describe('tmScoreBreakdown', () => {
  it('is null for a match nothing docked', () => {
    expect(tmScoreBreakdown({ percent: 100 })).toBeNull()
    expect(tmScoreBreakdown({ percent: 100, penalties: [] })).toBeNull()
  })

  // The text match is derived from the shown percent, never rounded from the
  // raw score on its own, so "text match − penalties" always equals the card.
  it('adds the penalties back onto the shown percent', () => {
    const b = tmScoreBreakdown({
      percent: 93,
      penalties: [
        { kind: 'source_variant', percent: 1, tm_language: 'en', project_language: 'en-us' },
        { kind: 'target_variant', percent: 1, tm_language: 'fr', project_language: 'fr-fr' },
        { kind: 'tm', percent: 5 },
      ],
    })
    expect(b?.textPercent).toBe(100)
    expect(b!.textPercent - b!.lines.reduce((s, l) => s + l.percent, 0)).toBe(93)
  })

  it('labels each deduction', () => {
    const b = tmScoreBreakdown({
      percent: 93,
      penalties: [
        { kind: 'source_variant', percent: 1, tm_language: 'en', project_language: 'en-us' },
        { kind: 'target_variant', percent: 1, tm_language: 'fr', project_language: 'fr-fr' },
        { kind: 'tm', percent: 5 },
      ],
    })
    expect(b?.lines.map(l => l.label)).toEqual(['Source language variant', 'Target language variant', 'Penalty set on this TM'])
    expect(b?.lines[0]).toMatchObject({ tmLanguage: 'en', projectLanguage: 'en-us' })
  })

  it('still shows the deduction of a kind it does not know', () => {
    const b = tmScoreBreakdown({ percent: 97, penalties: [{ kind: 'new_kind' as never, percent: 3 }] })
    expect(b?.lines[0].label).toBe('Penalty')
    expect(b?.textPercent).toBe(100)
  })
})

describe('tmPenaltyLineText', () => {
  it('names the languages of a variant penalty', () => {
    expect(tmPenaltyLineText({ percent: 1, label: 'Source language variant', tmLanguage: 'en', projectLanguage: 'en-us' }))
      .toBe('−1% Source language variant (TM: en, project: en-us)')
  })

  it('is just the deduction otherwise', () => {
    expect(tmPenaltyLineText({ percent: 5, label: 'Penalty set on this TM' })).toBe('−5% Penalty set on this TM')
  })
})
