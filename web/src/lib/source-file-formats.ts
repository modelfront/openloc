/**
 * Accepted source-file extensions for project uploads.
 *
 * Must stay in sync with `FormatFromFilename` / `DetectFormat` in
 * `internal/parser/json_i18n.go` — an extension missing here is silently
 * unreachable from the UI even though the backend supports it.
 *
 * `.xml` covers both Android string resources and generic XML documents
 * (SCHEMA ST4, DocBook, DITA, in-house schemas); the backend picks the parser
 * by root element, so there's nothing to choose at upload time.
 */
export const SOURCE_FILE_EXTENSIONS = [
  '.json',
  '.po',
  '.pot',
  '.xliff',
  '.xlf',
  '.sdlxliff',
  '.mxliff',
  '.mqxliff',
  '.md',
  '.html',
  '.htm',
  '.txt',
  '.xml',
  '.strings',
  '.xlsx',
  '.docx',
  '.pptx',
  '.pdf',
] as const

/** Value for an `<input type="file">` accept attribute. */
export const SOURCE_FILE_ACCEPT = SOURCE_FILE_EXTENSIONS.join(',')
