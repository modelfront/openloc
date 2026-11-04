// A filter profile's options as the editor holds them, as the API stores
// them, and as one line of the list. Mirrors internal/filterprofile's
// normalized(): only the format's fields, inline HTML on by default, lists
// trimmed and de-duplicated, column letters upper-cased.

import { z } from 'zod'
import type { FilterOptions, FilterXLSXRules, XLSXTargetColumn } from '@/api'
import { FILTER_FORMATS, INLINE_HTML_FORMATS, type FilterFormat } from './filter-formats'

/** One element's translated attributes, as the HTML editor lists them. */
export interface HTMLAttributeRow {
  element: string
  attributes: string[]
}

/** The options of every format, flat, as the editor's form holds them. */
export interface FilterOptionValues {
  inlineHTML: boolean
  sheets: string[]
  headerRow: boolean
  columns: string[]
  /** Each target language's column, next to the one source column in `columns`. */
  targetColumns: XLSXTargetColumn[]
  htmlAttributes: HTMLAttributeRow[]
  metaDescription: boolean
  skipPre: boolean
  xmlInline: string[]
  /** "name=value" markers. */
  xmlNoTranslate: string[]
  xmlRefElements: string[]
  xmlRefAttributes: string[]
  xmlSkip: string[]
}

// The parser's defaults (internal/parser DefaultHTMLOptions and
// DefaultXMLOptions): what a new HTML or XML profile starts with.
export const DEFAULT_HTML_ATTRIBUTES: Record<string, string[]> = {
  a: ['title'], abbr: ['title'], img: ['alt', 'title'], input: ['alt', 'value', 'title', 'placeholder'], td: ['abbr'], th: ['abbr'],
}
export const DEFAULT_XML: Required<NonNullable<FilterOptions['xml']>> = {
  inline_elements: ['b', 'i', 'em', 'strong', 'u', 'sup', 'sub', 'br', 'span', 'a', 'code', 'tt', 'small', 'big'],
  no_translate: [{ name: 'translate', value: 'no' }, { name: 'translatable', value: 'false' }],
  ref_elements: ['meta-ref'],
  ref_attributes: ['linkid'],
  skip_elements: [],
}

/** An XML-style name: a letter, '_' or ':' first, then also digits, '-' and '.'. */
const NAME_RE = /^[\p{L}_:][\p{L}\d_:.-]*$/u
export const isMarkupName = (n: string) => NAME_RE.test(n) && n.length <= 100
/** Attributes a translator may set: text a reader sees or hears, plus value
 *  on an input. The server's allowlist (internal/parser htmlTextAttr). */
const TEXT_ATTRS = new Set(['alt', 'title', 'placeholder', 'abbr', 'label', 'summary', 'aria-label', 'aria-description', 'aria-placeholder', 'aria-roledescription', 'aria-valuetext'])
/** Elements that configure the page: none of their attributes is translated. */
const CONFIG_ELEMENTS = new Set(['meta', 'link', 'base', 'html', 'head'])
function isTextHTMLAttribute(element: string, attribute: string): boolean {
  const name = attribute.toLowerCase()
  return TEXT_ATTRS.has(name) || (name === 'value' && element.toLowerCase() === 'input')
}

/** Excel's limits: a sheet name has 1 to 31 characters, the last column is XFD. */
export const MAX_SHEET_NAME = 31
const MAX_COLUMN = 16384
/** The server's cap on a profile name. */
export const MAX_NAME = 100

export const hasInlineHTML = (format: FilterFormat) => INLINE_HTML_FORMATS.includes(format)

/** The 1-based number of a column letter ("A" = 1, "AA" = 27); 0 when it is not one. */
export function columnNumber(letters: string): number {
  if (!/^[A-Z]{1,3}$/.test(letters)) return 0
  let n = 0
  for (const ch of letters) n = n * 26 + (ch.charCodeAt(0) - 64)
  return n <= MAX_COLUMN ? n : 0
}

