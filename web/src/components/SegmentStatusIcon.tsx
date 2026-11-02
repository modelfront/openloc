// The CAT editor's segment status icons: one family of glyphs. Colour is the
// state — yellow not confirmed, green approved, grey a previous step's state —
// and the shape says who did it: a pencil for a person's edit, a check for a
// person's approval, the same check with sparkles for an approval no person
// made (auto-approved, or confirmed by the assistant).
import type { Translation } from '../api'
import { isAssistantConfirmed, ASSISTANT_CONFIRMED_LABEL, type DisplayStatus } from '../lib/segment-status'
import { Button } from '@/components/ui/button'
import { statusLabel } from './SegmentRow.shared'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'

type SegmentStatusIconKind = 'draft' | 'approved' | 'machine_approved'

const KIND_COLOR: Record<SegmentStatusIconKind, string> = {
  draft: 'text-yellow-500 dark:text-yellow-400',
  approved: 'text-green-600 dark:text-green-400',
  machine_approved: 'text-green-600 dark:text-green-400',
}
const HISTORY_COLOR = 'text-gray-400 dark:text-gray-500'

/**
 * The Status column's icon for a row. The default states (untranslated,
 * pretranslated) get none, so the column only marks rows that progressed;
 * where a pretranslation came from is the Match column's job.
 */
export function statusIconFor(
  status: DisplayStatus,
  translation: Pick<Translation, 'status' | 'confirmed_via'> | null | undefined,
): { kind: SegmentStatusIconKind; label: string } | null {
  switch (status) {
    case 'needs_review': return { kind: 'draft', label: statusLabel('needs_review') }
    case 'auto_approved': return { kind: 'machine_approved', label: statusLabel('auto_approved') }
    case 'approved': return isAssistantConfirmed(translation)
      ? { kind: 'machine_approved', label: ASSISTANT_CONFIRMED_LABEL }
      : { kind: 'approved', label: statusLabel('approved') }
    default: return null
  }
}

/** One status icon with its name on hover. `history` greys it out: a previous step's state. */
export function SegmentStatusIcon({ kind, label, history = false, 'data-testid': testId }: {
  kind: SegmentStatusIconKind
  label: string
  history?: boolean
  'data-testid'?: string
}) {
  return (
    <Tooltip>
      <TooltipTrigger render={<span className="inline-flex" data-testid={testId} />}>
        <svg
          viewBox="0 0 20 20"
          width={20}
          height={20}
          overflow="visible"
          role="img"
          aria-label={label}
          data-status-icon={kind}
          className={cn('shrink-0', history ? HISTORY_COLOR : KIND_COLOR[kind])}
        >
          {/* The glyphs are drawn on a 24-unit grid at 19px. */}
          <svg x={0.5} y={0.5} width={19} height={19} viewBox="0 0 24 24" overflow="visible">
            {kind === 'draft' ? <PencilGlyph /> : <CheckGlyph sparkle={kind === 'machine_approved'} />}
          </svg>
        </svg>
      </TooltipTrigger>
      <TooltipContent side="top">{label}</TooltipContent>
    </Tooltip>
  )
}

/**
 * The padlock in a locked row's last column. With `onUnlock` (the user may
 * edit the row) it is a button that unlocks the segment; otherwise an icon.
 * Either way it fills a LOCK_SLOT_WIDTH square.
 */
export function LockIcon({ onUnlock, pending = false }: { onUnlock?: () => void; pending?: boolean }) {
  const glyph = <LockGlyph className="size-4 text-gray-500 dark:text-gray-400" />
  return (
    <Tooltip>
      {onUnlock ? (
        <TooltipTrigger
          render={
            <Button
              variant="ghost"
              size="icon-xs"
              // A hover tint of the text colour, not the ghost's bg-muted: a
              // hovered row is bg-muted too, so that one would not show.
              className="hover:bg-foreground/10 dark:hover:bg-foreground/15"
              aria-label="Unlock"
              loading={pending}
              onClick={e => {
                // The padlock sits on the row, which opens on click.
                e.stopPropagation()
                onUnlock()
              }}
            />
          }
        >
          {glyph}
        </TooltipTrigger>
      ) : (
        <TooltipTrigger render={<span className="inline-flex size-6 items-center justify-center" />}>{glyph}</TooltipTrigger>
      )}
      <TooltipContent side="top">{onUnlock ? 'Locked. Click to unlock.' : 'Locked'}</TooltipContent>
    </Tooltip>
  )
}

