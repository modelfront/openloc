// The file formats filter profiles exist for, in display order, with their
// labels and the extensions they cover. Kept in step with
// internal/filterprofile.Formats.

export type FilterFormat = 'html' | 'xlsx' | 'json_i18n' | 'po' | 'txt' | 'xml'

export const FILTER_FORMATS: readonly FilterFormat[] = ['html', 'xlsx', 'json_i18n', 'po', 'txt', 'xml']

export const FILTER_FORMAT_LABELS: Record<FilterFormat, string> = {
  html: 'HTML',
  xlsx: 'Excel',
  json_i18n: 'JSON',
  po: 'PO',
  txt: 'Plain text',
  xml: 'XML',
}

/** Extensions per format, for the picker's hint. */
export const FILTER_FORMAT_EXTENSIONS: Record<FilterFormat, string> = {
  html: '.html, .htm',
  xlsx: '.xlsx',
  json_i18n: '.json',
  po: '.po, .pot',
  txt: '.txt',
  xml: '.xml',
}

/** What a format's extensions leave out, where it matters. */
export const FILTER_FORMAT_NOTES: Partial<Record<FilterFormat, string>> = {
  xml: 'Not Android string resources: those are read on their own and have no filter profiles.',
}

/** The extensions with the format's note, for a one-line hint. */
export function filterFormatHint(format: FilterFormat): string {
  const note = FILTER_FORMAT_NOTES[format]
  return note ? `${FILTER_FORMAT_EXTENSIONS[format]}. ${note}` : FILTER_FORMAT_EXTENSIONS[format]
}

/** Formats whose strings may hold HTML ("Strings contain HTML"). */
export const INLINE_HTML_FORMATS: readonly FilterFormat[] = ['json_i18n', 'po', 'txt', 'xlsx']

export function isFilterFormat(v: string): v is FilterFormat {
  return (FILTER_FORMATS as readonly string[]).includes(v)
}

/** The filter format of an uploaded file's name; null when it has no filter
 *  profiles. A .xml file may be Android string resources, which have none:
 *  the server decides from the content and refuses a pick for those. */
export function filterFormatOfFilename(name: string): FilterFormat | null {
  const ext = name.toLowerCase().split('.').pop() ?? ''
  switch (ext) {
    case 'html':
    case 'htm':
      return 'html'
    case 'xlsx':
      return 'xlsx'
    case 'json':
      return 'json_i18n'
    case 'po':
    case 'pot':
      return 'po'
    case 'txt':
      return 'txt'
    case 'xml':
      return 'xml'
    default:
      return null
  }
}
