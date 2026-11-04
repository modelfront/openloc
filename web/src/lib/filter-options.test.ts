import { describe, it, expect } from 'vitest'
import {
  DEFAULT_HTML_ATTRIBUTES, DEFAULT_XML, columnNumber, filterProfileSchema, optionsOf, optionsSummary, sortFilterProfiles, targetColumnsProblem,
  valuesOf, xlsxRulesEmpty,
} from './filter-options'
import type { XLSXTargetColumn } from '@/api'

const rules = (sheets: string[], header_row: boolean, columns: string[], target_columns: XLSXTargetColumn[] = []) =>
  ({ sheets, header_row, columns, target_columns })

describe('columnNumber', () => {
  it('numbers letters up to XFD and refuses anything else', () => {
    expect(columnNumber('A')).toBe(1)
    expect(columnNumber('Z')).toBe(26)
    expect(columnNumber('AA')).toBe(27)
    expect(columnNumber('XFD')).toBe(16384)
    expect(columnNumber('XFE')).toBe(0)
    expect(columnNumber('b')).toBe(0)
    expect(columnNumber('A1')).toBe(0)
    expect(columnNumber('')).toBe(0)
  })
})

describe('optionsOf', () => {
  const values = { ...valuesOf(undefined), inlineHTML: false, sheets: [' Main ', 'Main', ''], headerRow: true, columns: ['b', ' C', 'B'] }

  it('keeps only the format\'s own fields', () => {
    expect(Object.keys(optionsOf('html', values))).toEqual(['html'])
    expect(Object.keys(optionsOf('xml', values))).toEqual(['xml'])
    expect(optionsOf('po', values)).toEqual({ inline_html: false })
  })

  it('starts a new HTML or XML profile from the parser defaults', () => {
    expect(optionsOf('html', valuesOf(undefined))).toEqual({
      html: { attributes: DEFAULT_HTML_ATTRIBUTES, meta_description: true, skip_pre: true },
    })
    expect(optionsOf('xml', valuesOf(undefined))).toEqual({ xml: DEFAULT_XML })
  })

  it('lower-cases and merges HTML rows, dropping empty ones', () => {
    const html = optionsOf('html', {
      ...values,
      htmlAttributes: [{ element: ' Button ', attributes: ['ARIA-label'] }, { element: 'button', attributes: ['title', 'aria-label'] }, { element: 'td', attributes: [] }],
    }).html
    expect(html?.attributes).toEqual({ button: ['aria-label', 'title'] })
  })

  it('reads do-not-translate markers as attribute=value', () => {
    expect(optionsOf('xml', { ...values, xmlNoTranslate: ['status=locked', 'hidden=', 'status=locked'] }).xml?.no_translate)
      .toEqual([{ name: 'status', value: 'locked' }, { name: 'hidden', value: '' }])
  })

  it('trims and de-duplicates the Excel lists, upper-casing the columns', () => {
    expect(optionsOf('xlsx', values)).toEqual({ inline_html: false, xlsx: rules(['Main'], true, ['B', 'C']) })
  })

  it('round-trips stored options, with inline HTML on when a format has none stored', () => {
    expect(valuesOf(undefined)).toMatchObject({ inlineHTML: true, sheets: [], headerRow: false, columns: [] })
    const stored = { inline_html: true, xlsx: rules(['S1'], false, ['D']) }
    expect(optionsOf('xlsx', valuesOf(stored))).toEqual(stored)
    const html = { html: { attributes: { a: ['title'], button: ['aria-label'] }, meta_description: false, skip_pre: false } }
    expect(optionsOf('html', valuesOf(html))).toEqual(html)
  })
})

