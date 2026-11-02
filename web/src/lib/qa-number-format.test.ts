import { describe, it, expect } from 'vitest'
import { NUMBER_FORMATS, numberFormatFromKey, numberFormatKey, parseNumberFormatConfig } from './qa-number-format'

describe('parseNumberFormatConfig', () => {
  it('fills in the default for a missing or unknown config', () => {
    expect(parseNumberFormatConfig(undefined)).toEqual({ mode: 'allow', languages: [] })
    expect(parseNumberFormatConfig({ mode: 'strict' })).toEqual({ mode: 'allow', languages: [] })
  })

  it('reads the languages, from an object or base64, and drops broken entries', () => {
    const config = { mode: 'require', languages: [{ language: 'es', decimal: '.', group: ',' }, { language: 'fr' }, null] }
    const want = { mode: 'require', languages: [{ language: 'es', decimal: '.', group: ',' }] }
    expect(parseNumberFormatConfig(config)).toEqual(want)
    expect(parseNumberFormatConfig(btoa(JSON.stringify(config)))).toEqual(want)
  })
})

describe('NUMBER_FORMATS', () => {
  // The API accepts a decimal of "." or "," and a group of ",", ".", " ", "'"
  // or none, the two different: eight pairs, and the list names each once.
  it('names every separator pair the API accepts, once', () => {
    const pairs = new Set<string>()
    for (const decimal of ['.', ',']) {
      for (const group of [',', '.', ' ', "'", '']) {
        if (decimal !== group) pairs.add(numberFormatKey({ decimal, group }))
      }
    }
    expect(new Set(NUMBER_FORMATS.map(numberFormatKey))).toEqual(pairs)
    expect(NUMBER_FORMATS).toHaveLength(pairs.size)
  })

  it('round-trips a pair through its key', () => {
    expect(numberFormatFromKey(numberFormatKey({ decimal: ',', group: ' ' }))).toEqual({ decimal: ',', group: ' ' })
  })
})
