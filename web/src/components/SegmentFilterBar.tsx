import { useState, type ReactNode } from 'react'
import { MessageSquare, Search } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { VerticalDivider } from '@/components/ui/vertical-divider'
import { modKey } from './SegmentRow'
import { SearchOptionsPanel, SearchOptionsToggle } from './SearchOptions'
import { type SegmentFilters, DEFAULT_FILTERS, isFilterActive } from '../lib/segment-filters'
import { getRegexError, searchOptionsActive } from '../lib/text-search'

const STATUS_OPTIONS = [
  { value: 'untranslated', label: 'Untranslated' },
  { value: 'pre_translated', label: 'Pretranslated' },
  { value: 'needs_review', label: 'Draft' },
  { value: 'approved', label: 'Approved' },
  { value: 'auto_approved', label: 'Auto-approved' },
]

// Origin is the sticky automation-lineage value (tm | mt | ape |
// file_import). Human edits and propagation never set it (it stays the last
// automation source).
const ORIGIN_OPTIONS = [
  { value: 'tm', label: 'TM' },
  { value: 'mt', label: 'MT' },
  { value: 'ape', label: 'APE' },
  { value: 'file_import', label: 'File import' },
]

const QA_OPTIONS = [
  { value: 'all' as const, label: 'All' },
  { value: 'has_errors' as const, label: 'Has errors' },
  { value: 'has_warnings' as const, label: 'Has warnings' },
  { value: 'no_issues' as const, label: 'No issues' },
  { value: 'ignored' as const, label: 'Ignored' },
]

interface Props {
  filters: SegmentFilters
  onChange: (filters: SegmentFilters) => void
  totalCount: number
  filteredCount: number
  /** Comment counts failed to load — the pill is disabled rather than
   *  matching zero rows, which would read as "nothing has comments". */
  commentCountsUnavailable?: boolean
  /** The stored QA report failed to load — the pills are disabled rather than
   *  matching every row (No issues) or none, which would read as "job is clean". */
  qaReportUnavailable?: boolean
  onOpenFindReplace?: () => void
  /** Segments per status (countByDisplayStatus), shown in brackets on each pill. */
  statusCounts?: Record<string, number>
  /** View controls at the right end of the row (the editor's ¶ toggle and
   *  shortcuts button). */
  actions?: ReactNode
}