describe('optionsSummary', () => {
  it('says how each format reads its files', () => {
    expect(optionsSummary('html', { html: { attributes: { img: ['alt'] }, meta_description: true, skip_pre: true } }))
      .toBe('Attributes of img · meta description · <pre> kept as is')
    expect(optionsSummary('xml', { xml: { ...DEFAULT_XML, skip_elements: ['code'] } }))
      .toBe('14 inline elements · skips code · translate=no, translatable=false')
    expect(optionsSummary('json_i18n', { inline_html: true })).toBe('Strings contain HTML')
    expect(optionsSummary('txt', { inline_html: false })).toBe('Plain text strings')
  })

  it('says when an Excel profile leaves the columns to the upload', () => {
    expect(xlsxRulesEmpty(rules([], false, []))).toBe(true)
    expect(optionsSummary('xlsx', { inline_html: false, xlsx: rules([], false, []) }))
      .toBe('Upload asks for columns · Plain text strings')
  })

  it('names the sheets and columns of an Excel profile, shortening long lists', () => {
    expect(optionsSummary('xlsx', { inline_html: true, xlsx: rules([], true, ['B', 'C']) }))
      .toBe('Columns B, C · header row · Strings contain HTML')
    expect(optionsSummary('xlsx', { inline_html: false, xlsx: rules(['Main'], false, ['A', 'B', 'C', 'D', 'E', 'F']) }))
      .toBe('Sheet Main · Columns A, B, C, D +2 more · Plain text strings')
    expect(optionsSummary('xlsx', { inline_html: false, xlsx: rules(['Main', 'Data'], true, []) }))
      .toBe('Sheets Main, Data · Every text cell · header row · Plain text strings')
  })
})

describe('filterProfileSchema', () => {
  const form = (patch: object = {}) => ({
    name: 'Product sheets', format: 'xlsx', options: valuesOf(undefined), ...patch,
  })
  const messages = (v: unknown) => {
    const r = filterProfileSchema.safeParse(v)
    return r.success ? [] : r.error.issues.map(i => `${i.path.join('.')}: ${i.message}`)
  }

  it('accepts a valid form', () => {
    expect(messages(form())).toEqual([])
  })

  it('requires a name and a known format', () => {
    expect(messages(form({ name: '  ', format: '' }))).toEqual(['name: Enter a name.', 'format: Choose a format.'])
  })

  it('names the first column that is not a letter and the first sheet name that is too long', () => {
    const options = { ...valuesOf(undefined), sheets: ['x'.repeat(32)], columns: ['b', '12', 'C'] }
    expect(messages(form({ options }))).toEqual([
      `options.sheets: "${'x'.repeat(32)}" is not a sheet name: 1 to 31 characters.`,
      'options.columns: "12" is not a column letter, such as B or AA.',
    ])
  })
})

describe('filterProfileSchema: HTML and XML', () => {
  const messages = (options: object) => {
    const r = filterProfileSchema.safeParse({ name: 'x', format: 'html', options: { ...valuesOf(undefined), ...options } })
    return r.success ? [] : r.error.issues.map(i => i.message)
  }

  it('refuses attributes a translator must not set, and bad names', () => {
    expect(messages({ htmlAttributes: [{ element: 'button', attributes: ['onclick'] }] }))
      .toEqual(['"onclick" cannot be translated: only text attributes such as alt, title, placeholder, label and aria-label can, and value on an input.'])
    expect(messages({ htmlAttributes: [{ element: 'a', attributes: ['HREF'] }] })).toHaveLength(1)
    expect(messages({ htmlAttributes: [{ element: '<b>', attributes: ['title'] }] })).toEqual(['"<b>" is not an element name.'])
    expect(messages({ htmlAttributes: [{ element: '', attributes: [] }] })).toEqual([])
    expect(messages({ xmlSkip: ['1code'] })).toEqual(['"1code" is not an element name.'])
    expect(messages({ xmlNoTranslate: ['=no'] })).toEqual(['"=no" is not a marker: write attribute=value, such as translate=no.'])
    // An empty value would match every element without the attribute.
    expect(messages({ xmlNoTranslate: ['translate'] })).toEqual(['"translate" is not a marker: write attribute=value, such as translate=no.'])
    expect(messages({ xmlNoTranslate: ['translate='] })).toHaveLength(1)
  })

  it('allows only text attributes, and none of a page-configuring element', () => {
    // A text attribute on an element that configures the page is refused too.
    expect(messages({ htmlAttributes: [{ element: 'meta', attributes: ['title'] }] }))
      .toEqual(['The attributes of meta cannot be translated: it configures the page.'])
    expect(messages({ htmlAttributes: [{ element: 'span', attributes: ['aria-label', 'label'] }, { element: 'input', attributes: ['value'] }] })).toEqual([])
    for (const [element, attribute] of [['a', 'ping'], ['link', 'imagesrcset'], ['img', 'longdesc'], ['div', 'aria-labelledby'], ['span', 'data-tooltip'], ['img', 'data-src'], ['option', 'value'], ['param', 'value']]) {
      expect(messages({ htmlAttributes: [{ element, attributes: [attribute] }] }), `${element} ${attribute}`).toHaveLength(1)
    }
  })
})

