import { useQuery } from '@tanstack/react-query'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from '@/components/ui/dialog'
import { listTMEntryHistory, type TMEntry } from '../api'
import { queryKeys } from '../lib/query-keys'
import { SegmentText } from '@/components/SegmentRow.shared'
import { Chip } from '@/components/ui/chip'

interface Props {
  /** TM ID — needed for the URL even though the entry uniquely identifies the row. */
  tmId: number
  /** The entry whose history we're viewing; null = dialog closed. */
  entry: TMEntry | null
  onClose: () => void
}

// Why a TM entry changed, as written by the backend (internal/translationmemory).
// The wire values are snake_case enums and `ui-ux.md` forbids showing those; a
// reason we don't recognise falls back to the raw value rather than vanishing,
// because an unlabelled reason is still information.
//
// `change_reason` is free-form on the wire, so the lookup must be own-property
// only: a bare `map[key]` resolves "__proto__" up the prototype chain to an
// object, which is not nullish, so `??` would pass it to React and throw.
const CHANGE_REASON_LABELS: Record<string, string> = {
  edit: 'Edited',
  cat_confirm: 'Confirmed in editor',
  find_replace: 'Find & replace',
  feedback_bulk: 'Job completed',
  migration_dedupe: 'Deduplicated',
}

function changeReasonLabel(reason: string): string {
  return Object.hasOwn(CHANGE_REASON_LABELS, reason) ? CHANGE_REASON_LABELS[reason] : reason
}

/**
 * Edit-history dialog for a single TM entry. Shows the BEFORE-snapshot of
 * each past edit (newest first) plus the current live values at the top.
 *
 * Backend: `GET /api/tms/{tmID}/entries/{entryID}/history`.
 *
 * A Dialog (not a side panel or inline expand), like ConcordanceModal and
 * FindReplaceModal, because the TM table is virtualized. Clock icon in
 * TMBrowser's actions column triggers it.
 */
export default function TMEntryHistoryDialog({ tmId, entry, onClose }: Props) {
  const open = entry !== null

  const { data: history = [], isLoading, error } = useQuery({
    queryKey: queryKeys.tm.entryHistory(tmId, entry?.id ?? 0),
    queryFn: () => listTMEntryHistory(tmId, entry!.id),
    enabled: open && entry !== null,
  })

  return (
    <Dialog open={open} onOpenChange={v => !v && onClose()}>
      <DialogContent draggable className="sm:max-w-2xl max-h-[80vh] flex flex-col" data-testid="tm-history-dialog">
        <DialogHeader>
          <DialogTitle>Edit history</DialogTitle>
          <DialogDescription>
            Past edits to this TM entry, newest first. Each row shows the values
            as they were BEFORE that edit was saved.
          </DialogDescription>
        </DialogHeader>

        {entry && (
          <div className="overflow-y-auto flex-1 -mx-6 px-6">
            {/* Current (live) values */}
            <div className="mb-4 pb-3 border-b border-gray-200 dark:border-gray-700">
              <div className="flex items-center gap-2 mb-2">
                <span className="text-xs font-semibold uppercase tracking-wide text-emerald-700 dark:text-emerald-400">
                  Current
                </span>
                <span className="text-[10px] text-gray-400 wrap-anywhere">
                  {entry.updated_by ? `last edited by ${entry.updated_by}` : 'imported'}
                  {entry.updated_at && ` · ${new Date(entry.updated_at).toLocaleString()}`}
                </span>
              </div>
              <HistoryRowBody
                source_text={entry.source_text}
                target_text={entry.target_text}
                source_rich_text={entry.source_rich_text ?? null}
                target_rich_text={entry.target_rich_text ?? null}
              />
            </div>

            {/* Past edits */}
            {isLoading && (
              <div className="text-sm text-gray-500 dark:text-gray-400 py-4 text-center">Loading history…</div>
            )}
            {error && (
              <div className="text-sm text-red-600 dark:text-red-400 py-4 text-center wrap-anywhere">
                Failed to load history: {(error as Error).message}
              </div>
            )}
            {!isLoading && !error && history.length === 0 && (
              <div
                data-testid="tm-history-empty"
                className="text-sm text-gray-500 dark:text-gray-400 py-6 text-center wrap-anywhere"
              >
                No past edits — this entry hasn't been changed since it was
                {entry.created_by ? ` created by ${entry.created_by}` : ' imported'}
                {entry.created_at && ` on ${new Date(entry.created_at).toLocaleDateString()}`}.
              </div>
            )}
            {!isLoading && !error && history.length > 0 && (
              <ul className="space-y-3" data-testid="tm-history-list">
                {history.map(h => (
                  <li
                    key={h.id}
                    className="pb-3 border-b border-gray-100 dark:border-gray-800 last:border-b-0"
                    data-testid={`tm-history-row-${h.id}`}
                  >
                    <div className="flex items-center gap-2 mb-2">
                      <span className="text-xs text-gray-700 dark:text-gray-300 wrap-anywhere">
                        {h.changed_by_email || <span className="text-gray-400 italic">unknown user</span>}
                      </span>
                      <span className="text-[10px] text-gray-400">
                        {new Date(h.created_at).toLocaleString()}
                      </span>
                      {h.change_reason && (
                        <Chip className="font-normal">{changeReasonLabel(h.change_reason)}</Chip>
                      )}
                    </div>
                    <HistoryRowBody
                      source_text={h.source_text}
                      target_text={h.target_text}
                      source_rich_text={h.source_rich_text}
                      target_rich_text={h.target_rich_text}
                    />
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

interface HistoryRowBodyProps {
  source_text: string
  target_text: string
  source_rich_text?: string | null
  target_rich_text?: string | null
}

function HistoryRowBody({ source_text, target_text, source_rich_text, target_rich_text }: HistoryRowBodyProps) {
  return (
    <div className="grid grid-cols-2 gap-3 text-sm">
      <div>
        <div className="text-[10px] uppercase tracking-wide text-gray-400 mb-1">Source</div>
        <div className="text-gray-900 dark:text-gray-100 break-words">
          <SegmentText text={source_text} richText={source_rich_text} />
        </div>
      </div>
      <div>
        <div className="text-[10px] uppercase tracking-wide text-gray-400 mb-1">Target</div>
        <div className="text-gray-700 dark:text-gray-300 break-words">
          <SegmentText text={target_text} richText={target_rich_text} />
        </div>
      </div>
    </div>
  )
}
