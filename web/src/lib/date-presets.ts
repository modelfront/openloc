import { TZDate } from '@date-fns/tz'
import { addBusinessDays, addDays, addHours, startOfHour } from 'date-fns'

// Quick-pick buttons for DateTimePicker. A preset resolves "now"
// (a TZDate in the picker's time zone, so setHours etc. act on that zone's
// wall clock) into the date to select. Callers pass their own list; the
// helpers below cover the common "in N days" shapes.

export interface DatePreset {
  label: string
  resolve: (now: TZDate) => Date
}

interface RelativeOptions {
  /** Button text. Defaults to "1 day", "2 days", … */
  label?: string
  /** Wall-clock time in the picker's zone, "HH:mm". Defaults to now, rounded up to the next full hour. */
  at?: string
}

/** Sets `at` ("HH:mm") on the date, or rounds it up to the next full hour. */
function applyTime(date: TZDate, at: string | undefined): TZDate {
  if (at) {
    const [h, m] = at.split(':').map(Number)
    const d = new TZDate(date, date.timeZone)
    d.setHours(h, m, 0, 0)
    return d
  }
  const top = startOfHour(date)
  return top.getTime() === date.getTime() ? top : addHours(top, 1)
}

function plural(n: number, unit: string): string {
  return `${n} ${unit}${n === 1 ? '' : 's'}`
}

export function daysFromNow(days: number, opts: RelativeOptions = {}): DatePreset {
  return {
    label: opts.label ?? plural(days, 'day'),
    resolve: now => applyTime(addDays(now, days), opts.at),
  }
}

/** Skips Saturdays and Sundays, counted in the picker's time zone. */
export function businessDaysFromNow(days: number, opts: RelativeOptions = {}): DatePreset {
  return {
    label: opts.label ?? plural(days, 'business day'),
    resolve: now => applyTime(addBusinessDays(now, days), opts.at),
  }
}

export function hoursFromNow(hours: number, opts: Pick<RelativeOptions, 'label'> = {}): DatePreset {
  return {
    label: opts.label ?? plural(hours, 'hour'),
    resolve: now => addHours(now, hours),
  }
}

/** The deadline pickers' default quick picks, at 17:00 in the picker's zone. */
export const DEADLINE_PRESETS: DatePreset[] = [
  daysFromNow(1, { at: '17:00' }),
  daysFromNow(2, { at: '17:00' }),
  daysFromNow(7, { label: '1 week', at: '17:00' }),
]
