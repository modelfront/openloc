import type { ButtonHTMLAttributes, HTMLAttributes } from 'react'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'

/** Grey that darkens on hover; red on hover for an action that removes text. */
type Tone = 'neutral' | 'danger'

const TONES: Record<Tone, string> = {
  neutral: 'text-gray-400 hover:bg-muted hover:text-gray-700 dark:hover:text-gray-200',
  danger: 'text-gray-400 hover:bg-muted hover:text-red-500',
}

/** One cell of a SegmentRowActionBar: the bar's full height, so its hover
 *  fill reaches the bar's edges. SegmentEditorToolbar's buttons use it too. */
export const SEGMENT_BAR_CELL = 'inline-flex h-full items-center justify-center px-1.5 leading-none transition-colors'

type Props = Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'className' | 'type'> & {
  tone?: Tone
  /** Why a disabled action is disabled, as a tooltip. The action stays
   *  focusable and hoverable so the reason is reachable; clicks do nothing. */
  disabledReason?: string
}

/**
 * A compact text action on the active segment's borders: Clear on the
 * translation's, Split on the source's. 9px text in a SegmentRowActionBar
 * cell, filled on hover. Anywhere else, use Button.
 */
export function SegmentRowAction({ tone = 'neutral', disabled, disabledReason, onClick, title, ...props }: Props) {
  const explain = !!disabled && !!disabledReason
  const button = (
    // eslint-disable-next-line no-restricted-syntax -- the editor border needs a control smaller than Button's smallest size
    <button
      type="button"
      {...props}
      title={explain ? undefined : title}
      disabled={disabled && !explain}
      aria-disabled={explain || undefined}
      onClick={explain ? undefined : onClick}
      className={`${SEGMENT_BAR_CELL} text-[9px] disabled:opacity-50 disabled:cursor-wait ${explain ? 'text-gray-400 opacity-50 cursor-not-allowed' : `cursor-pointer ${TONES[tone]}`}`}
    />
  )
  if (!explain) return button
  return (
    <Tooltip>
      <TooltipTrigger delay={0} render={button} />
      <TooltipContent>{disabledReason}</TooltipContent>
    </Tooltip>
  )
}

type BarProps = Omit<HTMLAttributes<HTMLDivElement>, 'className'> & {
  /** `bottom` when the open row sits right under the sticky column header, which would cover a top bar. */
  placement?: 'top' | 'bottom'
}

/**
 * Holds SegmentRowActions on the top (or bottom) border of the open segment's
 * source box or translation editor, which must be `relative`. It sits centred
 * on the border at the right and reaches half its height past the open row,
 * so it has its own fill and border: it covers the row line it overlaps.
 * Its cells sit edge to edge, clipped to its corners, with a divider between
 * the formatting group and Clear.
 */
export function SegmentRowActionBar({ placement = 'top', ...props }: BarProps) {
  const edge = placement === 'top' ? 'top-0 -translate-y-1/2' : 'bottom-0 translate-y-1/2'
  return (
    <div
      {...props}
      className={`absolute right-1.5 ${edge} z-10 flex h-4 items-stretch divide-x divide-border overflow-hidden whitespace-nowrap rounded border border-border bg-background shadow-xs`}
    />
  )
}
