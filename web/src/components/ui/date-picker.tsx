// DateTimePicker — a field that opens a calendar in a popover, with optional
// quick-pick presets ("1 day", "2 business days", …) on the left.
//
// It holds an instant (Date | null) shown in `timeZone` (the browser's by
// default). Pass `onTimeZoneChange` to let the user pick the zone; switching
// keeps the wall-clock time, so "17:00 Berlin" becomes "17:00 Tokyo".
//
// `trigger` swaps the field for custom content (DeadlinePicker's chip); the
// clear X then moves into the popover as a "Clear" button.

import { useEffect, useMemo, useRef, useState, type ReactElement, type ReactNode } from 'react'
import { TZDate } from '@date-fns/tz'
import { format } from 'date-fns'
import { CalendarIcon, ChevronLeft, ChevronRight } from 'lucide-react'
import { DayPicker, getDefaultClassNames, type DayButtonProps, type DayPickerProps } from 'react-day-picker'

import { cn } from '@/lib/utils'
import type { DatePreset } from '@/lib/date-presets'
import { browserTimeZone, timeZoneOffsetLabel, timeZoneOptions } from '@/lib/time-zone'
import { Button, buttonVariants } from './button'
import { ClearButton } from './clear-button'
import { Combobox } from './combobox'
import { controlFocusVisibleClasses, controlFrameClasses, controlHeightClass } from './control-box'
import { TimeInput } from './time-input'
import { Popover, PopoverContent, PopoverTrigger } from './popover'

interface CommonProps {
  /** Quick picks shown beside the calendar. Picking one closes the popover. */
  presets?: DatePreset[]
  /** Days before today can't be picked. */
  disablePast?: boolean
  /** Offer a way to empty the value. Default true. 'always' keeps the panel's
   *  Clear even with no value — for a trigger whose Clear acts on something
   *  else (the bulk deadline button clears the selected jobs' deadlines). */
  clearable?: boolean | 'always'
  placeholder?: string
  disabled?: boolean
  /** Custom trigger content, drawn instead of the field (e.g. a chip). */
  trigger?: ReactNode
  /** Element the custom trigger renders as, e.g. `<Button variant="outline" />`;
   *  it replaces the trigger's own look. */
  triggerAs?: ReactElement
  /** Called when Done or a preset is pressed, before the popover closes —
   *  a preset picks and confirms in one click. */
  onDone?: () => void
  /** Called when the popover opens or closes. */
  onOpenChange?: (open: boolean) => void
  /** Applied to the field box (width goes here), or to the custom trigger. */
  className?: string
  title?: string
  'aria-label'?: string
  /** On the trigger. The trigger also carries `data-value` with the current value, for tests. */
  'data-testid'?: string
}

// --- Shell: trigger + popover ------------------------------------------------

interface PickerShellProps extends Omit<CommonProps, 'disablePast'> {
  open: boolean
  setOpen: (open: boolean) => void
  /** Formatted value; empty shows the placeholder. */
  display: string
  /** Machine-readable value for `data-value`. */
  value: string
  onClear: () => void
  onPreset: (preset: DatePreset) => void
  /** Left side of the bottom row (the time field). */
  footer?: ReactNode
  /** Adds a "Done" button to the bottom row. */
  done?: boolean
  children: ReactNode
}

