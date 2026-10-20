import { describe, it, expect } from 'vitest'
import { parseQARuleConfig } from './qa-rule-config'

describe('parseQARuleConfig (base64-tolerant parser)', () => {
  describe('backend returns object', () => {
    it('returns the object as-is', () => {
      const cfg = { missing: false, extra: true, unclosed: true, unopened: true }
      expect(parseQARuleConfig(cfg)).toEqual(cfg)
    })

    it('handles empty object', () => {
      expect(parseQARuleConfig({})).toEqual({})
    })
  })

  describe('alternative serializer: raw JSON string', () => {
    it('parses JSON string to object', () => {
      expect(parseQARuleConfig('{"missing":false,"extra":true}')).toEqual({
        missing: false,
        extra: true,
      })
    })

    it('parses JSON string with empty object', () => {
      expect(parseQARuleConfig('{}')).toEqual({})
    })
  })

  describe('base64-encoded JSON string', () => {
    it('decodes base64 → parses JSON', () => {
      // btoa('{"missing":false,"extra":true,"unclosed":true,"unopened":true}')
      const b64 = btoa('{"missing":false,"extra":true,"unclosed":true,"unopened":true}')
      expect(parseQARuleConfig(b64)).toEqual({
        missing: false,
        extra: true,
        unclosed: true,
        unopened: true,
      })
    })

    it('works for empty config base64', () => {
      const b64 = btoa('{}')
      expect(parseQARuleConfig(b64)).toEqual({})
    })

    // Pins the real shape: base64 from Go's []byte JSON marshaler.
    it('decodes a real config string from the backend', () => {
      const real = 'eyJleHRyYSI6dHJ1ZSwibWlzc2luZyI6dHJ1ZSwidW5jbG9zZWQiOnRydWUsInVub3BlbmVkIjp0cnVlfQ=='
      const result = parseQARuleConfig(real)
      expect(result).toEqual({
        extra: true,
        missing: true,
        unclosed: true,
        unopened: true,
      })
    })
  })

  describe('garbage tolerance', () => {
    it('returns {} for null', () => {
      expect(parseQARuleConfig(null)).toEqual({})
    })

    it('returns {} for undefined', () => {
      expect(parseQARuleConfig(undefined)).toEqual({})
    })

    it('returns {} for number', () => {
      expect(parseQARuleConfig(42)).toEqual({})
    })

    it('returns {} for unparseable string (not JSON, not base64)', () => {
      expect(parseQARuleConfig('not-json-not-base64-just-garbage')).toEqual({})
    })

    it('returns {} for boolean', () => {
      expect(parseQARuleConfig(true)).toEqual({})
    })
  })
})
