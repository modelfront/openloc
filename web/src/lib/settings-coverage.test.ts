// The warning beside the Translation memory title: which target languages no
// assigned TM covers. TM lookups match the same base language (a de TM serves
// de-de), so coverage does too.
import { describe, it, expect } from 'vitest'
import { missingTMWarning, mtRouterCoverageWarning, uncoveredRouterTargets } from './settings-coverage'

const base = { sourceLanguage: 'en', targetLanguages: ['de', 'de-de', 'fr'] }

describe('missingTMWarning', () => {
  it('says nothing when every language has a TM', () => {
    expect(missingTMWarning({ ...base, tmTargetLanguages: ['de', 'fr'] })).toBeNull()
  })

  it('a TM for the base language covers its regional variants', () => {
    expect(missingTMWarning({ ...base, tmTargetLanguages: ['de-at', 'fr'] })).toBeNull()
  })

  it('names the languages without a TM', () => {
    expect(missingTMWarning({ ...base, tmTargetLanguages: ['fr'] }))
      .toBe('No translation memory for en→de, en→de-de.')
  })

  it('names every language when there is no TM at all', () => {
    expect(missingTMWarning({ ...base, tmTargetLanguages: [] }))
      .toBe('No translation memory for en→de, en→de-de, en→fr.')
  })
})

// The warning beside the MT router picker: which target languages the router
// sends to no engine. Mirrors the server's routing, which matches on the
// target language alone.
describe('uncoveredRouterTargets', () => {
  const routes = (...langs: string[]) => ({ rules: langs.map(target_lang => ({ target_lang })) })

  it('a route covers its own language', () => {
    expect(uncoveredRouterTargets(routes('es', 'de'), ['es', 'de'])).toEqual([])
  })

  it('a base-language route covers its regional variants', () => {
    expect(uncoveredRouterTargets(routes('fr'), ['fr-ca'])).toEqual([])
  })

  it('a regional route covers its base language and sibling variants', () => {
    expect(uncoveredRouterTargets(routes('fr-ca'), ['fr'])).toEqual([])
    expect(uncoveredRouterTargets(routes('es-es'), ['es-mx'])).toEqual([])
  })

  it('compares codes case- and separator-insensitively', () => {
    expect(uncoveredRouterTargets(routes('PT_BR'), ['pt-pt'])).toEqual([])
    expect(uncoveredRouterTargets(routes('es'), ['ES_MX'])).toEqual([])
  })

  it('returns every target no route covers, as given', () => {
    expect(uncoveredRouterTargets(routes('es'), ['es', 'de', 'fr-CA'])).toEqual(['de', 'fr-CA'])
  })

  it('a router without routes or default covers nothing', () => {
    expect(uncoveredRouterTargets(routes(), ['es', 'de'])).toEqual(['es', 'de'])
  })

  it('a default engine covers every language', () => {
    expect(uncoveredRouterTargets({ ...routes('es'), default_mt_engine_id: 3 }, ['de', 'ja'])).toEqual([])
    expect(uncoveredRouterTargets({ ...routes(), default_ai_engine_id: 4 }, ['de'])).toEqual([])
  })

  it('no target languages, nothing uncovered', () => {
    expect(uncoveredRouterTargets(routes('es'), [])).toEqual([])
  })
})

describe('mtRouterCoverageWarning', () => {
  const esOnly = { rules: [{ target_lang: 'es' }] }

  it('says nothing when every target is covered', () => {
    expect(mtRouterCoverageWarning(esOnly, ['es-mx'])).toBeNull()
  })

  it('names one uncovered language', () => {
    expect(mtRouterCoverageWarning(esOnly, ['es', 'de']))
      .toBe('This router has no engine for DE. Files in this language are not machine translated.')
  })

  it('names several uncovered languages', () => {
    expect(mtRouterCoverageWarning(esOnly, ['es', 'de', 'fr-ca']))
      .toBe('This router has no engine for DE, FR-CA. Files in these languages are not machine translated.')
  })
})
