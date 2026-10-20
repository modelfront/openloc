import { request } from './request'
import type { Palette } from '../lib/palettes'

// Per-user appearance. `light_palette` / `dark_palette` are palette
// ids (built-in or a custom palette's id) active in each mode; `custom_palettes`
// are the user's own palettes. Keyed by user server-side, so it follows the
// user across orgs.
export interface Appearance {
  light_palette: string
  dark_palette: string
  custom_palettes: Palette[]
}

export const getAppearance = () =>
  request<{ data: Appearance }>('/appearance').then(r => r.data)

export const updateAppearance = (a: Appearance) =>
  request<{ data: Appearance }>('/appearance', {
    method: 'PUT',
    body: JSON.stringify(a),
  }).then(r => r.data)
