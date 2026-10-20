// Text-search options shared by the CAT editor's segment search and the TM
// browser's entry search, and the highlight of what a search found.

import type { DecoratedRange } from './text-ranges'
import { SEARCH_HIT_CLASS } from './text-highlight'
import { escapeRegExp } from './regex'

export interface TextSearchOptions {
  searchSource: boolean // scope: match against the source text
  searchTarget: boolean // scope: match against the target / translation
  caseSensitive: boolean
  useRegex: boolean     // treat the search text as a JS regular expression
  /** The whole text must equal the search text (or the whole regex). */
  exactMatch?: boolean
}

/** Which fields a search looks in — the "Search in" choice. */
export type SearchScope = 'both' | 'source' | 'target'

export function searchScopeOf(o: TextSearchOptions): SearchScope {
  if (o.searchSource && o.searchTarget) return 'both'
  return o.searchSource ? 'source' : 'target'
}

export function searchScopeFlags(scope: SearchScope): Pick<TextSearchOptions, 'searchSource' | 'searchTarget'> {
  return { searchSource: scope !== 'target', searchTarget: scope !== 'source' }
}

/** Whether any option differs from the default (both scopes, no flags). */
export function searchOptionsActive(o: TextSearchOptions): boolean {
  return !o.searchSource || !o.searchTarget || o.caseSensitive || o.useRegex || !!o.exactMatch
}

/** RegExp constructor error message for a pattern, or null if it compiles. */
export function getRegexError(pattern: string): string | null {
  try {
    new RegExp(pattern)
    return null
  } catch (e) {
    return e instanceof Error ? e.message : String(e)
  }
}

/**
 * A global RegExp for highlighting what a search with these options matches,
 * or null when there is nothing to highlight (no text, or an invalid pattern).
 */
export function searchHighlightRegex(text: string, opts: TextSearchOptions): RegExp | null {
  if (text === '') return null
  let source = opts.useRegex ? text : escapeRegExp(text)
  if (opts.exactMatch) source = `^(?:${source})$`
  try {
    return new RegExp(source, opts.caseSensitive ? 'g' : 'gi')
  } catch {
    return null
  }
}

/** Ranges over `text` for every non-empty match of `re`, painted as search hits. */
export function searchRanges(text: string, re: RegExp): DecoratedRange[] {
  const out: DecoratedRange[] = []
  // matchAll needs the g flag and advances past empty matches on its own.
  const global = re.global ? re : new RegExp(re.source, `${re.flags}g`)
  for (const m of text.matchAll(global)) {
    if (m[0] === '') continue
    out.push({ start: m.index, end: m.index + m[0].length, className: SEARCH_HIT_CLASS })
  }
  return out
}
