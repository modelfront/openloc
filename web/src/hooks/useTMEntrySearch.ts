import { useMemo, useState } from 'react'
import { useDebounce } from './useDebounce'
import { getRegexError, searchHighlightRegex, type TextSearchOptions } from '@/lib/text-search'

const DEFAULT_TM_SEARCH: TextSearchOptions = {
  searchSource: true,
  searchTarget: true,
  exactMatch: false,
  caseSensitive: false,
  useRegex: false,
}

const regexInvalid = (text: string, o: TextSearchOptions) =>
  o.useRegex && text !== '' && getRegexError(text) !== null

/**
 * Search state of the TM browser. The text is debounced before it reaches the
 * server; the options apply at once. A regex the browser cannot compile never
 * reaches the server — `invalid` says so, and `draftInvalid` says it while
 * the user is still typing.
 */
export function useTMEntrySearch() {
  const [text, setText] = useState('')
  const [options, setOptions] = useState<TextSearchOptions>(DEFAULT_TM_SEARCH)
  const debouncedText = useDebounce(text, 300)

  const highlight = useMemo(() => searchHighlightRegex(debouncedText, options), [debouncedText, options])

  return {
    text,
    setText,
    options,
    setOptions,
    /** What the server searches for; '' lists every entry. */
    debouncedText,
    draftInvalid: regexInvalid(text, options),
    invalid: regexInvalid(debouncedText, options),
    /** Hits to tint per field — none on a field outside the scope. */
    sourceHighlight: options.searchSource ? highlight : null,
    targetHighlight: options.searchTarget ? highlight : null,
  }
}

export type TMEntrySearch = ReturnType<typeof useTMEntrySearch>
