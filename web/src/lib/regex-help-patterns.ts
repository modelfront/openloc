// Reference card for the Regex search option. Every claim here must match what
// the searches actually do: patterns are tested as "contains", not "equals",
// the Case sensitive checkbox supplies the case flag (no slashes/flags in the
// pattern), and scopes are OR'd.
//
// Each row carries a `sample` it must match. The segment search runs patterns
// as JavaScript (lib/segment-filters.ts, pinned by RegexSearchHelp.test.tsx);
// the TM search runs them in Postgres (pinned by
// internal/translationmemory/entry_search_integration_test.go, which parses
// this file — keep one row per line and single-quoted strings).
// `segmentsOnly` rows have no meaning in a TM, where neither side is empty.
export interface RegexHelpPattern {
  pattern: string
  desc: string
  sample: string
  segmentsOnly?: boolean
}

export const REGEX_HELP_PATTERNS: RegexHelpPattern[] = [
  { pattern: 'cancel|abort', desc: 'either word', sample: 'Press to cancel the upload' },
  { pattern: '^Please', desc: 'segments that start with "Please" (^ = start, $ = end)', sample: 'Please try again' },
  { pattern: '\\.$', desc: 'segments ending in a period', sample: 'The job is done.' },
  { pattern: '^.{0,10}$', desc: 'segments 10 characters or shorter', sample: 'Short one' },
  { pattern: '^$', desc: 'empty text (Search in Translation → finds untranslated)', sample: '', segmentsOnly: true },
  { pattern: '\\{[a-z_]+\\}', desc: 'placeholders like {name} or {user_id}', sample: 'Welcome, {user_name}' },
  { pattern: '%[sd]|%\\d+\\$s', desc: 'printf placeholders (%s, %d, %1$s)', sample: 'Deleted %s files' },
  { pattern: '\\d{2,}', desc: 'numbers with 2+ digits', sample: 'Retry in 30 seconds' },
  { pattern: '\\d+[.,]\\d+', desc: 'decimals with either separator', sample: 'Total: 1,50 EUR' },
  { pattern: '\\bserver\\b', desc: 'whole word only (not "servers" or "observer")', sample: 'The server is down' },
  { pattern: 'TM\\s+match', desc: 'words with any whitespace between them', sample: 'a 100% TM  match' },
  { pattern: '  +', desc: 'double spaces (QA catch)', sample: 'double  space' },
  { pattern: '[“”«»]', desc: 'typographic quotes', sample: 'He said “hello”' },
  { pattern: '(?=.*password)(?=.*reset)', desc: 'both words, any order', sample: 'reset your password' },
]

/** The rows that hold for a TM search. */
export const TM_REGEX_HELP_PATTERNS = REGEX_HELP_PATTERNS.filter(p => !p.segmentsOnly)
