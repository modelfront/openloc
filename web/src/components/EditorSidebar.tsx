import { useState, useEffect, useMemo, memo, type ReactNode } from 'react'
import { useQuery, keepPreviousData } from '@tanstack/react-query'
import { getMTPreview, type TermLookupResult } from '../api'
import { queryKeys } from '../lib/query-keys'
import { useTMMatches, TM_LOOKUP_DEBOUNCE_MS } from '../hooks/useTMMatches'
import { isTermApplied } from '../lib/cat-editor-logic'
import { countWordOccurrences } from '../lib/term-match'
import { PanelRightClose, Check, AlertTriangle, ArrowRight } from 'lucide-react'
import CommentPanel from './CommentPanel'
import QAFindingsSection from './QAFindingsSection'
import { EditorSidebarTMSection } from './EditorSidebarTMSection'
import { EditorSidebarCustomizer } from './EditorSidebarCustomizer'
import { EditorSidebarSection } from './EditorSidebarSection'
import SegmentHistorySection from './SegmentHistorySection'
import { qaIssuesEqual, type QaFinding } from '../lib/qa-findings'
import type { PanelInsert } from '../lib/panel-insert'
import { SegmentText } from './SegmentRow.shared'
import { Button } from '@/components/ui/button'
import { Chip } from '@/components/ui/chip'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { TERM_STATUS_COLOR, TERM_STATUS_ICON, termStatusLabel } from '@/lib/term-status'
import { isSectionVisible, loadSidebarLayout, saveSidebarLayout, visibleSections, type SidebarLayout, type SidebarSectionId } from '@/lib/sidebar-sections'

/** Stable identity for the "no findings" case, so the memo comparator and the
 *  section below don't see a fresh array on every render. */
const EMPTY_ISSUES: QaFinding[] = []

/** Icon-only term status chip; the label is in the tooltip and for screen readers.
 *  An applied term shows a check instead of its status icon — the color still carries the status. */
