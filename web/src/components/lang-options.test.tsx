import { describe, expect, it } from 'vitest'
import { toLangOptions } from './lang-options'

// Language codes read uppercase everywhere; the value a picker returns and the
// code the search matches stay the stored code.
describe('toLangOptions', () => {
  it('shows the code uppercase and keeps the stored code as value and keyword', () => {
    const [opt] = toLangOptions([{ code: 'pt-br', name: 'Portuguese (Brazil)' }])
    expect(opt.value).toBe('pt-br')
    expect(opt.keywords).toBe('pt-br')
    expect(opt.label).toBe('Portuguese (Brazil)')
    expect(opt.chip).toBe('PT-BR')
    expect(opt.shortLabel).toBe('PT-BR')
  })
})
