// DeadlinePicker — the control for setting one deadline.
//
// The value is an ISO 8601 instant and goes back to the caller as UTC
// (`toISOString()`), which is how every deadline is stored. It is shown and
// edited in the viewer's browser zone; the zone selector lets the user enter a
// time in another zone ("17:00 New York"), but the choice is not kept — the
// next open shows the same instant in the viewer's zone again.
//
// Edits are held while the popover is open and committed once on close, so
// picking a day and then a time is one change, not two saves.
//
// `variant="chip"` (default) draws the same chip the project list, job list and
// Tasks page show (`deadlineChipProps`); `variant="field"` is a form field.

import { useRef, useState } from 'react'
import { Chip } from '@/components/ui/chip'
import { DateTimePicker } from '@/components/ui/date-picker'
import { deadlineChipProps } from '@/lib/deadline'
import { DEADLINE_PRESETS, type DatePreset } from '@/lib/date-presets'
import { browserTimeZone } from '@/lib/time-zone'

interface Props {
  /** ISO 8601, or null/undefined for no deadline. */
  value: string | null | undefined
  /** Called with a UTC ISO string, or null when cleared. */
  onChange: (value: string | null) => void
  variant?: 'chip' | 'field'
  /** Quick picks beside the calendar; a pick saves at once. Default DEADLINE_PRESETS, [] for none. */
  presets?: DatePreset[]
  disabled?: boolean
  /** Names which deadline this is — there are several on a workflow screen. */
  'aria-label': string
  className?: string
  'data-testid'?: string
}

const toISO = (d: Date | null) => (d ? d.toISOString() : null)

export function DeadlinePicker({
  value, onChange, variant = 'chip', presets = DEADLINE_PRESETS, disabled, 'aria-label': ariaLabel, className, 'data-testid': testId,
}: Props) {
  const [timeZone, setTimeZone] = useState(browserTimeZone)
  // The pending edit while the popover is open; undefined = closed. A ref too,
  // because a preset sets it and closes the popover in the same event.
  const [draft, setDraftState] = useState<Date | null | undefined>(undefined)
  const draftRef = useRef<Date | null | undefined>(undefined)
  const setDraft = (d: Date | null | undefined) => {
    draftRef.current = d
    setDraftState(d)
  }

  const stored = value ? new Date(value) : null
  const commit = (d: Date | null) => {
    if (toISO(d) !== toISO(stored)) onChange(toISO(d))
  }

  const chip = deadlineChipProps(value)
  // "No due date" is how a deadline READS wherever it is only displayed. This
  // one is a control, so with nothing set it says what clicking it does.
  const tip = value ? chip.title : 'Set due date'

  return (
    <DateTimePicker
      value={draft !== undefined ? draft : stored}
      onChange={d => (draftRef.current === undefined ? commit(d) : setDraft(d))}
      onOpenChange={open => {
        if (open) {
          setDraft(stored)
          return
        }
        if (draftRef.current !== undefined) commit(draftRef.current)
        setDraft(undefined)
        setTimeZone(browserTimeZone())
      }}
      timeZone={timeZone}
      onTimeZoneChange={setTimeZone}
      defaultTime="17:00"
      presets={presets}
      trigger={variant === 'chip' ? <Chip {...chip}>{value ? chip.children : 'Set due date'}</Chip> : undefined}
      placeholder="Set due date"
      disabled={disabled}
      title={tip}
      aria-label={value ? `${ariaLabel}: ${chip.title}` : ariaLabel}
      className={className}
      data-testid={testId}
    />
  )
}