function uniqueTrimmed(values: string[], upper: boolean): string[] {
  const out: string[] = []
  for (const raw of values) {
    const v = upper ? raw.trim().toUpperCase() : raw.trim()
    if (v && !out.includes(v)) out.push(v)
  }
  return out
}

/** "FR C": a target column's language, upper-cased like every code in the app, then its letter. */
export function targetColumnLabel(t: XLSXTargetColumn, joiner = ' '): string {
  return `${t.language.toUpperCase()}${joiner}${t.column}`
}

/** Target column rows as stored: trimmed, column upper-cased, empty rows dropped. */
function targetColumnsOf(rows: XLSXTargetColumn[]): XLSXTargetColumn[] {
  return rows
    .map(r => ({ language: r.language.trim(), column: r.column.trim().toUpperCase() }))
    .filter(r => r.language || r.column)
}

/**
 * The first problem with target columns, as the server words it
 * (internal/parser ValidateXLSXTargets); null when there is none.
 */
export function targetColumnsProblem(columns: string[], rows: XLSXTargetColumn[]): string | null {
  const targets = targetColumnsOf(rows)
  if (targets.length === 0) return null
  const sources = uniqueTrimmed(columns, true)
  if (sources.length !== 1) return 'Choose exactly one source column for the translations to go next to.'
  const owner = new Map<string, string>()
  const langs = new Set<string>()
  for (const t of targets) {
    if (!t.language) return `Choose the language for column ${t.column}.`
    if (!columnNumber(t.column)) return `"${t.column}" is not a column, such as C.`
    if (t.column === sources[0]) return `Column ${t.column} holds the source text, so ${t.language} cannot be written into it.`
    if (langs.has(t.language)) return `${t.language} has two target columns.`
    const other = owner.get(t.column)
    if (other) return `Column ${t.column} is the target of both ${other} and ${t.language}.`
    langs.add(t.language)
    owner.set(t.column, t.language)
  }
  return null
}

/** The editor's values for stored options; a format's missing fields take their defaults. */
export function valuesOf(options: FilterOptions | undefined): FilterOptionValues {
  const html = options?.html
  const xml = options?.xml ?? DEFAULT_XML
  return {
    inlineHTML: options?.inline_html ?? true,
    sheets: [...(options?.xlsx?.sheets ?? [])],
    headerRow: options?.xlsx?.header_row ?? false,
    columns: [...(options?.xlsx?.columns ?? [])],
    targetColumns: (options?.xlsx?.target_columns ?? []).map(t => ({ ...t })),
    htmlAttributes: Object.entries(html?.attributes ?? DEFAULT_HTML_ATTRIBUTES)
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([element, attributes]) => ({ element, attributes: [...attributes] })),
    metaDescription: html?.meta_description ?? true,
    skipPre: html?.skip_pre ?? true,
    xmlInline: [...xml.inline_elements],
    xmlNoTranslate: xml.no_translate.map(m => `${m.name}=${m.value}`),
    xmlRefElements: [...xml.ref_elements],
    xmlRefAttributes: [...xml.ref_attributes],
    xmlSkip: [...xml.skip_elements],
  }
}

/** "translate=no" → {name: "translate", value: "no"}; the value may be empty. */
export function parseMarker(text: string): { name: string; value: string } {
  const i = text.indexOf('=')
  return i < 0 ? { name: text.trim(), value: '' } : { name: text.slice(0, i).trim(), value: text.slice(i + 1).trim() }
}

