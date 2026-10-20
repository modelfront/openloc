import { useState, useEffect, useCallback, useRef } from 'react'
import { concordanceSearch, type ConcordanceResult, type TermConcordanceResult } from '../api'
import { highlightText } from '../lib/text-highlight'
import { TERM_STATUS_COLOR, termStatusLabel } from '../lib/term-status'
import { Button } from '@/components/ui/button'
import { Chip } from '@/components/ui/chip'
import { Input } from '@/components/ui/input'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { EmptyState } from '@/components/layout/EmptyState'
import { SegmentText } from './SegmentRow.shared'

type ConcordanceTab = 'tm' | 'terms'

interface Props {
  open: boolean
  onClose: () => void
  projectId: number | null
  sourceLanguage: string
  targetLanguage: string
  onInsert: (text: string) => void
  initialQuery?: string
  readOnly?: boolean
  /** The target file's strings are plain text: insert a match's plain target, never its tags. */
  plainText?: boolean
}

export default function ConcordanceModal({ open, onClose, projectId, sourceLanguage, targetLanguage, onInsert, initialQuery, readOnly = false, plainText = false }: Props) {
  const [query, setQuery] = useState(initialQuery ?? '')
  const [tmResults, setTmResults] = useState<ConcordanceResult[]>([])
  const [termResults, setTermResults] = useState<TermConcordanceResult[]>([])
  const [loading, setLoading] = useState(false)
  const [searched, setSearched] = useState(false)
  // Always opens on TM; Terminology is a click away inside the modal.
  const [tab, setTab] = useState<ConcordanceTab>('tm')
  const [keepOpen, setKeepOpen] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Single search path for both triggers: auto-search on open, and Search / Enter.
  // `searchSeq` drops superseded responses — opening with a selection searches
  // immediately, so retyping before that answers is normal, and a slow first
  // response must not land last and overwrite what the user is now looking at.
  const searchSeq = useRef(0)

  const runSearch = useCallback(async (raw: string) => {
    const term = raw.trim()
    if (!term) return
    // Say so rather than returning silently: without project context there is
    // nothing to search, and a modal that opens prefilled and does nothing reads
    // as a broken feature.
    if (!projectId || !sourceLanguage || !targetLanguage) {
      setError("Can't search — this job is missing its project context. Reload the page.")
      setSearched(false)
      return
    }
    const seq = ++searchSeq.current
    setError(null)
    setLoading(true)
    try {
      const data = await concordanceSearch(projectId, sourceLanguage, targetLanguage, term)
      if (seq !== searchSeq.current) return
      setTmResults(data.tm_matches ?? [])
      setTermResults(data.term_matches ?? [])
      setSearched(true)
    } catch {
      if (seq !== searchSeq.current) return
      // Don't fall through to the "no matches" empty state — a failed request
      // and a genuinely empty TM are different answers.
      setTmResults([])
      setTermResults([])
      setSearched(false)
      setError('Search failed. Check your connection and try again.')
    } finally {
      if (seq === searchSeq.current) setLoading(false)
    }
  }, [projectId, sourceLanguage, targetLanguage])

  // Latest-ref so the reset effect below can call runSearch without listing it
  // as a dependency: runSearch changes identity whenever projectId or a language
  // changes, and re-running the reset would discard whatever the user has typed
  // and re-search the phrase the modal opened with.
  const runSearchRef = useRef(runSearch)
  runSearchRef.current = runSearch

  useEffect(() => {
    if (!open || initialQuery === undefined) return
    setQuery(initialQuery)
    setSearched(false)
    setTmResults([])
    setTermResults([])
    setError(null)
    // Invalidate anything still in flight from a previous open, so its results
    // can't paint over this one's. That also orphans the in-flight `finally`,
    // which is seq-guarded — so clear `loading` here or the Search button stays
    // disabled reading "Searching…" with no request left to finish it.
    searchSeq.current++
    setLoading(false)
    runSearchRef.current(initialQuery)
  }, [open, initialQuery])

  useEffect(() => {
    if (open) setTab('tm')
  }, [open])

  const handleSearch = () => runSearch(query)

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      handleSearch()
    }
  }

  // Insert the selected text from a result, or the full text if nothing selected.
  //
  // For TM matches, the caller passes `r.target_rich_text || r.target_text` (the
  // plain target for a plain-text file) so
  // the editor receives the tag-bearing form when present — its setText path
  // parses XLIFF inline tags into atomic chips automatically. When
  // the user makes a partial selection inside the rendered chips, we use the
  // raw selection (plain text only — DOM selection can't span the chip's
  // internal data attributes), which inserts as plain.
  const handleInsert = useCallback((fullText: string) => {
    if (readOnly) return
    const sel = window.getSelection()?.toString().trim()
    onInsert(sel && sel.length > 0 && sel.length < fullText.length ? sel : fullText)
    if (!keepOpen) onClose()
  }, [readOnly, onInsert, onClose, keepOpen])

  // Same <mark> styling as the F&R inline highlights.
  const highlightMatch = (text: string) => highlightText(text, query)

  const tmCount = tmResults.length
  const termCount = termResults.length

  return (
    <Dialog open={open} onOpenChange={v => !v && onClose()}>
      <DialogContent draggable className="sm:max-w-2xl max-h-[80vh] flex flex-col">
        <DialogHeader>
          <DialogTitle>Concordance search</DialogTitle>
        </DialogHeader>

        <div className="flex gap-2 mb-3">
          <Input
            value={query}
            onChange={e => setQuery(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Search TM and terminology..."
            autoFocus
            className="flex-1"
          />
          <Button onClick={handleSearch} disabled={loading || !query.trim()}>
            {loading ? 'Searching...' : 'Search'}
          </Button>
        </div>

        {error && (
          <p role="alert" className="text-xs text-red-600 dark:text-red-400 mb-2">
            {error}
          </p>
        )}

        <Tabs value={tab} onValueChange={v => setTab(v as ConcordanceTab)} className="flex-1 min-h-0">
          {searched && (
            <div className="flex items-center gap-1">
              <TabsList>
                <TabsTrigger value="tm">
                  TM matches {tmCount > 0 && <span className="text-muted-foreground">({tmCount})</span>}
                </TabsTrigger>
                <TabsTrigger value="terms">
                  Terminology {termCount > 0 && <span className="text-muted-foreground">({termCount})</span>}
                </TabsTrigger>
              </TabsList>
              <CheckboxField size="compact" className="ml-auto" label="Keep open" checked={keepOpen} onChange={setKeepOpen} />
            </div>
          )}

          <div className="flex-1 overflow-y-auto min-h-0">
            {!readOnly && searched && (tmCount > 0 || termCount > 0) && (
              <p className="text-[10px] text-gray-400 dark:text-gray-600 mb-1 px-1">
                Click a result to insert the full target. Select part of the text to insert only that.
              </p>
            )}

            <TabsContent value="tm">
              {searched && tmCount === 0 && (
                <EmptyState message="No matches found in Translation memory" className="text-sm text-gray-400 dark:text-gray-500" />
              )}
              {tmCount > 0 && (
                <div className="space-y-1">
                  {tmResults.map(r => (
                    <div
                      key={r.id}
                      onClick={readOnly ? undefined : () => handleInsert(plainText ? r.target_text : r.target_rich_text || r.target_text)}
                      className={`w-full text-left p-3 rounded-lg border border-gray-200 dark:border-gray-700 transition-colors group ${readOnly ? 'opacity-70 cursor-default' : 'hover:bg-primary/10 cursor-pointer'}`}
                    >
                      <div className="grid grid-cols-2 gap-3">
                        <div>
                          <p className="text-xs text-gray-400 dark:text-gray-500 mb-0.5 uppercase">Source</p>
                          <p className="text-sm text-gray-700 dark:text-gray-300 wrap-anywhere">
                            <SegmentText text={r.source_text} richText={r.source_rich_text} highlightTerm={query} />
                          </p>
                        </div>
                        <div>
                          <p className="text-xs text-gray-400 dark:text-gray-500 mb-0.5 uppercase">Target {!readOnly && <span className="text-primary opacity-0 group-hover:opacity-100 transition-opacity ml-1">click to insert</span>}{readOnly && <span className="text-gray-400 ml-1">🔒</span>}</p>
                          <p className="text-sm text-gray-700 dark:text-gray-300 select-text wrap-anywhere" onMouseDown={e => e.stopPropagation()}>
                            <SegmentText text={r.target_text} richText={r.target_rich_text} highlightTerm={query} />
                          </p>
                        </div>
                      </div>
                      <p className="text-[10px] text-gray-400 dark:text-gray-600 mt-1 wrap-anywhere">TM: {r.tm_name}</p>
                    </div>
                  ))}
                </div>
              )}
            </TabsContent>

            <TabsContent value="terms">
              {searched && termCount === 0 && (
                <EmptyState message="No matching terminology found" className="text-sm text-gray-400 dark:text-gray-500" />
              )}
              {termCount > 0 && (
                <div className="space-y-1">
                  {termResults.map(r => (
                    <div
                      key={r.id}
                      onClick={readOnly ? undefined : () => handleInsert(r.target_term)}
                      className={`w-full text-left p-3 rounded-lg border border-primary/40 transition-colors group ${readOnly ? 'opacity-70 cursor-default' : 'hover:bg-primary/10 cursor-pointer'}`}
                    >
                      <div className="grid grid-cols-2 gap-3">
                        <div>
                          <p className="text-xs text-gray-400 dark:text-gray-500 mb-0.5 uppercase">Source term</p>
                          <p className="text-sm text-gray-700 dark:text-gray-300 font-medium wrap-anywhere">{highlightMatch(r.source_term)}</p>
                        </div>
                        <div>
                          <p className="text-xs text-gray-400 dark:text-gray-500 mb-0.5 uppercase">Target term {!readOnly && <span className="text-primary opacity-0 group-hover:opacity-100 transition-opacity ml-1">click to insert</span>}{readOnly && <span className="text-gray-400 ml-1">🔒</span>}</p>
                          <p className="text-sm text-gray-700 dark:text-gray-300 font-medium select-text wrap-anywhere" onMouseDown={e => e.stopPropagation()}>{highlightMatch(r.target_term)}</p>
                        </div>
                      </div>
                      <div className="flex items-center gap-2 mt-1">
                        <Chip size="small" color={TERM_STATUS_COLOR[r.status] ?? 'grey'}>{termStatusLabel(r.status)}</Chip>
                        <span className="text-[10px] text-gray-400 dark:text-gray-600 wrap-anywhere">TB: {r.term_base_name}</span>
                        {r.notes && <span className="text-[10px] text-gray-400 dark:text-gray-600 wrap-anywhere">— {r.notes}</span>}
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </TabsContent>
          </div>
        </Tabs>
      </DialogContent>
    </Dialog>
  )
}
