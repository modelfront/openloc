// User language-pair API client.
//
// Pairs are tenant-scoped via the user. Server normalizes language codes
// (lowercase, region subtags preserved e.g. en-us) — frontend can pass any
// case but should expect the canonical lowercase form on read.

import { request } from './request'

export interface LanguagePair {
  source_lang: string
  target_lang: string
  // Which editor row the pair belongs to — lets the client round-trip the
  // row grouping. Optional/0 for legacy pairs (they collapse into one row).
  row_group?: number
}

export interface QualifiedUser {
  id: number
  email: string
  name: string
  role: string
}

export const listUserLanguagePairs = (userId: number) =>
  request<{ data: LanguagePair[] }>(`/users/${userId}/language-pairs`).then(r => r.data)

// PUT replace semantics — pass the FULL pair set; server clears + bulk inserts in one tx.
// Empty array clears all pairs for the user.
export const replaceUserLanguagePairs = (userId: number, pairs: LanguagePair[]) =>
  request<{ data: LanguagePair[] }>(`/users/${userId}/language-pairs`, {
    method: 'PUT',
    body: JSON.stringify({ pairs }),
  }).then(r => r.data)

// Used by the workflow assignment dropdown. Returns members in caller's
// tenant qualified for the given pair. Both query params are required.
export const listUsersByLanguagePair = (source: string, target: string) => {
  const qs = new URLSearchParams({ source, target }).toString()
  return request<{ data: QualifiedUser[] }>(`/users/by-language-pair?${qs}`).then(r => r.data)
}