export default function SegmentFilterBar({ filters, onChange, totalCount, filteredCount, commentCountsUnavailable, qaReportUnavailable, onOpenFindReplace, statusCounts, actions }: Props) {
  const [searchDraft, setSearchDraft] = useState(filters.searchText)
  // View state, not filter state — deliberately survives the Clear link.
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const active = isFilterActive(filters)
  const advancedActive = searchOptionsActive(filters)
  // Validate the draft (not the committed text) so the error shows while typing.
  const regexError = filters.useRegex && searchDraft.trim() !== '' ? getRegexError(searchDraft) : null

  const toggleStatus = (status: string) => {
    const next = filters.statuses.includes(status)
      ? filters.statuses.filter(s => s !== status)
      : [...filters.statuses, status]
    onChange({ ...filters, statuses: next })
  }

  const toggleOrigin = (origin: string) => {
    const next = filters.origins.includes(origin)
      ? filters.origins.filter(o => o !== origin)
      : [...filters.origins, origin]
    onChange({ ...filters, origins: next })
  }

  const setQAFilter = (qa: SegmentFilters['qaFilter']) => {
    onChange({ ...filters, qaFilter: qa === filters.qaFilter ? 'all' : qa })
  }

  const commitSearch = () => {
    // Skip the no-op commit: a fresh filters object re-runs filterSegments
    // over the whole virtualized grid, so plain focus-then-blur shouldn't pay.
    if (searchDraft !== filters.searchText) onChange({ ...filters, searchText: searchDraft })
  }

  const clearSearch = () => {
    setSearchDraft('')
    onChange({ ...filters, searchText: '' })
  }

  // Scope/case/regex checkboxes commit immediately — only the text keeps the
  // Enter/blur draft-commit model.
  const setFlag = (patch: Partial<SegmentFilters>) => onChange({ ...filters, ...patch })

  return (
    <div className="mb-2" data-guide="editor.filters">
      <div className="flex items-center gap-2 flex-wrap">
        {/* Search */}
        <Input
          value={searchDraft}
          onChange={e => setSearchDraft(e.target.value)}
          onKeyDown={e => { if (e.key === 'Enter') commitSearch() }}
          onBlur={commitSearch}
          placeholder="Search segments..."
          className="w-56 h-7"
          inputClassName="text-xs"
          data-testid="segment-search"
          icon={<Search className="size-3.5" />}
          // Clearing has to commit the empty filter too, not just blank the
          // draft the way the built-in clear would.
          onClear={clearSearch}
          action={
            <SearchOptionsToggle
              open={advancedOpen}
              active={advancedActive}
              onToggle={() => setAdvancedOpen(o => !o)}
              testIdPrefix="segment-search"
            />
          }
        />

      {/* Find & Replace lives next to search — both are "locate text in
          segments" actions, unlike the job-level actions in the top toolbar. */}
      {onOpenFindReplace && (
        <Tooltip>
          <TooltipTrigger render={<Button variant="outline" size="sm" onClick={onOpenFindReplace} className="h-7 text-xs" />}>
            Find and replace
          </TooltipTrigger>
          <TooltipContent>{modKey}+H — Find and replace across all segments</TooltipContent>
        </Tooltip>
      )}

      {/* Status pills */}
      <div className="flex gap-0.5">
        {STATUS_OPTIONS.map(opt => (
          // eslint-disable-next-line no-restricted-syntax -- status filter chip toggle, not an action button
          <button
            key={opt.value}
            onClick={() => toggleStatus(opt.value)}
            className={`px-1.5 py-0.5 text-[10px] rounded border cursor-pointer transition-colors ${
              filters.statuses.includes(opt.value)
                ? 'bg-blue-100 dark:bg-blue-900 border-blue-300 dark:border-blue-600 text-blue-700 dark:text-blue-300'
                : 'bg-white dark:bg-gray-800 border-gray-200 dark:border-gray-700 text-gray-500 dark:text-gray-400 hover:bg-gray-50 dark:hover:bg-gray-700'
            }`}
          >
            {opt.label}{statusCounts && ` (${(statusCounts[opt.value] ?? 0).toLocaleString()})`}
          </button>
        ))}
      </div>

      <VerticalDivider />

      {/* Origin pills */}
      <div className="flex gap-0.5">
        {ORIGIN_OPTIONS.map(opt => (
          // eslint-disable-next-line no-restricted-syntax -- origin filter chip toggle, not an action button
          <button
            key={opt.value}
            onClick={() => toggleOrigin(opt.value)}
            className={`px-1.5 py-0.5 text-[10px] rounded border cursor-pointer transition-colors ${
              filters.origins.includes(opt.value)
                ? 'bg-purple-100 dark:bg-purple-900 border-purple-300 dark:border-purple-600 text-purple-700 dark:text-purple-300'
                : 'bg-white dark:bg-gray-800 border-gray-200 dark:border-gray-700 text-gray-500 dark:text-gray-400 hover:bg-gray-50 dark:hover:bg-gray-700'
            }`}
          >
            {opt.label}
          </button>
        ))}
      </div>

      <VerticalDivider />

      {/* QA filter */}
      <div className="flex gap-0.5">
        {QA_OPTIONS.filter(o => o.value !== 'all').map(opt => (
          // eslint-disable-next-line no-restricted-syntax -- QA filter chip toggle, not an action button
          <button
            key={opt.value}
            onClick={() => setQAFilter(opt.value)}
            disabled={qaReportUnavailable}
            title={qaReportUnavailable
              ? "QA findings couldn't be loaded — reload to try again"
              : undefined}
            className={`px-1.5 py-0.5 text-[10px] rounded border transition-colors disabled:opacity-50 disabled:cursor-not-allowed ${
              qaReportUnavailable ? '' : 'cursor-pointer'
            } ${
              filters.qaFilter === opt.value
                ? 'bg-red-100 dark:bg-red-900 border-red-300 dark:border-red-600 text-red-700 dark:text-red-300'
                : 'bg-white dark:bg-gray-800 border-gray-200 dark:border-gray-700 text-gray-500 dark:text-gray-400 hover:bg-gray-50 dark:hover:bg-gray-700'
            }`}
          >
            {opt.label}
          </button>
        ))}
      </div>

      <VerticalDivider />

      {/* Comment filter — same blue as the segment-row comment indicator (the
          Approved-chip palette) so the pill and the badge read as one feature. */}
      {/* eslint-disable-next-line no-restricted-syntax -- comment filter toggle, not an action button */}
      <button
        onClick={() => onChange({ ...filters, hasComments: !filters.hasComments })}
        data-testid="segment-filter-comments"
        aria-pressed={filters.hasComments}
        disabled={commentCountsUnavailable}
        title={commentCountsUnavailable
          ? "Comment counts couldn't be loaded — reload to try again"
          : 'Show only segments with comments'}
        className={`flex items-center gap-1 px-1.5 py-0.5 text-[10px] rounded border transition-colors disabled:opacity-50 disabled:cursor-not-allowed ${
          commentCountsUnavailable ? '' : 'cursor-pointer'
        } ${
          filters.hasComments
            ? 'bg-blue-100 dark:bg-blue-900 border-blue-300 dark:border-blue-600 text-blue-700 dark:text-blue-300'
            : 'bg-white dark:bg-gray-800 border-gray-200 dark:border-gray-700 text-gray-500 dark:text-gray-400 hover:bg-gray-50 dark:hover:bg-gray-700'
        }`}
      >
        <MessageSquare className="size-3" />
        Commented
      </button>

      {/* Filter count + clear */}
      {active && (
        <div className="flex items-center gap-1">
          <Badge variant="outline" className="text-[10px] h-5 px-1.5">
            {filteredCount} / {totalCount}
          </Badge>
          {/* eslint-disable-next-line no-restricted-syntax -- compact clear-filters control in the filter bar, not a standard action button */}
          <button
            onClick={() => { onChange(DEFAULT_FILTERS); setSearchDraft('') }}
            className="text-[10px] text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 cursor-pointer"
            data-testid="segment-filter-clear"
          >
            Clear
          </button>
        </div>
      )}

      {/* ml-auto: the right end of the row — of its last line when it wraps. */}
      {actions && <div className="ml-auto flex items-center gap-2">{actions}</div>}
      </div>

      <SearchOptionsPanel
        open={advancedOpen}
        options={filters}
        onChange={setFlag}
        targetLabel="Translation"
        regexError={regexError ? 'Invalid regular expression' : null}
        regexHelp="segments"
        testIdPrefix="segment-search"
      />
    </div>
  )
}