describe('sortFilterProfiles', () => {
  it('groups by format in display order, built-ins first, then by name', () => {
    const p = (name: string, format: string, builtin = false) => ({ name, format, builtin })
    const sorted = sortFilterProfiles([p('Zeta', 'po'), p('Alpha', 'po'), p('Default', 'po', true), p('Sheets', 'xlsx'), p('Pages', 'html')])
    expect(sorted.map(s => `${s.format}:${s.name}`)).toEqual(['html:Pages', 'xlsx:Sheets', 'po:Default', 'po:Alpha', 'po:Zeta'])
  })
})

describe('Excel target columns', () => {
  const fr = { language: 'fr', column: 'C' }

  it('stores trimmed rows with upper-case columns and drops empty ones', () => {
    const v = { ...valuesOf(undefined), columns: ['B'], targetColumns: [{ language: 'fr', column: ' c ' }, { language: '', column: '' }] }
    expect(optionsOf('xlsx', v).xlsx?.target_columns).toEqual([fr])
    const stored = { inline_html: true, xlsx: rules([], true, ['B'], [fr, { language: 'de', column: 'D' }]) }
    expect(optionsOf('xlsx', valuesOf(stored))).toEqual(stored)
  })

  it('counts as rules and shows in the summary', () => {
    expect(xlsxRulesEmpty(rules([], false, [], [fr]))).toBe(false)
    expect(optionsSummary('xlsx', { inline_html: true, xlsx: rules([], true, ['B'], [fr, { language: 'de', column: 'D' }]) }))
      .toBe('Column B · Translations in FR C, DE D · header row · Strings contain HTML')
  })

  it('words each problem as the server does', () => {
    expect(targetColumnsProblem(['B'], [fr])).toBeNull()
    expect(targetColumnsProblem([], [])).toBeNull()
    expect(targetColumnsProblem(['A', 'B'], [fr])).toBe('Choose exactly one source column for the translations to go next to.')
    expect(targetColumnsProblem([], [fr])).toBe('Choose exactly one source column for the translations to go next to.')
    expect(targetColumnsProblem(['c'], [fr])).toBe('Column C holds the source text, so fr cannot be written into it.')
    expect(targetColumnsProblem(['B'], [{ language: '', column: 'D' }])).toBe('Choose the language for column D.')
    expect(targetColumnsProblem(['B'], [{ language: 'fr', column: 'C1' }])).toBe('"C1" is not a column, such as C.')
    expect(targetColumnsProblem(['B'], [fr, { language: 'fr', column: 'D' }])).toBe('fr has two target columns.')
    expect(targetColumnsProblem(['B'], [fr, { language: 'de', column: 'c' }])).toBe('Column C is the target of both fr and de.')
  })

  it('puts the problem on the target columns field', () => {
    const r = filterProfileSchema.safeParse({
      name: 'Multilingual', format: 'xlsx', options: { ...valuesOf(undefined), columns: ['C'], targetColumns: [fr] },
    })
    expect(r.success ? [] : r.error.issues.map(i => `${i.path.join('.')}: ${i.message}`))
      .toEqual(['options.targetColumns: Column C holds the source text, so fr cannot be written into it.'])
  })
})