/** The options a save sends for format: its own fields only, normalized. */
export function optionsOf(format: FilterFormat, v: FilterOptionValues): FilterOptions {
  const out: FilterOptions = {}
  if (hasInlineHTML(format)) out.inline_html = v.inlineHTML
  if (format === 'xlsx') {
    out.xlsx = {
      sheets: uniqueTrimmed(v.sheets, false), header_row: v.headerRow, columns: uniqueTrimmed(v.columns, true),
      target_columns: targetColumnsOf(v.targetColumns),
    }
  }
  if (format === 'html') {
    const attributes: Record<string, string[]> = {}
    for (const row of [...v.htmlAttributes].sort((a, b) => a.element.localeCompare(b.element))) {
      const element = row.element.trim().toLowerCase()
      const names = uniqueTrimmed([...(attributes[element] ?? []), ...row.attributes.map(a => a.toLowerCase())], false)
      if (element && names.length) attributes[element] = names
    }
    out.html = { attributes, meta_description: v.metaDescription, skip_pre: v.skipPre }
  }
  if (format === 'xml') {
    out.xml = {
      inline_elements: uniqueTrimmed(v.xmlInline, false),
      no_translate: uniqueTrimmed(v.xmlNoTranslate, false).map(parseMarker).filter(m => m.name),
      ref_elements: uniqueTrimmed(v.xmlRefElements, false),
      ref_attributes: uniqueTrimmed(v.xmlRefAttributes, false),
      skip_elements: uniqueTrimmed(v.xmlSkip, false),
    }
  }
  return out
}

/** Rules that choose nothing: the upload asks which columns to translate. */
export function xlsxRulesEmpty(r: FilterXLSXRules | undefined): boolean {
  return !r || (r.sheets.length === 0 && !r.header_row && r.columns.length === 0 && r.target_columns.length === 0)
}

/** "B, C, D" — at most `max` items, then "+N more". */
function listText(items: string[], max = 4): string {
  return items.length <= max ? items.join(', ') : `${items.slice(0, max).join(', ')} +${items.length - max} more`
}

/** One line for the list: what the profile does with a file. */
export function optionsSummary(format: FilterFormat, options: FilterOptions | undefined): string {
  if (format === 'html') {
    const v = valuesOf(options)
    const elements = v.htmlAttributes.map(r => r.element)
    return [
      elements.length ? `Attributes of ${listText(elements)}` : 'No attributes',
      v.metaDescription ? 'meta description' : 'no meta description',
      v.skipPre ? '<pre> kept as is' : '<pre> translated',
    ].join(' · ')
  }
  if (format === 'xml') {
    const v = valuesOf(options)
    const parts = [`${v.xmlInline.length} inline ${v.xmlInline.length === 1 ? 'element' : 'elements'}`]
    if (v.xmlSkip.length) parts.push(`skips ${listText(v.xmlSkip)}`)
    if (v.xmlNoTranslate.length) parts.push(listText(v.xmlNoTranslate))
    return parts.join(' · ')
  }
  if (!hasInlineHTML(format)) return 'Default reading'
  const parts: string[] = []
  if (format === 'xlsx') {
    const r = options?.xlsx
    if (xlsxRulesEmpty(r)) {
      parts.push('Upload asks for columns')
    } else if (r) {
      if (r.sheets.length) parts.push(`${r.sheets.length === 1 ? 'Sheet' : 'Sheets'} ${listText(r.sheets)}`)
      parts.push(r.columns.length ? `${r.columns.length === 1 ? 'Column' : 'Columns'} ${listText(r.columns)}` : 'Every text cell')
      if (r.target_columns.length) parts.push(`Translations in ${listText(r.target_columns.map(t => targetColumnLabel(t)))}`)
      if (r.header_row) parts.push('header row')
    }
  }
  parts.push((options?.inline_html ?? true) ? 'Strings contain HTML' : 'Plain text strings')
  return parts.join(' · ')
}

function namesRefine(kind: 'element' | 'attribute') {
  return (names: string[], ctx: z.RefinementCtx) => {
    const bad = names.find(n => !isMarkupName(n.trim()))
    if (bad !== undefined) ctx.addIssue({ code: 'custom', message: `"${bad}" is not an ${kind} name.` })
  }
}

