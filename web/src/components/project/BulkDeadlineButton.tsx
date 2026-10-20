// BulkDeadlineButton — "Set deadline" for the selected jobs' active steps.
//
// Opens the same picker as a single deadline (DateTimePicker: zone, calendar,
// time, presets), always starting empty. Only an explicit button acts: Done or
// a preset applies the instant (UTC) to every selected job, Clear removes their
// deadlines.
// Closing any other way changes nothing — this writes to many jobs at once.
// As with DeadlinePicker, the zone is for entry only and resets on close.

import { useRef, useState } from 'react'
import { DateTimePicker } from '../ui/date-picker'
import { Button } from '../ui/button'
import { browserTimeZone } from '../../lib/time-zone'
import { DEADLINE_PRESETS, type DatePreset } from '../../lib/date-presets'

interface Props {
  /** Called with a UTC ISO string, or null to clear. */
  onApply: (deadline: string | null) => void
  pending: boolean
  disabled: boolean
  /** Quick picks; a pick applies at once, like Done. Default DEADLINE_PRESETS, [] for none. */
  presets?: DatePreset[]
}

export function BulkDeadlineButton({ onApply, pending, disabled, presets = DEADLINE_PRESETS }: Props) {
  const [draft, setDraftState] = useState<Date | null>(null)
  // A ref too: a preset sets the draft and confirms it in the same event.
  const draftRef = useRef<Date | null>(null)
  const setDraft = (d: Date | null) => {
    draftRef.current = d
    setDraftState(d)
  }
  const [timeZone, setTimeZone] = useState(browserTimeZone)

  return (
    <DateTimePicker
      value={draft}
      // null only comes from the panel's Clear: the popover opens empty and
      // has no other way to empty the value.
      onChange={d => (d ? setDraft(d) : onApply(null))}
      onDone={() => {
        if (draftRef.current) onApply(draftRef.current.toISOString())
      }}
      onOpenChange={open => {
        if (open) return
        setDraft(null)
        setTimeZone(browserTimeZone())
      }}
      timeZone={timeZone}
      onTimeZoneChange={setTimeZone}
      defaultTime="17:00"
      presets={presets}
      clearable="always"
      triggerAs={<Button variant="outline" size="xs" />}
      trigger={pending ? 'Saving…' : 'Set deadline'}
      disabled={disabled}
      data-testid="bulk-deadline-btn"
    />
  )
}
