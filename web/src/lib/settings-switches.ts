// The settings editor's on/off switches: each one's key in a settings update,
// its field in the editor's values, and the toast its save shows. A new
// switch is one row here.
export const SETTING_SWITCHES = [
  { key: 'mt_on_upload', field: 'mtOnUpload', on: 'MT on upload enabled', off: 'MT on upload disabled' },
  { key: 'tm_on_upload', field: 'tmOnUpload', on: 'Apply TM on upload enabled', off: 'Apply TM on upload disabled' },
  { key: 'auto_complete', field: 'autoComplete', on: 'Auto-complete enabled', off: 'Auto-complete disabled' },
] as const

type SettingSwitch = (typeof SETTING_SWITCHES)[number]
export type SwitchKey = SettingSwitch['key']
export type SwitchField = SettingSwitch['field']
export type Switches = Record<SwitchField, boolean>

/** The switch values out of a larger values object. */
export function pickSwitches(values: Switches): Switches {
  return Object.fromEntries(SETTING_SWITCHES.map(s => [s.field, values[s.field]])) as Switches
}

/** `prev` with every switch the update sets. */
export function mergeSwitches(prev: Switches, update: Partial<Record<SwitchKey, boolean>>): Switches {
  return Object.fromEntries(SETTING_SWITCHES.map(s => [s.field, update[s.key] ?? prev[s.field]])) as Switches
}

/** The save toast for the first switch the update sets, or null. */
export function describeSwitchSave(update: Partial<Record<SwitchKey, boolean>>): string | null {
  for (const s of SETTING_SWITCHES) {
    const v = update[s.key]
    if (v !== undefined) return v ? s.on : s.off
  }
  return null
}