/** The editor's form: name, format (picked on create) and the options. */
export const filterProfileSchema = z.object({
  name: z.string().trim().min(1, 'Enter a name.').max(MAX_NAME, `A name has at most ${MAX_NAME} characters.`),
  // Kept a plain string: the form starts with no format picked.
  format: z.string().refine(f => (FILTER_FORMATS as readonly string[]).includes(f), 'Choose a format.'),
  options: z.object({
    inlineHTML: z.boolean(),
    sheets: z.array(z.string()).superRefine((sheets, ctx) => {
      const bad = sheets.find(s => !s.trim() || s.trim().length > MAX_SHEET_NAME)
      if (bad !== undefined) ctx.addIssue({ code: 'custom', message: `"${bad}" is not a sheet name: 1 to ${MAX_SHEET_NAME} characters.` })
    }),
    headerRow: z.boolean(),
    columns: z.array(z.string()).superRefine((columns, ctx) => {
      const bad = columns.find(c => !columnNumber(c.trim().toUpperCase()))
      if (bad !== undefined) ctx.addIssue({ code: 'custom', message: `"${bad}" is not a column letter, such as B or AA.` })
    }),
    htmlAttributes: z.array(z.object({ element: z.string(), attributes: z.array(z.string()) })).superRefine((rows, ctx) => {
      for (const row of rows) {
        const element = row.element.trim()
        if (!element && row.attributes.length === 0) continue
        if (!isMarkupName(element)) {
          ctx.addIssue({ code: 'custom', message: `"${element}" is not an element name.` })
          return
        }
        if (CONFIG_ELEMENTS.has(element.toLowerCase())) {
          ctx.addIssue({ code: 'custom', message: `The attributes of ${element} cannot be translated: it configures the page.` })
          return
        }
        const bad = row.attributes.find(a => !isMarkupName(a.trim()))
        if (bad !== undefined) {
          ctx.addIssue({ code: 'custom', message: `"${bad}" is not an attribute name.` })
          return
        }
        const unsafe = row.attributes.find(a => !isTextHTMLAttribute(element, a.trim()))
        if (unsafe !== undefined) {
          ctx.addIssue({ code: 'custom', message: `"${unsafe}" cannot be translated: only text attributes such as alt, title, placeholder, label and aria-label can, and value on an input.` })
          return
        }
      }
    }),
    metaDescription: z.boolean(),
    skipPre: z.boolean(),
    xmlInline: z.array(z.string()).superRefine(namesRefine('element')),
    xmlNoTranslate: z.array(z.string()).superRefine((markers, ctx) => {
      // An empty value would match every element without the attribute.
      const bad = markers.find(m => !isMarkupName(parseMarker(m).name) || parseMarker(m).value === '')
      if (bad !== undefined) ctx.addIssue({ code: 'custom', message: `"${bad}" is not a marker: write attribute=value, such as translate=no.` })
    }),
    xmlRefElements: z.array(z.string()).superRefine(namesRefine('element')),
    xmlRefAttributes: z.array(z.string()).superRefine(namesRefine('attribute')),
    xmlSkip: z.array(z.string()).superRefine(namesRefine('element')),
    targetColumns: z.array(z.object({ language: z.string(), column: z.string() })),
  }).superRefine((o, ctx) => {
    const problem = targetColumnsProblem(o.columns, o.targetColumns)
    if (problem) ctx.addIssue({ code: 'custom', path: ['targetColumns'], message: problem })
  }),
})

export type FilterProfileForm = z.infer<typeof filterProfileSchema>

/** By format in display order; per format the built-ins first, then by name. */
export function sortFilterProfiles<T extends { format: string; builtin: boolean; name: string }>(profiles: T[]): T[] {
  const rank = (f: string) => {
    const i = (FILTER_FORMATS as readonly string[]).indexOf(f)
    return i < 0 ? FILTER_FORMATS.length : i
  }
  return [...profiles].sort((a, b) =>
    rank(a.format) - rank(b.format) || Number(b.builtin) - Number(a.builtin) || a.name.localeCompare(b.name))
}
