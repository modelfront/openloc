import { describe, it, expect } from 'vitest'
import { encodeLangPair, decodeLangPair } from './lang-pair'

describe('lang-pair encoding', () => {
  it('round-trips simple language codes', () => {
    const key = encodeLangPair('en', 'fr')
    expect(decodeLangPair(key)).toEqual({ source: 'en', target: 'fr' })
  })

  it('round-trips hyphenated locale codes', () => {
    // Hyphenated locales round-trip: the separator is never '-'.
    const key = encodeLangPair('en-us', 'es-mx')
    const decoded = decodeLangPair(key)
    expect(decoded).toEqual({ source: 'en-us', target: 'es-mx' })
  })

  it('round-trips complex locale codes', () => {
    const key = encodeLangPair('zh-hans', 'pt-br')
    expect(decodeLangPair(key)).toEqual({ source: 'zh-hans', target: 'pt-br' })
  })

  it('returns null for "all"', () => {
    expect(decodeLangPair('all')).toBeNull()
  })

  it('returns null for keys without separator', () => {
    expect(decodeLangPair('invalid')).toBeNull()
  })

  it('handles target language with hyphens', () => {
    const key = encodeLangPair('en', 'zh-hans')
    expect(decodeLangPair(key)).toEqual({ source: 'en', target: 'zh-hans' })
  })

  it('handles source language with hyphens', () => {
    const key = encodeLangPair('pt-br', 'en')
    expect(decodeLangPair(key)).toEqual({ source: 'pt-br', target: 'en' })
  })
})