function PickerShell({
  open,
  setOpen,
  display,
  value,
  onClear,
  onPreset,
  footer,
  done,
  presets = [],
  clearable = true,
  placeholder,
  disabled,
  trigger,
  triggerAs,
  onDone,
  onOpenChange,
  className,
  title,
  'aria-label': ariaLabel,
  'data-testid': testId,
  children,
}: PickerShellProps) {
  const changeOpen = (o: boolean) => {
    setOpen(o)
    onOpenChange?.(o)
  }
  const triggerProps = { disabled, title, 'aria-label': ariaLabel, 'data-testid': testId, 'data-value': value }
  const clearInPanel = trigger !== undefined && (clearable === 'always' || (clearable && display !== ''))
  // In field mode the popover lines up with the frame, not the trigger button
  // inside its padding.
  const frameRef = useRef<HTMLDivElement>(null)

  return (
    <Popover open={open} onOpenChange={changeOpen}>
      {triggerAs ? (
        <PopoverTrigger {...triggerProps} render={triggerAs}>{trigger}</PopoverTrigger>
      ) : trigger !== undefined ? (
        <PopoverTrigger
          {...triggerProps}
          className={cn(
            'inline-flex cursor-pointer items-center rounded-md hover:opacity-80 disabled:cursor-not-allowed disabled:opacity-50',
            controlFocusVisibleClasses,
            className,
          )}
        >
          {trigger}
        </PopoverTrigger>
      ) : (
        // The frame wraps the trigger so the clear X can be a real button
        // beside it — a button inside the trigger would be nested buttons.
        <div ref={frameRef} className={cn('flex w-56 shrink-0 items-center gap-1', controlHeightClass, controlFrameClasses, className)}>
          <PopoverTrigger
            {...triggerProps}
            className={cn(
              'flex h-full min-w-0 flex-1 cursor-pointer items-center gap-2 text-left outline-none disabled:cursor-not-allowed',
              display ? 'text-foreground' : 'text-muted-foreground',
            )}
          >
            <CalendarIcon className="size-4 shrink-0 opacity-50" aria-hidden="true" />
            <span className="min-w-0 flex-1 truncate">{display || placeholder || 'Pick a date'}</span>
          </PopoverTrigger>
          {clearable && display && !disabled && (
            <ClearButton onClear={onClear} data-testid={testId ? `${testId}-clear` : undefined} />
          )}
        </div>
      )}
      <PopoverContent align="start" anchor={trigger === undefined ? frameRef : undefined} className="w-auto flex-row gap-0 p-0">
        {presets.length > 0 && (
          <div className="flex min-w-28 flex-col gap-0.5 border-r p-2" data-slot="date-picker-presets">
            {presets.map(p => (
              <Button key={p.label} variant="ghost" size="sm" className="justify-start" onClick={() => onPreset(p)}>
                {p.label}
              </Button>
            ))}
          </div>
        )}
        <div className="flex flex-col gap-2 p-2.5">
          {children}
          {(footer || done || clearInPanel) && (
            <div className="flex items-center gap-2">
              {footer}
              <span className="ml-auto flex gap-1">
                {clearInPanel && (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      onClear()
                      changeOpen(false)
                    }}
                    data-testid={testId ? `${testId}-clear` : undefined}
                  >
                    Clear
                  </Button>
                )}
                {done && (
                  <Button
                    size="sm"
                    onClick={() => {
                      onDone?.()
                      changeOpen(false)
                    }}
                  >
                    Done
                  </Button>
                )}
              </span>
            </div>
          )}
        </div>
      </PopoverContent>
    </Popover>
  )
}

// --- Calendar ----------------------------------------------------------------
// react-day-picker's DayPicker in the app's tokens. Every DayPicker prop passes
// through (mode, selected, disabled, timeZone, …); only the look is set here.

function Calendar({ className, classNames, showOutsideDays = true, components, ...props }: DayPickerProps) {
  const d = getDefaultClassNames()
  const navButton = cn(buttonVariants({ variant: 'ghost', size: 'icon-sm' }), 'aria-disabled:opacity-50')
  return (
    <DayPicker
      showOutsideDays={showOutsideDays}
      className={cn('w-fit [--cell-size:--spacing(8)]', className)}
      classNames={{
        root: d.root,
        months: cn('relative flex flex-col gap-4 md:flex-row', d.months),
        month: cn('flex w-full flex-col gap-2', d.month),
        nav: cn('absolute inset-x-0 top-0 flex items-center justify-between', d.nav),
        button_previous: cn(navButton, d.button_previous),
        button_next: cn(navButton, d.button_next),
        month_caption: cn('flex h-7 items-center justify-center px-8', d.month_caption),
        caption_label: cn('text-sm font-medium select-none', d.caption_label),
        month_grid: 'border-collapse',
        weekdays: cn('flex', d.weekdays),
        weekday: cn('w-(--cell-size) text-[0.8rem] font-normal text-muted-foreground select-none', d.weekday),
        week: cn('mt-1 flex', d.week),
        day: cn('size-(--cell-size) p-0 text-center', d.day),
        outside: cn('text-muted-foreground', d.outside),
        disabled: cn('opacity-40', d.disabled),
        hidden: cn('invisible', d.hidden),
        ...classNames,
      }}
      components={{
        Chevron: ({ orientation, className: iconClass }) =>
          orientation === 'left'
            ? <ChevronLeft className={cn('size-4', iconClass)} />
            : <ChevronRight className={cn('size-4', iconClass)} />,
        DayButton: CalendarDayButton,
        ...components,
      }}
      {...props}
    />
  )
}

