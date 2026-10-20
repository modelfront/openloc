import { describe, it, expect } from 'vitest'
import { isSameOriginPath } from './safe-redirect'

describe('isSameOriginPath', () => {
  it('allows genuine same-origin paths', () => {
    expect(isSameOriginPath('/oauth/authorize?client_id=x')).toBe(true)
    expect(isSameOriginPath('/select-org')).toBe(true)
    expect(isSameOriginPath('/')).toBe(true)
  })

  it('rejects the open-redirect payloads', () => {
    // Pins: a backslash after the leading slash introduces an authority.
    expect(isSameOriginPath('/\\evil.example/phish')).toBe(false)
    expect(isSameOriginPath('//evil.example/phish')).toBe(false)
    expect(isSameOriginPath('/\\/evil.example')).toBe(false)
    expect(isSameOriginPath('\\/evil.example')).toBe(false)
    expect(isSameOriginPath('https://evil.example')).toBe(false)
    expect(isSameOriginPath('http:evil.example')).toBe(false)
    expect(isSameOriginPath('javascript:alert(1)')).toBe(false)
    expect(isSameOriginPath('')).toBe(false)
    expect(isSameOriginPath('relative/path')).toBe(false)
    // Browsers strip tab and newline characters before parsing, so these
    // collapse to the protocol-relative `//evil.example`.
    expect(isSameOriginPath('/\t/evil.example')).toBe(false)
    expect(isSameOriginPath('/\n/evil.example')).toBe(false)
  })
})
