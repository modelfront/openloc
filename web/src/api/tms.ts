import { request, getToken, BASE } from './request';
import { downloadFrom } from './download';
import type { TextSearchOptions } from '@/lib/text-search';

export interface TMCollection {
  id: number;
  name: string;
  created_at: string;
  updated_at: string;
  entry_count?: number;
  // Captured at creation. source_language + target_languages are
  // required for new TMs; legacy TMs created before this feature have them null.
  source_language?: string | null;
  target_languages?: string[] | null;
  customer_id?: number | null;
  // Only populated by the list endpoint (LEFT JOIN customers); null when the
  // TM has no customer.
  customer_name?: string | null;
  // Creator attribution. created_by is the user id (null for TMs created
  // before attribution existed); created_by_name is the resolved display name
  // (LEFT JOIN users, list endpoint only) and is '' when unknown.
  created_by?: number | null;
  created_by_name?: string;
}

export interface CreateTMInput {
  name: string;
  source_language: string;
  target_languages: string[];
  customer_id?: number | null;
}

export interface TMEntry {
  id: number;
  tm_id: number;
  source_language: string;
  target_language: string;
  source_text: string;
  target_text: string;
  // Inline-tag-bearing form for display. Null when the entry has no XLIFF
  // tags (older imports, plain text segments). Frontend's <SegmentText>
  // component prefers rich_text when non-null and falls back to plain text.
  source_rich_text?: string | null;
  target_rich_text?: string | null;
  created_at: string;
  updated_at?: string;
  created_by?: string;
  updated_by?: string;
  tu_id?: string;
  usage_count?: number;
}

// TM Collections
export const listTMCollections = () =>
  request<{ data: TMCollection[] }>('/tms').then(r => r.data);

export const createTMCollection = (input: CreateTMInput) =>
  request<{ data: TMCollection }>('/tms', { method: 'POST', body: JSON.stringify(input) }).then(r => r.data);

export const renameTMCollection = (id: number, name: string) =>
  request<{ data: TMCollection }>(`/tms/${id}`, {
    method: 'PATCH',
    body: JSON.stringify({ name }),
  }).then(r => r.data);

export const deleteTMCollection = (id: number) =>
  request<void>(`/tms/${id}`, { method: 'DELETE' });

// TM Entries
export interface TMLanguagePair {
  source_language: string;
  target_language: string;
}

export const listTMLanguagePairs = (tmId: number) =>
  request<{ data: TMLanguagePair[] }>(`/tms/${tmId}/language-pairs`).then(r => r.data);

export interface TMEntriesPage {
  data: TMEntry[];
  cursor: number;
  has_more: boolean;
  /** Left out by a search on later pages, and when counting ran too long. */
  total?: number;
}

export const listTMEntries = (tmId: number, opts?: { limit?: number; cursor?: number; sourceLang?: string; targetLang?: string; search?: string; searchOptions?: TextSearchOptions }): Promise<TMEntriesPage> => {
  const params = new URLSearchParams()
  if (opts?.limit) params.set('limit', String(opts.limit))
  if (opts?.cursor) params.set('cursor', String(opts.cursor))
  if (opts?.sourceLang) params.set('source_language', opts.sourceLang)
  if (opts?.targetLang) params.set('target_language', opts.targetLang)
  if (opts?.search) params.set('q', opts.search)
  const so = opts?.searchOptions
  if (opts?.search && so) {
    if (so.searchSource !== so.searchTarget) params.set('scope', so.searchSource ? 'source' : 'target')
    if (so.exactMatch) params.set('exact', 'true')
    if (so.caseSensitive) params.set('case_sensitive', 'true')
    if (so.useRegex) params.set('regex', 'true')
  }
  const qs = params.toString()
  return request<TMEntriesPage>(`/tms/${tmId}/entries${qs ? `?${qs}` : ''}`)
}

export const deleteTMEntry = (tmId: number, entryId: number) =>
  request<void>(`/tms/${tmId}/entries/${entryId}`, { method: 'DELETE' });

// One row of TM-entry edit history. Stored BEFORE values — i.e. the row
// captures the entry's state as it was BEFORE the change that produced this
// history record. Read newest-first from the backend (`ORDER BY created_at DESC`).
export interface TMEntryHistory {
  id: number;
  entry_id: number;
  source_text: string;
  target_text: string;
  source_rich_text?: string | null;
  target_rich_text?: string | null;
  changed_by_user_id?: number | null;
  changed_by_email?: string | null;
  change_reason: string;
  created_at: string;
}

export const listTMEntryHistory = (tmId: number, entryId: number) =>
  request<{ data: TMEntryHistory[] }>(`/tms/${tmId}/entries/${entryId}/history`).then(r => r.data);

// ─── TM bulk Find & Replace ────────────────────────────────────────────────

export type TMFRScope = 'source' | 'target' | 'both';

export interface TMFRMatch {
  entry_id: number;
  source_text: string;
  target_text: string;
  new_source_text: string;
  new_target_text: string;
  source_match_count: number;
  target_match_count: number;
}

// One page of matches; the totals cover the whole TM. next_cursor is null on
// the last page — pass it back as `cursor` to load the next one.
export interface TMFRFindResult {
  matches: TMFRMatch[];
  total_entries: number;
  total_replacements: number;
  next_cursor: number | null;
}

export interface TMFRReplaceResult {
  entries_updated: number;
  total_replacements: number;
}

interface TMFRBaseOpts {
  find: string;
  replace: string;
  caseSensitive?: boolean;
  scope?: TMFRScope;
  sourceLanguage?: string;
  targetLanguage?: string;
}

