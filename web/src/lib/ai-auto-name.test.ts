import { describe, it, expect } from 'vitest'
import { engineAutoName, uniqueName } from './ai-auto-name'

describe('engineAutoName', () => {
  it('leads with what the engine does and parenthesizes the provider', () => {
    expect(engineAutoName('OpenAI GPT-4o mini', 'QE')).toBe('QE (OpenAI GPT-4o mini)')
    expect(engineAutoName('Claude Opus 5', 'QE+APE')).toBe('QE+APE (Claude Opus 5)')
    expect(engineAutoName('Claude Opus 5', 'APE')).toBe('APE (Claude Opus 5)')
    expect(engineAutoName('Claude Opus 5', 'MT')).toBe('MT (Claude Opus 5)')
  })

  it('keeps QE and APE apart, though both are qe_ape-purpose systems', () => {
    // The whole reason the name carries the prompt's mode rather than the
    // system's coarse purpose.
    expect(engineAutoName('Claude Opus 5', 'QE')).not.toBe(engineAutoName('Claude Opus 5', 'APE'))
  })

  it('omits an unresolved mode rather than guessing one', () => {
    expect(engineAutoName('Mock QE', '')).toBe('Mock QE')
  })

  it('returns empty with no provider', () => {
    expect(engineAutoName('', 'MT')).toBe('')
  })
})

describe('uniqueName', () => {
  it('leaves a free name alone', () => {
    expect(uniqueName('Claude Opus 5', ['OpenAI GPT-4o'])).toBe('Claude Opus 5')
  })

  it('starts suffixing at 2', () => {
    expect(uniqueName('X', ['X'])).toBe('X 2')
  })

  it('skips past an existing suffix', () => {
    expect(uniqueName('X', ['X', 'X 2'])).toBe('X 3')
    expect(uniqueName('X', ['X', 'X 2', 'X 3'])).toBe('X 4')
  })

  it('fills a gap rather than always taking the highest', () => {
    expect(uniqueName('X', ['X', 'X 3'])).toBe('X 2')
  })

  it('ignores surrounding whitespace on taken names', () => {
    expect(uniqueName('X', ['  X  '])).toBe('X 2')
  })

  it('passes an empty base through untouched', () => {
    expect(uniqueName('', ['X'])).toBe('')
  })
})
