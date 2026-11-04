import { request } from './request'
import type { Choice, DefaultInput, Input, Options, Profile, XLSXRules, XLSXTargetColumn } from '../types/filterprofile'

// Filter profiles: named import options for one file format, the built-ins
// (read-only) and the org's own. Every member reads; owners, admins and
// project managers write.
export type {
  Choice as FilterProfileChoice,
  Options as FilterOptions,
  Profile as FilterProfile,
  XLSXRules as FilterXLSXRules,
  XLSXTargetColumn,
}

const base = '/filter-profiles'

/** With a format: only that format's profiles. */
export const listFilterProfiles = (format?: string) =>
  request<{ data: Profile[] }>(`${base}${format ? `?format=${encodeURIComponent(format)}` : ''}`).then(r => r.data)

export const getFilterProfile = (id: number) =>
  request<{ data: Profile }>(`${base}/${id}`).then(r => r.data)

export const createFilterProfile = (input: Input) =>
  request<{ data: Profile }>(base, { method: 'POST', body: JSON.stringify(input) }).then(r => r.data)

export const updateFilterProfile = (id: number, input: Input) =>
  request<{ data: Profile }>(`${base}/${id}`, { method: 'PUT', body: JSON.stringify(input) }).then(r => r.data)

export const deleteFilterProfile = (id: number) =>
  request<void>(`${base}/${id}`, { method: 'DELETE' })

/** Sets the org default of one format (null: the built-in Default); returns every format's. */
export const setFilterProfileDefault = (input: DefaultInput) =>
  request<{ data: Choice[] }>(`${base}/defaults`, { method: 'PUT', body: JSON.stringify(input) }).then(r => r.data)
