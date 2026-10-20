import { useEffect, useMemo, useRef, useState } from 'react'
import { useOnChange } from '@/hooks/useOnChange'
import { Check, Search } from 'lucide-react'
import { useInfiniteQuery } from '@tanstack/react-query'
import { listTemplatesPage } from '../../api'
import { formatDate } from '../../utils'
import { queryKeys } from '../../lib/query-keys'
import { useDebounce } from '../../hooks/useDebounce'
import { cn } from '@/lib/utils'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { TextLink } from '@/components/ui/text-link'

/** Templates fetched per request. Small enough that the first page paints fast. */
const PAGE_SIZE = 20

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Id of the template the form is already based on, if any. */
  selectedId?: string
  onPick: (id: string) => void
}

/**
 * The "From template" step of the New project page: one row per template,
 * click to base the new project on it. Picking closes the dialog; closing it
 * without picking is the caller's cue to fall back to the previous choice.
 *
 * The list is searched and paged on the server, and the next page loads as the
 * user scrolls, so a tenant with thousands of templates opens as fast as one
 * with three.
 */
export function TemplatePickerDialog({ open, onOpenChange, selectedId, onPick }: Props) {
  const [search, setSearch] = useState('')
  const debouncedSearch = useDebounce(search.trim(), 300)

  const { data, isLoading, isError, error, refetch, fetchNextPage, hasNextPage, isFetchingNextPage } = useInfiniteQuery({
    queryKey: [...queryKeys.templates.paginated, debouncedSearch] as const,
    queryFn: ({ pageParam }) => listTemplatesPage(debouncedSearch, pageParam, PAGE_SIZE),
    initialPageParam: 0,
    getNextPageParam: page => (page.has_more ? page.cursor : undefined),
    enabled: open,
  })
  const templates = useMemo(() => data?.pages.flatMap(p => p.data) ?? [], [data])

  // Infinite scroll: a sentinel below the last row, watched inside the list's
  // own scroll box. Rows are a fixed height here, so the sentinel sits where it
  // looks like it does — unlike in VirtualDataTable, where measured rows make a
  // bottom-pinned sentinel unreliable.
  const listRef = useRef<HTMLUListElement>(null)
  const sentinelRef = useRef<HTMLLIElement>(null)
  useEffect(() => {
    const sentinel = sentinelRef.current
    if (!sentinel || !hasNextPage || isFetchingNextPage) return
    const observer = new IntersectionObserver(
      entries => { if (entries.some(e => e.isIntersecting)) void fetchNextPage() },
      { root: listRef.current, rootMargin: '120px' },
    )
    observer.observe(sentinel)
    return () => observer.disconnect()
  }, [hasNextPage, isFetchingNextPage, fetchNextPage, templates.length])

  // A reopened dialog starts from a clean search rather than the last one.
  useOnChange([open], () => { if (!open) setSearch('') })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md" data-testid="template-picker-dialog">
        <DialogHeader>
          <DialogTitle>Choose a template</DialogTitle>
          <DialogDescription>
            The template fills in the languages, workflow and settings. You can change everything after.
          </DialogDescription>
        </DialogHeader>

        <Input
          value={search}
          onChange={e => setSearch(e.target.value)}
          placeholder="Search templates"
          icon={<Search className="size-3.5" />}
          aria-label="Search templates"
          data-testid="template-picker-search"
        />

        {isLoading ? (
          <LoadingPlaceholder />
        ) : isError && !data ? (
          <LoadFailed what="templates" error={error} onRetry={() => refetch()} />
        ) : templates.length === 0 ? (
          <p className="text-sm text-gray-500 dark:text-gray-400" data-testid="template-picker-empty">
            {debouncedSearch ? (
              <>No templates match “{debouncedSearch}”.</>
            ) : (
              <>
                No templates yet. Create one on the{' '}
                <TextLink to="/templates">Templates</TextLink> page.
              </>
            )}
          </p>
        ) : (
          <ul ref={listRef} className="max-h-[60vh] space-y-1 overflow-y-auto -mx-1 px-1">
            {templates.map(t => {
              const selected = String(t.id) === selectedId
              return (
                <li key={t.id}>
                  {/* eslint-disable-next-line no-restricted-syntax -- a selectable row, not an action button */}
                  <button
                    type="button"
                    onClick={() => onPick(String(t.id))}
                    aria-current={selected ? 'true' : undefined}
                    data-testid={`template-option-${t.id}`}
                    className={cn(
                      'flex w-full items-center justify-between gap-3 rounded-md border px-3 py-2 text-left transition-colors',
                      'hover:border-primary focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none',
                      selected ? 'border-primary bg-primary/5' : 'border-gray-200 dark:border-gray-800',
                    )}
                  >
                    <span className="min-w-0">
                      <span className="block truncate text-sm font-medium text-gray-900 dark:text-gray-100">{t.name}</span>
                      <span className="block text-xs text-gray-400 dark:text-gray-500">
                        {t.workflow_name ? `${t.workflow_name} workflow` : 'Custom workflow'} · Created {formatDate(t.created_at)}
                      </span>
                    </span>
                    {selected && <Check className="size-4 shrink-0 text-primary" aria-hidden="true" />}
                  </button>
                </li>
              )
            })}
            <li ref={sentinelRef} aria-hidden="true" data-testid="template-picker-sentinel" />
            {isFetchingNextPage && (
              <li className="py-2 text-center text-xs text-gray-400 dark:text-gray-500" data-testid="template-picker-loading-more">
                Loading…
              </li>
            )}
          </ul>
        )}
      </DialogContent>
    </Dialog>
  )
}
