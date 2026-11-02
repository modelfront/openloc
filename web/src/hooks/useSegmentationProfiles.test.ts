import { describe, it, expect } from 'vitest'
import type { SegmentationProfile } from '../api'
import { isSegmentationProfileMissing, pickSegmentationProfile } from './useSegmentationProfiles'

const profile = (id: number, sourceLanguage: string, extra: Partial<SegmentationProfile> = {}): SegmentationProfile => ({
  id, name: `P${id}`, source_languages: sourceLanguage ? [sourceLanguage] : [], builtin: false, description: '',
  definition: { mode: 'sentence', rules: [], abbreviations: [], number_abbreviations: [] },
  created_at: '', updated_at: '', ...extra,
})

describe('pickSegmentationProfile', () => {
  const forIcelandic = [profile(1, '', { builtin: true }), profile(5, 'is', { builtin: true, suggested: true }), profile(9, 'is')]

  it('keeps a pick the list holds', () => {
    expect(pickSegmentationProfile(forIcelandic, 9)).toBe(9)
  })

  it.each([null, undefined, 42])('takes the suggested profile for %s', id => {
    expect(pickSegmentationProfile(forIcelandic, id)).toBe(5)
  })

  it('falls back to the default built-in when nothing is suggested', () => {
    const all = [profile(3, '', { builtin: true, builtin_key: 'builtin:conservative@1' }), profile(1, '', { builtin: true, builtin_key: 'builtin:default@1' }), profile(9, 'is')]
    expect(pickSegmentationProfile(all, null)).toBe(1)
  })

  it('is null while there is nothing to pick from', () => {
    expect(pickSegmentationProfile([], 9)).toBeNull()
  })
})

describe('isSegmentationProfileMissing', () => {
  const list = [profile(1, '', { builtin: true, suggested: true }), profile(2, 'is')]
  it('is false while a profile resolves', () => {
    expect(isSegmentationProfileMissing(list, null, 2)).toBe(false)
    expect(isSegmentationProfileMissing(list, null, null)).toBe(false)
  })
  it('is true when nothing resolves, as while the list loads', () => {
    expect(isSegmentationProfileMissing([], null, 2)).toBe(true)
  })
  it('does not block when the list failed to load: the server picks', () => {
    expect(isSegmentationProfileMissing([], new Error('down'), 2)).toBe(false)
  })
})