function TermStatusChip({ status, applied }: { status: string; applied: boolean }) {
  const label = applied ? `${termStatusLabel(status)} · applied` : termStatusLabel(status)
  const Icon = applied ? Check : TERM_STATUS_ICON[status]
  const color = TERM_STATUS_COLOR[status] ?? 'grey'
  if (!Icon) return <Chip size="small" color={color}>{label}</Chip>
  return (
    <TooltipProvider delay={200}>
      <Tooltip>
        <TooltipTrigger render={<span className="inline-flex" tabIndex={0} />}>
          <Chip size="small" color={color} icon={<Icon />}>
            <span className="sr-only">{label}</span>
          </Chip>
        </TooltipTrigger>
        <TooltipContent side="top">{label}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}

/** One cross-reference in the active segment: the term the chip
 *  renders and the row that defines it. Built by CATEditor from refTerms. */
export interface SegmentReference {
  refKey: string
  /** Source term of the defining segment ("Wet"). */
  source: string
  /** Its current translation; '' while untranslated. */
  target: string
  /** 1-based row number of the defining segment, as shown in the # column. */
  segmentNumber: number | null
}

interface Props {
  jobId: number
  sourceText: string
  editText: string
  terms: TermLookupResult[]
  /** Cross-references in the active segment; section hidden when empty. */
  references?: SegmentReference[]
  /** A TM match (the editor re-anchors its tags onto the current source) or
   *  an MT suggestion, each with the insert token that records it. */
  onInsertMatch: (insert: PanelInsert) => void
  onTermClick: (sourceTerm: string, targetTerm: string, mtTranslation?: string) => void
  onTermHover?: (sourceTerm: string, targetTerm: string, mtTranslation?: string) => void
  /** Jump to (scroll + activate) the segment a reference points at. */
  onJumpToReference?: (refKey: string) => void
  /** The active segment's stored QA findings; empty when none are active. */
  qaIssues?: QaFinding[]
  /** Receives the segment's fresh finding list after ignore / un-ignore. */
  onQARefresh?: (translationId: number, issues: QaFinding[]) => void
  onClose: () => void
  /** Reports the TM hit the user is pointing at (null on leave), so the editor
   *  can re-diff the segment's source against that hit instead of the top one. */
  onMatchHover?: (sourceText: string | null) => void
  translationId?: number | null
  /** The file's strings are plain text: MT inserts its plain text, never tags. */
  plainText?: boolean
}

export function editorSidebarEqual(prev: Props, next: Props): boolean {
  // Compare terms by reference, not length. Parent allocates a new
  // array on every term-lookup result, so a length-only check skips re-renders
  // when switching A→B where both have the same count of (different) terms.
  return (
    prev.jobId === next.jobId &&
    prev.sourceText === next.sourceText &&
    prev.editText === next.editText &&
    prev.terms === next.terms &&
    prev.references === next.references &&
    prev.translationId === next.translationId &&
    prev.onMatchHover === next.onMatchHover &&
    prev.plainText === next.plainText &&
    // Structural, not by reference: an ignore flip rewrites the array in place
    // of the same length, and that flip is exactly what the section re-renders
    // for. Reference equality would also miss it — the parent rebuilds the
    // array from the report cache on every patch.
    qaIssuesEqual(prev.qaIssues ?? EMPTY_ISSUES, next.qaIssues ?? EMPTY_ISSUES)
  )
}

export default memo(function EditorSidebar({
  jobId, sourceText, editText, terms, references, qaIssues,
  onInsertMatch, onTermClick, onTermHover, onJumpToReference, onQARefresh, onClose, onMatchHover, translationId, plainText = false,
}: Props) {
  const [mtOpen, setMtOpen] = useState(true)
  const [termOpen, setTermOpen] = useState(true)
  const [refsOpen, setRefsOpen] = useState(true)
  const [qaOpen, setQaOpen] = useState(true)
  const [historyOpen, setHistoryOpen] = useState(true)
  const [commentsOpen, setCommentsOpen] = useState(true)

  // Section order + visibility, persisted next to the panel's open/closed flag.
  const [layout, setLayout] = useState<SidebarLayout>(loadSidebarLayout)
  const updateLayout = (next: SidebarLayout) => {
    setLayout(next)
    saveSidebarLayout(next)
  }

  // Source + translation id are debounced as a PAIR: they change together on
  // segment switch, and debouncing them separately would fire an MT preview
  // request with the new segment's id against the old segment's source.
  const [debounced, setDebounced] = useState({ source: sourceText, translationId: translationId ?? null })
  useEffect(() => {
    const timer = setTimeout(() => setDebounced({ source: sourceText, translationId: translationId ?? null }), TM_LOOKUP_DEBOUNCE_MS)
    return () => clearTimeout(timer)
  }, [sourceText, translationId])
  const debouncedSource = debounced.source

  // A source under 3 chars isn't worth an engine round trip. useTMMatches
  // applies the same gate, and owns the note on why a gated query must blank
  // its data.
  const resourcesEnabled = debouncedSource.length > 2

  // TM matches — the same hook CATEditor uses to diff the segment's source
  // against the top hit, so both land on one query key and one request.
  const { matches, isFetching: tmFetching, stale: matchesStale } = useTMMatches(jobId, sourceText, translationId ?? null)

  // MT preview query — reuses the pipeline's stored MT when the segment has
  // one (translation_id → translation_history kind='mt' lookup server-side);
  // only segments with no MT on record live-call the engine. Cached 5 min so
  // revisits don't re-fetch. `retry: false` avoids hammering a flaky engine.
  // The user-facing label below is still "MT suggestion" (facade); the
  // endpoint + query key are mt-preview.
  const { data: rawMt, isFetching: mtFetching, isError: mtError, isPlaceholderData: mtPlaceholder } = useQuery({
    queryKey: queryKeys.mtPreview.byJob(jobId, debounced.translationId, debouncedSource),
    queryFn: () => getMTPreview(jobId, debouncedSource, debounced.translationId),
    // Off while the section is hidden: with no stored MT this is a live engine call.
    enabled: resourcesEnabled && isSectionVisible(layout, 'mt'),
    staleTime: 5 * 60_000,
    placeholderData: keepPreviousData,
    retry: false,
  })
  const mtData = resourcesEnabled ? rawMt : undefined
  // The previous segment's suggestion stays on screen while this one loads;
  // its insert token belongs to that segment, so a click on it inserts nothing.
  const mtStale = mtPlaceholder || debounced.source !== sourceText || debounced.translationId !== (translationId ?? null)

  // Defensive dedupe by term id. The list below keys on `term.id`; if the same
  // id ever appears twice (e.g. a lookup that multiplied rows across assignment
  // references), duplicate React keys break reconciliation and stale nodes from
  // a previously-active segment stick around ("poison" the list) instead of
  // being replaced. The backend lookup dedupes at the source (LATERAL/EXISTS
  // semi-join), but this guarantees unique keys regardless of what arrives.
  const displayTerms = useMemo(() => {
    const seen = new Set<number>()
    return terms.filter(t => (seen.has(t.id) ? false : (seen.add(t.id), true)))
  }, [terms])

  const isMac = navigator.platform.includes('Mac')
  const modKey = isMac ? '⌘' : 'Ctrl'

  // Every section as a node, keyed by id; the render loop below picks them in
  // the user's order and skips the hidden ones. References returns null when
  // the active segment has none, whatever the layout says.
  const sectionNodes: Record<SidebarSectionId, ReactNode> = {
    tm: (
      <EditorSidebarTMSection
        matches={matches}
        fetching={tmFetching}
        stale={matchesStale}
        sourceText={sourceText}
        onInsertMatch={onInsertMatch}
        onMatchHover={onMatchHover}
        modKey={modKey}
      />
    ),

    // MT suggestion — live fetch from the project's MT engine. Always shown,
    // even when TM filled the cell, so the user can swap in MT with one click.
    mt: (
        <EditorSidebarSection
          data-testid="mt-suggestion-section"
          title="MT suggestion"
          open={mtOpen}
          onToggle={() => setMtOpen(v => !v)}
          meta={mtData?.engine && <span className="block text-[9px] truncate max-w-[100px]">{mtData.engine}</span>}
          bodyClassName={`transition-opacity duration-200 ${mtFetching || mtStale ? 'opacity-50' : 'opacity-100'}`}
        >
          {!debouncedSource && (
            <p className="text-[10px] text-gray-400 px-1">Select a segment</p>
          )}
          {debouncedSource && mtError && (
            <p className="text-[10px] text-gray-400 px-1">No MT available</p>
          )}
          {debouncedSource && !mtError && mtData?.text && (
            <div
              data-testid="mt-suggestion"
              className={`rounded border border-primary/40 bg-primary/10 text-primary px-2 py-1.5 ${mtStale ? 'cursor-default' : 'cursor-pointer hover:ring-1 hover:ring-ring'}`}
              // Insert the rich form when the suggestion carries tags, so
              // the editor parses them into chips instead of dropping them.
              // Unlike a TM hit this needs no tag remap: the engine placed
              // these tags in THIS segment's source, so the ids already
              // match — and a remap would reject a legitimately split pair.
              onClick={() => { if (!mtStale) onInsertMatch({ kind: 'mt', text: plainText ? mtData.text : mtData.rich_text || mtData.text, insertToken: mtData.insert_token }) }}
              title={mtStale ? 'Loading the suggestion for this segment…' : 'Click to insert MT suggestion'}
            >
              <div className="text-[11px] font-medium">
                <SegmentText text={mtData.text} richText={mtData.rich_text} />
              </div>
            </div>
          )}
          {debouncedSource && !mtError && !mtFetching && !mtData?.text && (
            <p className="text-[10px] text-gray-400 px-1">No MT available</p>
          )}
        </EditorSidebarSection>
    ),

    terminology: (
        <EditorSidebarSection
          data-testid="terminology-section"
          title="Terminology"
          open={termOpen}
          onToggle={() => setTermOpen(v => !v)}
          meta={displayTerms.length}
          bodyClassName="space-y-0.5"
        >
          {displayTerms.length === 0 && (
            <p className="text-[10px] text-gray-400 px-1">No terms</p>
          )}
          {displayTerms.map(term => {
            // Forbidden terms invert the whole "applied" concept: you never
            // WANT a forbidden term in the target, so there's no green ✓ and
            // no click-to-insert. Instead, if the banned target term is
            // actually present we flag it as a violation (red ⚠). Approved /
            // pending terms keep the insert + ✓ behaviour.
            const isForbidden = term.status === 'forbidden'
            const forbiddenPresent = isForbidden && countWordOccurrences(editText, term.target_term) > 0
            const applied = !isForbidden && isTermApplied(sourceText, editText, term.source_term, term.target_term)
            const interactive = !isForbidden && !applied
            return (
              <div
                key={term.id}
                className={`flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs rounded px-1.5 py-1 ${
                  interactive
                    ? 'group/term cursor-pointer hover:bg-primary/10'
                    : 'cursor-default'
                }`}
                onMouseDown={interactive ? (e) => { e.preventDefault(); onTermClick(term.source_term, term.target_term, term.mt_translation) } : undefined}
                onMouseEnter={interactive ? () => onTermHover?.(term.source_term, term.target_term, term.mt_translation) : undefined}
                title={
                  isForbidden
                    ? (forbiddenPresent ? 'Forbidden term is used in the translation — replace it' : 'Forbidden — do not use this term')
                    : (applied ? 'Already applied' : 'Click to replace selection')
                }
              >
                <span className={`font-mono min-w-0 wrap-anywhere ${interactive ? 'text-primary' : 'text-gray-700 dark:text-gray-300'}`}>{term.source_term}</span>
                <span className="text-gray-400 dark:text-gray-500">&rarr;</span>
                <span className="font-semibold min-w-0 wrap-anywhere text-gray-900 dark:text-gray-100">{term.target_term}</span>
                {!applied && term.notes && <span className="text-gray-400 dark:text-gray-500 truncate max-w-24">{term.notes}</span>}
                <span className="flex items-center gap-1">
                  <TermStatusChip status={term.status} applied={applied} />
                  {forbiddenPresent && <AlertTriangle className="w-3 h-3 text-red-600 dark:text-red-400" aria-label="forbidden term in use" />}
                  {term.domain && (
                    <Chip size="small" title={`Domain: ${term.domain}`}>{term.domain}</Chip>
                  )}
                </span>
              </div>
            )
          })}
        </EditorSidebarSection>
    ),

    // References — only when the active segment has
    // cross-reference chips. Each entry is the term a chip renders plus the
    // row that defines it; clicking jumps there (same navigation as clicking
    // the chip itself, but discoverable without hovering).
    references: references && references.length > 0 && (
          <EditorSidebarSection
            data-testid="references-section"
            title="References"
            open={refsOpen}
            onToggle={() => setRefsOpen(v => !v)}
            meta={references.length}
            bodyClassName="space-y-0.5"
          >
            {references.map(ref => (
              // eslint-disable-next-line no-restricted-syntax -- whole-row navigation target (jumps to the defining segment), not an action button
              <button
                key={ref.refKey}
                onClick={() => onJumpToReference?.(ref.refKey)}
                className="group/ref flex items-center gap-2 w-full text-left text-xs rounded px-1.5 py-1 cursor-pointer hover:bg-primary/10"
                title={`This reference renders segment ${ref.segmentNumber ?? '?'}'s translation — click to go there`}
              >
                <span className="font-mono text-primary">{`{${ref.source}}`}</span>
                <span className="text-gray-400 dark:text-gray-500">&rarr;</span>
                {ref.target
                  ? <span className="font-semibold text-gray-900 dark:text-gray-100 truncate">{ref.target}</span>
                  : <span className="italic text-gray-400 dark:text-gray-500">untranslated</span>}
                <span className="ml-auto shrink-0 text-gray-400 group-hover/ref:text-primary">
                  #{ref.segmentNumber ?? '?'}
                  <ArrowRight className="inline w-3 h-3 ml-0.5 align-[-2px]" aria-hidden="true" />
                </span>
              </button>
            ))}
          </EditorSidebarSection>
    ),

    // Where the current translation came from — the last of the "provenance"
    // sections (TM / MT / Terminology / References) by default, above the two
    // that comment on what's already written.
    history: (
        <SegmentHistorySection
          translationId={translationId ?? null}
          open={historyOpen}
          onOpenChange={setHistoryOpen}
        />
    ),

    // QA findings for the active segment. Defaults below TM / MT / Terminology:
    // those three are things to insert, this is feedback on what is written.
    qa: (
        <QAFindingsSection
          issues={qaIssues ?? EMPTY_ISSUES}
          translationId={translationId ?? null}
          open={qaOpen}
          onOpenChange={setQaOpen}
          onRefresh={onQARefresh}
        />
    ),

    comments: (
        <EditorSidebarSection
          data-testid="comments-section"
          title="Comments"
          open={commentsOpen}
          onToggle={() => setCommentsOpen(v => !v)}
          bodyClassName="px-3 pb-3"
        >
          {/* Keyed by translation: the panel holds an unsent draft in local
              state, and a draft typed against one segment must not survive
              into the next one — it would post onto the wrong segment. */}
          <CommentPanel key={translationId ?? 'none'} translationId={translationId ?? null} />
        </EditorSidebarSection>
    ),
  }

  return (
    <div data-testid="editor-sidebar" data-guide="editor.sidebar" className="flex flex-col h-full bg-card border-l border-border overflow-hidden">
      {/* Header */}
      <div className="flex items-center justify-between px-3 py-1.5 border-b border-border bg-muted">
        <span className="text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Resources</span>
        <div className="flex items-center">
          <EditorSidebarCustomizer layout={layout} onChange={updateLayout} />
          <Button variant="ghost" size="icon-xs" onClick={onClose} title="Close sidebar">
            <PanelRightClose className="w-3.5 h-3.5 text-gray-400" />
          </Button>
        </div>
      </div>

      {/* divide-y draws the separators, so a section carries no border of its
          own and the lines stay right whatever order the user picks. */}
      {/* pb-16 clears the floating chat launcher (fixed bottom-5, h-11 — 64px
          of the bottom-right corner). The sidebar is the rightmost column, so
          without it the last section's controls sit under the launcher and
          cannot be clicked. */}
      <div className="flex-1 overflow-y-scroll pb-16 divide-y divide-gray-200 dark:divide-gray-700">
        {visibleSections(layout).map(id => {
          const node = sectionNodes[id]
          return node ? <div key={id} data-sidebar-section={id}>{node}</div> : null
        })}
      </div>
    </div>
  )
}, editorSidebarEqual)