/**
 * The padlock drawing, in the current text colour. `LockIcon` shows it on a
 * locked row; the selection bar's Lock button carries it too.
 */
export function LockGlyph({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" role="img" aria-label="Locked" data-status-icon="locked" className={cn('shrink-0', className)}>
      {/* The keyhole is cut out, so whatever is behind shows through. */}
      <path
        fillRule="evenodd"
        fill="currentColor"
        d="M4.6 7 H11.4 A1.6 1.6 0 0 1 13 8.6 V12.9 A1.6 1.6 0 0 1 11.4 14.5 H4.6 A1.6 1.6 0 0 1 3 12.9 V8.6 A1.6 1.6 0 0 1 4.6 7 Z M8 9.5 A1.1 1.1 0 1 0 8 11.7 A1.1 1.1 0 1 0 8 9.5 Z"
      />
      <path d="M5.3 7 V5.2 A2.7 2.7 0 0 1 10.7 5.2 V7" stroke="currentColor" strokeWidth={1.3} fill="none" />
    </svg>
  )
}

/**
 * A pencil like Material's "edit" icon: a solid eraser on a hollow body that
 * narrows into a short tip, drawn upright and turned 45° so the tip points to
 * the bottom left.
 */
function PencilGlyph() {
  return (
    <g fill="none" stroke="currentColor" strokeWidth={1.65} strokeLinejoin="round" strokeLinecap="round" transform="rotate(45 12 12)">
      <path d="M9 4.2 V2.1 Q9 0.6 10.5 0.6 H13.5 Q15 0.6 15 2.1 V4.2 Z" fill="currentColor" />
      <path d="M9 4.2 V18.4 L12 23.4 L15 18.4 V4.2 Z" />
    </g>
  )
}

// A four-point sparkle centred on (x, y) with radius r.
function sparklePath(x: number, y: number, r: number): string {
  const k = r * 0.16
  return `M${x} ${y - r} Q${x + k} ${y - k} ${x + r} ${y} Q${x + k} ${y + k} ${x} ${y + r} Q${x - k} ${y + k} ${x - r} ${y} Q${x - k} ${y - k} ${x} ${y - r} Z`
}
// The big sparkle sits on the circle's line at the lower right, the small one
// in the open space on the right. Both reach a little past the grid.
const SPARKLE_PATHS = [sparklePath(18.3, 19.1, 6), sparklePath(21.8, 11.2, 4.8)]

/**
 * An open circle and a filled check, thin at its ends and thickest where the
 * strokes meet. Without sparkles the circle is open at the upper right, where
 * the check's tip leaves it; with them the whole right side is open and the
 * sparkles stand there.
 */
function CheckGlyph({ sparkle }: { sparkle: boolean }) {
  const arc = sparkle ? 'M17 3.34 A10 10 0 1 0 12.87 21.96' : 'M21.8 10 A10 10 0 1 1 17 3.34'
  return (
    <>
      <path d={arc} fill="none" stroke="currentColor" strokeWidth={1.65} strokeLinecap="round" />
      <path
        transform="translate(5.5 0.1) scale(0.7)"
        fill="currentColor"
        d="M2.5 13.2 C3.3 12.4 4.4 12.3 5.2 13.1 L9.2 16.8 L19.6 4.6 C20.1 4 21 3.9 21.5 4.5 C21.9 5 21.8 5.6 21.4 6.1 L10.6 20.2 C9.9 21 8.7 21.1 8 20.3 L2.5 15.6 C1.9 14.9 1.9 13.9 2.5 13.2 Z"
      />
      {sparkle && SPARKLE_PATHS.map(d => <path key={d} d={d} fill="currentColor" />)}
    </>
  )
}
