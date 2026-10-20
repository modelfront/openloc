import { useState } from 'react'
import { useOnChange } from '@/hooks/useOnChange'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { Braces, FileText } from 'lucide-react'
import { searchDocs } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { useDebounce } from '@/hooks/useDebounce'
// eslint-disable-next-line no-restricted-imports -- a command palette, not a pick-one field; the picker rule is for Combobox use cases
import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'
import { useDocsSet } from './docs-set'

/** A search hit that is not a Markdown page, e.g. an API endpoint. */
export type ExtraSearchHit = { key: string; title: string; subtitle: string; to: string }

type Props = {
  extraHits?: (query: string) => ExtraSearchHit[]
  open: boolean
  onOpenChange: (open: boolean) => void
  apiBase: string
  routeBase: string
}

const MIN_QUERY = 2

// Docs search palette. The server ranks and role-filters, so cmdk's own
// filtering is off (`shouldFilter={false}`); picking a hit navigates to it.
export function DocsSearch({ open, onOpenChange, apiBase, routeBase, extraHits }: Props) {
  const navigate = useNavigate()
  const { copy } = useDocsSet()
  const [q, setQ] = useState('')
  const debounced = useDebounce(q.trim(), 200)
  const enabled = open && debounced.length >= MIN_QUERY

  const { data: hits = [], isFetching } = useQuery({
    queryKey: queryKeys.docs.search(apiBase, debounced),
    queryFn: () => searchDocs(debounced, apiBase),
    enabled,
    staleTime: 60_000,
  })

  useOnChange([open], () => { if (open) setQ('') })

  const extra = enabled && extraHits ? extraHits(debounced) : []

  const go = (to: string) => {
    onOpenChange(false)
    navigate(to)
  }

  return (
    <CommandDialog open={open} onOpenChange={onOpenChange} title={copy.searchTitle} description={copy.searchDescription}>
      <Command shouldFilter={false}>
        <CommandInput
          value={q}
          onValueChange={setQ}
          placeholder={copy.searchPlaceholder}
          autoFocus
          data-testid="docs-search-input"
        />
        <CommandList data-testid="docs-search-results">
          {enabled && !isFetching && hits.length === 0 && extra.length === 0 && (
            <CommandEmpty>{copy.noResults}</CommandEmpty>
          )}
          {!enabled && (
            <p className="px-3 py-6 text-center text-sm text-gray-500 dark:text-gray-400">
              Type at least {MIN_QUERY} characters.
            </p>
          )}
          {hits.map(hit => (
            <CommandItem key={hit.slug} value={hit.slug} onSelect={() => go(`${routeBase}/${hit.slug}`)} className="items-start">
              <FileText className="mt-0.5 text-gray-400" />
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-medium text-gray-900 dark:text-gray-100">
                  {hit.title}
                  <span className="ml-2 text-xs font-normal text-gray-400 dark:text-gray-500">{hit.section_title}</span>
                </span>
                <span className="mt-0.5 block truncate text-xs text-gray-500 dark:text-gray-400">{hit.snippet}</span>
              </span>
            </CommandItem>
          ))}
          {extra.map(hit => (
            <CommandItem key={hit.key} value={hit.key} onSelect={() => go(hit.to)} className="items-start">
              <Braces className="mt-0.5 text-gray-400" />
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-medium text-gray-900 dark:text-gray-100">{hit.title}</span>
                <span className="mt-0.5 block truncate font-mono text-xs text-gray-500 dark:text-gray-400">{hit.subtitle}</span>
              </span>
            </CommandItem>
          ))}
        </CommandList>
      </Command>
    </CommandDialog>
  )
}
