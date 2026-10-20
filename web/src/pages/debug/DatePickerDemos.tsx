// Gallery demos for DateTimePicker (DebugPage).
// Each one prints its raw value underneath, so what the caller receives is visible.

import { useState } from 'react'
import { DateTimePicker, type DateTimePickerProps } from '@/components/ui/date-picker'
import { businessDaysFromNow, daysFromNow, DEADLINE_PRESETS, hoursFromNow, type DatePreset } from '@/lib/date-presets'
import { browserTimeZone } from '@/lib/time-zone'

function Value({ children }: { children: string }) {
  return <span className="font-mono text-xs text-muted-foreground">{children || '(empty)'}</span>
}

const EOD_PRESETS: DatePreset[] = [
  hoursFromNow(4),
  daysFromNow(0, { label: 'Today 17:00', at: '17:00' }),
  daysFromNow(1, { label: 'Tomorrow 17:00', at: '17:00' }),
  businessDaysFromNow(3, { at: '17:00' }),
]

type DateTimeDemoProps = Pick<DateTimePickerProps, 'disablePast' | 'defaultTime'> & {
  presets?: 'deadline' | 'eod'
  withTimeZone?: boolean
}

export function DateTimePickerDemo({ presets, withTimeZone, ...props }: DateTimeDemoProps) {
  const [value, setValue] = useState<Date | null>(null)
  const [tz, setTz] = useState(browserTimeZone())
  return (
    <div className="flex items-center gap-3">
      <DateTimePicker
        {...props}
        value={value}
        onChange={setValue}
        presets={presets === 'deadline' ? DEADLINE_PRESETS : presets === 'eod' ? EOD_PRESETS : undefined}
        timeZone={withTimeZone ? tz : undefined}
        onTimeZoneChange={withTimeZone ? setTz : undefined}
        aria-label="Demo date and time"
      />
      <Value>{value ? `${value.toISOString()}${withTimeZone ? ` · ${tz}` : ''}` : ''}</Value>
    </div>
  )
}