// Preview mode — returns one page of matches, never mutates.
export const tmFindReplaceFind = (
  tmId: number,
  opts: TMFRBaseOpts & { cursor?: number; limit?: number },
): Promise<TMFRFindResult> =>
  request<{ data: TMFRFindResult }>(`/tms/${tmId}/find-replace`, {
    method: 'POST',
    body: JSON.stringify({
      find: opts.find,
      replace: opts.replace,
      case_sensitive: opts.caseSensitive ?? false,
      mode: 'find',
      scope: opts.scope ?? 'both',
      source_language: opts.sourceLanguage ?? '',
      target_language: opts.targetLanguage ?? '',
      cursor: opts.cursor ?? 0,
      limit: opts.limit ?? 0,
    }),
  }).then(r => r.data);

// Apply mode — single transaction, every match updated + history row.
export const tmFindReplaceApply = (tmId: number, opts: TMFRBaseOpts): Promise<TMFRReplaceResult> =>
  request<{ data: TMFRReplaceResult }>(`/tms/${tmId}/find-replace`, {
    method: 'POST',
    body: JSON.stringify({
      find: opts.find,
      replace: opts.replace,
      case_sensitive: opts.caseSensitive ?? false,
      mode: 'replace_all',
      scope: opts.scope ?? 'both',
      source_language: opts.sourceLanguage ?? '',
      target_language: opts.targetLanguage ?? '',
    }),
  }).then(r => r.data);

// Inline edit. Mirrors `updateTranslationHuman` for optimistic locking:
// caller passes the `updated_at` last seen; backend rejects with 409 if the row
// has changed since. `expectedUpdatedAt` may be omitted only by scripted callers.
export const updateTMEntry = (
  tmId: number,
  entryId: number,
  payload: {
    sourceText: string;
    targetText: string;
    sourceRichText?: string | null;
    targetRichText?: string | null;
    expectedUpdatedAt?: string | null;
    changeReason?: string;
  },
) =>
  request<{ data: TMEntry }>(`/tms/${tmId}/entries/${entryId}`, {
    method: 'PATCH',
    body: JSON.stringify({
      source_text: payload.sourceText,
      target_text: payload.targetText,
      source_rich_text: payload.sourceRichText ?? '',
      target_rich_text: payload.targetRichText ?? '',
      ...(payload.expectedUpdatedAt ? { expected_updated_at: payload.expectedUpdatedAt } : {}),
      change_reason: payload.changeReason ?? 'edit',
    }),
  }).then(r => r.data);

// TMX Import/Export
export interface TMImport {
  id: number;
  tm_id: number;
  tenant_id: number;
  file_name: string;
  status: string; // pending, processing, completed, failed
  total_units: number;
  imported: number;
  skipped: number;
  error_message: string | null;
  created_at: string;
  updated_at: string;
}

export const importTMX = (
  tmId: number,
  file: File,
  onUploadProgress?: (pct: number) => void,
): { promise: Promise<TMImport>; abort: () => void } => {
  const xhr = new XMLHttpRequest()
  const promise = new Promise<TMImport>((resolve, reject) => {
    const form = new FormData()
    form.append('file', file)
    xhr.open('POST', `${BASE}/tms/${tmId}/import`)
    const token = getToken()
    if (token && token !== '__cookie_auth__') xhr.setRequestHeader('Authorization', `Bearer ${token}`)
    xhr.withCredentials = true

    if (onUploadProgress) {
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable) {
          onUploadProgress(Math.round((e.loaded / e.total) * 100))
        }
      }
    }

    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          const body = JSON.parse(xhr.responseText)
          resolve(body.data)
        } catch {
          reject(new Error('Invalid response'))
        }
      } else {
        try {
          const err = JSON.parse(xhr.responseText)
          reject(new Error(err.error?.message || xhr.statusText))
        } catch {
          reject(new Error(xhr.statusText))
        }
      }
    }
    xhr.onerror = () => reject(new Error('Upload failed'))
    xhr.onabort = () => reject(new Error('Upload cancelled'))
    xhr.send(form)
  })
  return { promise, abort: () => xhr.abort() }
}

export const listTMImports = (tmId: number) =>
  request<{ data: TMImport[] }>(`/tms/${tmId}/imports`).then(r => r.data);

export const exportTMX = (tmId: number, tmName: string) =>
  downloadFrom(`/tms/${tmId}/export`, `${tmName}.tmx`);

// ─── TM matches (editor sidebar) ───

export interface TMMatch {
  score: number;
  source_text: string;
  target_text: string;
  // Inline-tag-bearing form for display + insert. Null when the TM entry has
  // no XLIFF tags (older imports, plain pairs). EditorSidebar prefers this
  // when present so chips render in the side panel AND get inserted as chips
  // into the editor on click.
  source_rich_text?: string | null;
  target_rich_text?: string | null;
  tm_name: string;
  tm_id: number;
  // The match % as the Match column shows it (101 for an in-context exact
  // match, which needs the translation id in the lookup).
  percent: number;
  // Signed note of this match; a draft save that inserts it records the
  // match on the segment. Absent when the server issues none.
  insert_token?: string;
}

// translationId names the segment, so an exact match can score 101% in context.
export const getTMMatches = (jobId: number, sourceText: string, translationId?: number | null) =>
  request<{ matches: TMMatch[] }>(
    `/jobs/${jobId}/tm-matches?source_text=${encodeURIComponent(sourceText)}${translationId ? `&translation_id=${translationId}` : ''}`,
  ).then(r => r.matches);
