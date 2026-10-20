// TimeInput — a 24-hour time field ("HH:mm") with a dropdown of every
// `step` minutes. Typing filters the list ("17" → 17:00, 17:15, …; "9" and
// "930" work too) and a typed time commits as soon as it is complete, or on
// Enter / blur for short forms like "9" or "1730". Opening the field with its
// own value in it shows the whole list, scrolled to that time.
//
// Base UI's Autocomplete: free text plus a filtered, keyboard-navigable list.

import { useMemo, useRef, useState } from 'react'
import { Autocomplete } from '@base-ui/react/autocomplete'
import { CheckIcon } from 'lucide-react'

import { cn } from '@/lib/utils'
import { Input } from './input'
import { matchesTime, pad, parseTime, timeSlots } from '@/lib/time-of-day'

interface Props {
  /** "HH:mm", or '' for none. */
  value: string
  onChange: (time: string) => void
  /** Minutes between list entries. Default 15. */
  step?: number
  /** Where the list opens when `value` is empty, "HH:mm". */
  anchor?: string
  className?: string
  'aria-label'?: string
}

export function TimeInput({ value, onChange, step = 15, anchor = '09:00', className, 'aria-label': ariaLabel = 'Time' }: Props) {
  // While the field has focus it shows what the user types; otherwise the value.
  const [text, setText] = useState(value)
  // Filter only once the user types: opening on "17:00" shows every time.
  const [typed, setTyped] = useState(false)
  const [focused, setFocused] = useState(false)
  const listRef = useRef<HTMLDivElement>(null)

  const slots = useMemo(() => timeSlots(step), [step])
  const shown = focused ? text : value

  const commit = (raw: string) => {
    const t = parseTime(raw)
    if (t) {
      setText(t)
      if (t !== value) onChange(t)
    } else {
      setText(value)
    }
  }

  const scrollToCurrent = () => {
    requestAnimationFrame(() => {
      const list = listRef.current
      if (!list) return
      const [h, m] = (value || anchor).split(':').map(Number)
      const floored = Math.floor((h * 60 + m) / step) * step
      const el = list.querySelector<HTMLElement>(`[data-time="${pad(Math.floor(floored / 60))}:${pad(floored % 60)}"]`)
      if (el) list.scrollTop = el.offsetTop - list.clientHeight / 2 + el.offsetHeight / 2
    })
  }

  return (
    <Autocomplete.Root
      items={slots}
      value={shown}
      onValueChange={(next, details) => {
        setText(next)
        if (details.reason === 'item-press') {
          setTyped(false)
          commit(next)
          return
        }
        if (details.reason === 'input-change') {
          setTyped(true)
          // A complete "HH:mm" commits while typing; short forms wait for Enter or blur.
          if (/^\d{1,2}:\d{2}$/.test(next.trim())) commit(next)
        }
      }}
      filter={(slot, query) => !typed || matchesTime(slot, query)}
      openOnInputClick
      onOpenChange={(open, details) => {
        // Typing opens the list too; that open must keep filtering.
        if (!open || details.reason === 'input-change') return
        setTyped(false)
        scrollToCurrent()
      }}
    >
      <Autocomplete.Input
        render={<Input clearable={false} className={cn('w-28', className)} inputClassName="tabular-nums" />}
        aria-label={ariaLabel}
        placeholder="HH:mm"
        inputMode="numeric"
        onFocus={e => {
          setText(value)
          setFocused(true)
          // Select it all, so typing replaces the time instead of appending to
          // it. After the click's own caret placement, hence the frame delay.
          const input = e.currentTarget
          requestAnimationFrame(() => input.select())
        }}
        onBlur={() => {
          setFocused(false)
          setTyped(false)
          commit(text)
        }}
        onKeyDown={e => {
          if (e.key === 'Enter') commit(text)
        }}
      />
      <Autocomplete.Portal>
        <Autocomplete.Positioner sideOffset={4} align="start" className="isolate z-50">
          <Autocomplete.Popup className="w-(--anchor-width) min-w-28 rounded-lg bg-popover p-1 text-sm text-popover-foreground shadow-md ring-1 ring-foreground/10">
            <Autocomplete.Empty className="px-2 py-1.5 text-muted-foreground empty:hidden">No match</Autocomplete.Empty>
            <Autocomplete.List ref={listRef} className="max-h-56 overflow-y-auto relative">
              {(slot: string) => (
                <Autocomplete.Item
                  key={slot}
                  value={slot}
                  data-time={slot}
                  className="flex cursor-default items-center gap-2 rounded-sm px-2 py-1.5 tabular-nums outline-hidden select-none data-highlighted:bg-muted data-highlighted:text-foreground"
                >
                  {slot}
                  {slot === value && <CheckIcon className="ml-auto size-4" aria-hidden="true" />}
                </Autocomplete.Item>
              )}
            </Autocomplete.List>
          </Autocomplete.Popup>
        </Autocomplete.Positioner>
      </Autocomplete.Portal>
    </Autocomplete.Root>
  )
}
