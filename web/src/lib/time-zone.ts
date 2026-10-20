import type { ComboboxOption } from '@/components/ui/combobox'

export function browserTimeZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone
}

/** "GMT+2", "GMT-3:30", "GMT" — the zone's UTC offset at `at`. */
export function timeZoneOffsetLabel(timeZone: string, at: Date = new Date()): string {
  const part = new Intl.DateTimeFormat('en-US', { timeZone, timeZoneName: 'shortOffset' })
    .formatToParts(at)
    .find(p => p.type === 'timeZoneName')
  // Some ICU builds say "GMT+0" for UTC.
  const label = part?.value ?? timeZone
  return label === 'GMT+0' ? 'GMT' : label
}

/** "Europe/Berlin" → "Europe / Berlin"; "America/New_York" → "America / New York". */
function timeZoneName(timeZone: string): string {
  return timeZone.replace(/_/g, ' ').replace(/\//g, ' / ')
}

const MINUTE_MS = 60_000

// One list per clock minute, shared by every picker on the page: building it
// formats ~420 zones. A DST switch shows up in the list within a minute.
let cached: { minute: number; options: ComboboxOption[] } | undefined

/**
 * Every IANA zone the browser knows, as Combobox options with the UTC offset
 * at `at` as a chip. The browser's own zone is kept even when the runtime's
 * list leaves it out (e.g. "UTC").
 */
export function timeZoneOptions(at: Date = new Date()): ComboboxOption[] {
  const minute = Math.floor(at.getTime() / MINUTE_MS)
  if (cached?.minute === minute) return cached.options
  const zones = new Set(Intl.supportedValuesOf('timeZone'))
  zones.add(browserTimeZone())
  zones.add('UTC')
  const options = [...zones].sort().map(tz => {
    const offset = timeZoneOffsetLabel(tz, at)
    return {
      value: tz,
      label: timeZoneName(tz),
      chip: offset,
      keywords: `${tz} ${offset}`,
    }
  })
  cached = { minute, options }
  return options
}