// `day` is not a DOM attribute, and an HTML `color` would collide with Button's own `color` prop.
function CalendarDayButton({ day, modifiers, className, color, ...props }: DayButtonProps) {
  const ref = useRef<HTMLButtonElement>(null)
  // DayPicker moves keyboard focus by flagging the day; the button has to take it.
  useEffect(() => {
    if (modifiers.focused) ref.current?.focus()
  }, [modifiers.focused])

  return (
    <Button
      ref={ref}
      variant="ghost"
      data-selected={modifiers.selected || undefined}
      data-today={modifiers.today || undefined}
      className={cn(
        'size-(--cell-size) p-0 font-normal',
        'data-today:bg-muted data-today:font-semibold',
        'data-selected:bg-primary data-selected:text-primary-foreground data-selected:hover:bg-primary/80',
        className,
      )}
      {...props}
    />
  )
}

// --- DateTimePicker ----------------------------------------------------------

export interface DateTimePickerProps extends CommonProps {
  value: Date | null
  onChange: (value: Date | null) => void
  /** IANA zone the value is shown and edited in. Defaults to the browser's. */
  timeZone?: string
  /** Shows the zone selector. Called with the new zone; `onChange` fires too when a value is set. */
  onTimeZoneChange?: (timeZone: string) => void
  /** Time given to a day picked while the value is empty, "HH:mm". Default "09:00". */
  defaultTime?: string
  /** Minutes between the time dropdown's entries. Default 15. */
  timeStep?: number
}

/** The same wall-clock day and time, anchored in `timeZone`. */
function atWallClock(day: Date, time: string, timeZone: string): TZDate {
  const [h, m] = time.split(':').map(Number)
  return new TZDate(day.getFullYear(), day.getMonth(), day.getDate(), h, m, timeZone)
}

export function DateTimePicker({
  value,
  onChange,
  timeZone,
  onTimeZoneChange,
  defaultTime = '09:00',
  timeStep = 15,
  disablePast,
  onOpenChange,
  ...rest
}: DateTimePickerProps) {
  const tz = timeZone ?? browserTimeZone()
  const [open, setOpen] = useState(false)
  const zoned = value ? new TZDate(value, tz) : undefined
  const time = zoned ? format(zoned, 'HH:mm') : ''
  const [month, setMonth] = useState<Date | undefined>(zoned)
  // Callers get a plain Date: a TZDate would serialize with its zone offset
  // instead of "Z", and carry a zone the caller never asked for.
  const emit = (d: Date | null) => onChange(d ? new Date(d.getTime()) : null)
  const zoneOptions = useMemo(() => (onTimeZoneChange ? timeZoneOptions() : []), [onTimeZoneChange])

  const display = useMemo(() => {
    if (!value) return ''
    const text = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short', timeZone: tz }).format(value)
    return onTimeZoneChange ? `${text} ${timeZoneOffsetLabel(tz, value)}` : text
  }, [value, tz, onTimeZoneChange])

  const setTime = (next: string) => {
    if (!next) return
    emit(atWallClock(zoned ?? TZDate.tz(tz), next, tz))
  }

  const setZone = (next: string) => {
    onTimeZoneChange?.(next)
    if (zoned) emit(atWallClock(zoned, time, next))
  }

  return (
    <PickerShell
      placeholder="Pick a date and time"
      {...rest}
      className={rest.trigger === undefined ? cn('w-72', rest.className) : rest.className}
      open={open}
      setOpen={o => {
        if (o) setMonth(zoned)
        setOpen(o)
      }}
      onOpenChange={onOpenChange}
      display={display}
      value={value ? value.toISOString() : ''}
      onClear={() => emit(null)}
      onPreset={p => {
        emit(p.resolve(TZDate.tz(tz)))
        rest.onDone?.()
        setOpen(false)
        onOpenChange?.(false)
      }}
      footer={
        <TimeInput value={time} onChange={setTime} step={timeStep} anchor={defaultTime} />
      }
      done
    >
      <Calendar
        mode="single"
        timeZone={tz}
        selected={zoned}
        onSelect={d => d && emit(atWallClock(d, time || defaultTime, tz))}
        month={month}
        onMonthChange={setMonth}
        disabled={disablePast ? { before: TZDate.tz(tz) } : undefined}
      />
      {onTimeZoneChange && (
        <Combobox
          aria-label="Time zone"
          options={zoneOptions}
          value={tz}
          onChange={setZone}
          searchPlaceholder="Search time zones…"
          // w-0 min-w-full: fills the calendar's width without adding to it, so a
          // long zone name truncates instead of widening the popover.
          className="w-0 min-w-full"
        />
      )}
    </PickerShell>
  )
}
