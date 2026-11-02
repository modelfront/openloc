import { describe, it, expect } from 'vitest'
import { ANY_LANGUAGE, nextSourceLanguages } from './SourceLanguagesPicker'

describe('nextSourceLanguages', () => {
  it('picking Any language clears the languages', () => {
    expect(nextSourceLanguages(['de', 'fr'], ['de', 'fr', ANY_LANGUAGE])).toEqual([])
  })
  it('picking a language clears Any language', () => {
    expect(nextSourceLanguages([ANY_LANGUAGE], [ANY_LANGUAGE, 'de'])).toEqual(['de'])
  })
  it('adds and removes languages', () => {
    expect(nextSourceLanguages(['de'], ['de', 'fr'])).toEqual(['de', 'fr'])
    expect(nextSourceLanguages(['de', 'fr'], ['fr'])).toEqual(['fr'])
  })
  it('removing the last language leaves Any language', () => {
    expect(nextSourceLanguages(['de'], [])).toEqual([])
  })
})
