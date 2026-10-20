import { request, getToken, logout, BASE } from './request';
import { downloadFrom } from './download';
import type { CreatorRef } from '../lib/creators';
import type { TMLookupOrder } from '../lib/settings-values';
import type { SegmentBulkAction, SegmentBulkResult } from '../lib/segment-bulk';
import type { ReplaceCounts } from '../lib/find-replace';
import type { XLSXLayout } from './xlsx-layout';

// Project lifecycle states. Mirrors the PG enum.
// Keep in sync with `projectStatuses` in internal/project/handler_metadata.go.
export type ProjectStatus = 'draft' | 'in_progress' | 'completed' | 'on_hold';

export const PROJECT_STATUSES: ProjectStatus[] = [
  'draft',
  'in_progress',
  'completed',
  'on_hold',
];

export interface Project {
  id: number;
  name: string;
  description: string;
  source_language: string;
  target_languages: string[];
  template_id: number | null;
  fuzzy_threshold: number;
  sublanguage_penalty_pct?: number;
  near_exact_penalty_pct?: number;
  tm_lookup_order?: string;
  mt_on_upload: boolean;
  tm_on_upload: boolean;
  // Like the fields above, this lives on the project's
  // settings_profile and is flattened into GET /projects/{id}/settings —
  // it is NOT on the plain project list rows.
  auto_complete?: boolean;
  // Strings of JSON, PO, TXT and XLSX uploads are HTML; settings payload only.
  inline_html?: boolean;
  // Library workflow the project's steps were copied from;
  // null for an inline workflow. Only on GET /projects/{id}/settings.
  workflow_id?: number | null;
  workflow_name?: string | null;
  // The MT router the project's settings profile points at (null = no MT).
  mt_router_id?: number | null;
  mt_router_name?: string | null;
  qa_profile_id: number | null;
  created_at: string;
  updated_at: string;
  // Project lifecycle status, customer link, creator.
  // status has a DB default ('in_progress') so it's effectively always set,
  // but typed optional to match the wire shape (older fixtures may omit).
  // customer_id and created_by are nullable in the schema.
  status?: ProjectStatus;
  customer_id?: number | null;
  created_by?: number | null;
  // Resolved creator display name (LEFT JOIN users on the list endpoint).
  // '' when created_by is null or the user row is gone.
  created_by_name?: string;
  /** The project's Customer label value, denormalized for the list row. '' = none. */
  customer_name?: string;
  // Project deadline. RFC 3339 string (e.g. "2026-09-15T17:00:00+02:00"),
  // null when unset. The cron in internal/notification/scheduled.go fires
  // EventDeadline when this is within 24h.
  deadline?: string | null;
  // Optional free-text project taxonomy. null/empty when unset.
  client?: string | null;
  domain?: string | null;
}

export interface FileRecord {
  id: number;
  project_id: number;
  name: string;
  original_name: string | null;
  format: string;
  segment_count: number;
  created_at: string;
  /**
   * Revision stamp, used as the optimistic-lock token for XLIFF re-import
   * (`?revision=` query param). Bumped whenever the
   * `files` row is mutated (rename, segment_count change). Always present
   * — the backend's `files.updated_at` column is NOT NULL with DEFAULT now().
   */
  updated_at: string;
  /** The columns an Excel workbook is read from; null for every other file. */
  xlsx_layout: XLSXLayout | null;
}

export interface Segment {
  id: number;
  file_id: number;
  source_text: string;
  source_rich_text?: string; // source text with inline XLIFF tags (bpt, ept, ph)
  segment_key: string | null;
  position: number;
  /** Source word count, populated at upload time by the parser. Used by the
   *  optimistic leverage-bar adjustment in lib/segment-save.ts so the bar
   *  updates instantly on Cmd+Enter without waiting for the network refetch. */
  word_count: number;
}

export interface Translation {
  id: number;
  segment_id: number;
  target_language: string;
  status: string;
  /** Integer 0..101 (101 = 101% in-context exact). Sticky through human edits. */
  tm_match_percent: number | null;
  predict_outcome: { predict_outcome: string; valid: boolean };
  /** Free-text note from the LLM QE+APE run — currently the model's
   *  0-100 quality score. NULL for extension rows / never-scored segments. */
  qe_note?: string | null;
  active_text: string | null;
  active_rich_text?: string; // current visible target; XLIFF/HTML-tagged form when present
  /** Set by every human write path (confirm/draft/propagation-origin); NULL for
   *  automation-only rows. Drives the derived display status. */
  edited_by?: number | null;
  /** A person changed the text during the job's current workflow step.
   *  Reset when a step starts. Drives the derived display status. */
  step_edited?: boolean;
  locked?: boolean;
  /**
   * Automation-lineage column. Values: 'tm' | 'mt' | 'ape' |
   * 'file_import'. Sticky through human edits — does NOT include 'human' /
   * 'propagation'. Wire shape is {translation_origin, valid} (sqlc
   * NullTranslationOrigin).
   */
  origin?: { translation_origin: string; valid: boolean };
  /** Who confirmed an approved row: a person, or the assistant on a user's
   *  request. Assistant confirms are not written to TM until a person confirms. */
  confirmed_via?: 'user' | 'assistant';
  created_at?: string;
  updated_at?: string; // used for optimistic locking on save
}

// Projects
//
// listProjects: response shape is {data, cursor, has_more} (cursor pagination). This helper extracts only `data` for callers that don't care
// about pagination (ProjectDetail, CATEditor — both find a single project by
// id). The backend's high default limit (10000) makes this safe for any
// realistic tenant. ProjectList uses listProjectsFilteredPage below for the
// full page object so it can drive useInfiniteQuery.
export const listProjects = () =>
  request<{ data: Project[]; cursor: number; has_more: boolean }>('/projects').then(r => r.data);

// listProjectsFilteredPage: returns the FULL page object (data + cursor +
// has_more) so React Query's useInfiniteQuery can drive cursor pagination.
// Filters are all optional; omit any param to skip that filter.
//
// `cursor === 0` means first page; the response's `cursor` is the keyset for
// the NEXT page (or 0 if this was the last page; check `has_more`).
export interface ProjectsPage {
  data: Project[];
  cursor: number;
  has_more: boolean;
}

export interface ProjectListFilters {
  status?: ProjectStatus;
  customer_id?: number;
  source?: string;
  target?: string;
  created_by?: number;
  q?: string;
}

export const listProjectsFilteredPage = (filters: ProjectListFilters, cursor = 0, limit = 50) => {
  const qs = new URLSearchParams();
  qs.set('cursor', String(cursor));
  qs.set('limit', String(limit));
  if (filters.status) qs.set('status', filters.status);
  if (filters.customer_id != null) qs.set('customer_id', String(filters.customer_id));
  if (filters.source) qs.set('source', filters.source);
  if (filters.target) qs.set('target', filters.target);
  if (filters.created_by != null) qs.set('created_by', String(filters.created_by));
  if (filters.q) qs.set('q', filters.q);
  return request<ProjectsPage>(`/projects?${qs.toString()}`);
};

// Distinct creators of this tenant's projects — the option set for the list's
// "Created by" filter. Deliberately NOT /api/members: that includes people who
// never created a project, and each one is a filter option that can only return
// an empty list.
export const listProjectCreators = () =>
  request<{ data: CreatorRef[] }>('/projects/creators').then(r => r.data);

// The New Project wizard's whole form: POST /projects and the draft autosave
// PUT /projects/{id}/draft take the same body.
//
// customer_id, status and deadline are optional — no customer, status
// 'in_progress' (the DB default) and no deadline. `deadline` is a UTC ISO
// string or null; null, "" and omitting it all mean no deadline. Server
// validates.
export interface ProjectCreatePayload {
  // A draft may leave name and languages empty; publish enforces them.
  name: string;
  source_language: string;
  target_languages: string[];
  description?: string;
  customer_id?: number | null;
  status?: ProjectStatus;
  deadline?: string | null;
  // Optional taxonomy. Empty string is treated as "not set" by
  // the backend (trimmed to NULL), so callers can pass either "" or omit.
  client?: string;
  domain?: string;
  // Apply this template's settings + assignments at creation and stamp
  // provenance. Templates are create-time-only — there is no post-create
  // apply endpoint; a draft save only re-stamps the provenance.
  template_id?: number | null;
  // Optional initial configuration, applied atomically with creation so the
  // audit log records ONE project.created event instead of a burst of
  // settings/assignment "changes". Absent lists
  // keep template values / defaults; present lists are full replaces.
  settings?: {
    fuzzy_threshold?: number;
    sublanguage_penalty_pct?: number;
    near_exact_penalty_pct?: number;
    tm_lookup_order?: TMLookupOrder;
    mt_on_upload?: boolean;
    tm_on_upload?: boolean;
    auto_complete?: boolean;
    inline_html?: boolean;
    qa_profile_id?: number | null;
    tm_profile_id?: number;
    mt_router_id?: number;
  };
  tm_assignments?: { tm_id: number; mode: string; priority: number; penalty_pct?: number }[];
  term_assignments?: { term_base_id: number; source_language: string; target_language: string; mode: string; priority: number }[];
  // Library workflow to copy onto the project; workflow_steps (library
  // references) are applied on top as the wizard's tweaks.
  workflow_id?: number;
  workflow_steps?: import('./workflows').WorkflowStepPayload[];
}

export const createProject = (data: ProjectCreatePayload) =>
  request<{ data: Project }>('/projects', { method: 'POST', body: JSON.stringify(data) }).then(r => r.data);

// saveProjectDraft replays the wizard's form onto an existing draft — a full
// replace, not a patch. 404 when the project is no longer a draft.
export const saveProjectDraft = (id: number, data: ProjectCreatePayload) =>
  request<{ data: Project }>(`/projects/${id}/draft`, { method: 'PUT', body: JSON.stringify(data) }).then(r => r.data);

// publishProject turns a draft into an in_progress project: the required
// fields are checked, files uploaded while drafting get their jobs (with
// `analyze` deciding whether they request an automatic analysis), and the
// project's single project.created history entry is filled in.
export const publishProject = (id: number, analyze: boolean) =>
  request<{ data: Project }>(`/projects/${id}/publish`, { method: 'POST', body: JSON.stringify({ analyze }) }).then(r => r.data);

// renameProject: thin alias over the partial-PATCH endpoint that handles
// name-only updates. The route also accepts customer_id, status, and
// description — see updateProjectMetadata below for the full
// surface. Kept as a separate helper so callers can keep the focused name.
export const renameProject = (id: number, name: string) =>
  request<{ data: Project }>(`/projects/${id}`, {
    method: 'PATCH',
    body: JSON.stringify({ name }),
  }).then(r => r.data);

// updateProjectMetadata: partial PATCH. Send any subset of
// {name, description, customer_id, status, deadline}; absent fields keep
// their value. Tri-state for customer_id + deadline: omit to keep, null to
// clear, value to set. Admin-only on the server.
export const updateProjectMetadata = (
  id: number,
  data: {
    name?: string;
    description?: string;
    customer_id?: number | null;
    status?: ProjectStatus;
    deadline?: string | null;
    // Client / domain. Pointer-style on the server distinguishes
    // absent (keep) from present-empty (clear). Pass undefined to keep,
    // "" to clear, or a value to set.
    client?: string;
    domain?: string;
  },
) =>
  request<{ data: Project }>(`/projects/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(data),
  }).then(r => r.data);

export const deleteProject = (id: number) =>
  request<void>(`/projects/${id}`, { method: 'DELETE' });

// Project settings (the project is the core entity)
// TM assignments are per-TM — source/target languages come from the
// TM's own tm_collections metadata, not the assignment.
export interface ProjectTMAssignment {
  id: number;
  settings_profile_id: number;
  tm_id: number;
  mode: string;
  priority: number;
  penalty_pct?: number;
}

export interface ProjectTermAssignment {
  id: number;
  project_id: number;
  term_base_id: number;
  source_language: string;
  target_language: string;
  mode: string;
  priority: number;
}

export interface ProjectSettings {
  project: Project;
  tm_assignments: ProjectTMAssignment[];
  // The TM profile the project follows, or null. When set, tm_assignments and
  // the project's fuzzy_threshold / penalties are the PROFILE's values.
  tm_profile?: import('./tm-profiles').TMProfileRef | null;
  term_assignments: ProjectTermAssignment[];
  workflow_steps: import('../types/generated').WorkflowStep[];
}

export const getProjectSettings = (projectId: number) =>
  request<{ data: ProjectSettings }>(`/projects/${projectId}/settings`).then(r => r.data);

export const updateProjectSettings = (projectId: number, data: { fuzzy_threshold?: number; sublanguage_penalty_pct?: number; near_exact_penalty_pct?: number; tm_lookup_order?: TMLookupOrder; mt_on_upload?: boolean; tm_on_upload?: boolean; auto_complete?: boolean; inline_html?: boolean; qa_profile_id?: number | null; tm_profile_id?: number; mt_router_id?: number | null }) =>
  request<{ data: Project }>(`/projects/${projectId}/settings`, {
    method: 'PATCH',
    body: JSON.stringify(data),
  }).then(r => r.data);

export const setProjectTMAssignments = (projectId: number, assignments: { tm_id: number; mode: string; priority: number }[]) =>
  request<{ data: ProjectTMAssignment[] }>(`/projects/${projectId}/tms`, {
    method: 'PUT',
    body: JSON.stringify({ assignments }),
  }).then(r => r.data);

export const setProjectTermAssignments = (projectId: number, assignments: { term_base_id: number; source_language: string; target_language: string; mode: string; priority: number }[]) =>
  request<{ data: ProjectTermAssignment[] }>(`/projects/${projectId}/terminology`, {
    method: 'PUT',
    body: JSON.stringify({ assignments }),
  }).then(r => r.data);


// Files
export interface FilesPage {
  data: FileRecord[]
  cursor: number
  has_more: boolean
  total: number
}

export const listFiles = (projectId: number, opts?: { limit?: number; cursor?: number }): Promise<FilesPage> => {
  const params = new URLSearchParams()
  if (opts?.limit) params.set('limit', String(opts.limit))
  if (opts?.cursor) params.set('cursor', String(opts.cursor))
  const qs = params.toString()
  return request<FilesPage>(`/projects/${projectId}/files${qs ? `?${qs}` : ''}`)
}

export const renameFile = (projectId: number, fileId: number, name: string) =>
  request<{ data: FileRecord }>(`/projects/${projectId}/files/${fileId}`, {
    method: 'PATCH',
    body: JSON.stringify({ name }),
  }).then(r => r.data);

export const deleteFile = (projectId: number, fileId: number) =>
  request<void>(`/projects/${projectId}/files/${fileId}`, { method: 'DELETE' });

export interface UploadResult {
  data: FileRecord;
  warnings?: string[];
}

export interface UploadFileOptions {
  /** Ask for auto-analysis. The backend coalesces a whole upload into one
   *  pending project analysis that runs once pretranslate settles. */
  analyze?: boolean;
  /** The columns of an Excel workbook without language codes in row 1. */
  xlsxLayout?: XLSXLayout;
}

/**
 * Upload a file to a project. Optionally reports byte-level progress via the
 * `onUploadProgress` callback (0-100, percent of bytes uploaded).
 *
 * Uses `XMLHttpRequest`, not `fetch`: `fetch` exposes no upload progress
 * events. Same XHR pattern as the importTMX / importGlossary helpers.
 * Returns a plain Promise<UploadResult> (no abort handle) to keep the API
 * surface narrow.
 */
export const uploadFile = (
  projectId: number,
  file: File,
  onUploadProgress?: (pct: number) => void,
  { analyze, xlsxLayout }: UploadFileOptions = {},
): Promise<UploadResult> => {
  return new Promise<UploadResult>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    const form = new FormData();
    if (analyze) {
      form.append('analyze', 'true');
    }
    if (xlsxLayout) {
      form.append('xlsx_layout', JSON.stringify(xlsxLayout));
    }
    form.append('file', file);
    xhr.open('POST', `${BASE}/projects/${projectId}/files`);
    const token = getToken();
    if (token && token !== '__cookie_auth__') xhr.setRequestHeader('Authorization', `Bearer ${token}`);
    xhr.withCredentials = true;

    if (onUploadProgress) {
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable) {
          onUploadProgress(Math.round((e.loaded / e.total) * 100));
        }
      };
    }

    xhr.onload = () => {
      if (xhr.status === 401) {
        logout();
        reject(new Error('Session expired'));
        return;
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          resolve(JSON.parse(xhr.responseText) as UploadResult);
        } catch {
          reject(new Error('Invalid response'));
        }
        return;
      }
      // Surface the backend's error message when present.
      try {
        const body = JSON.parse(xhr.responseText);
        reject(new Error(body.error?.message || 'Upload failed'));
      } catch {
        reject(new Error('Upload failed'));
      }
    };
    xhr.onerror = () => reject(new Error('Upload failed'));
    xhr.onabort = () => reject(new Error('Upload cancelled'));
    xhr.send(form);
  });
};

// Segments
export const listSegments = (fileId: number) =>
  request<{ data: Segment[] }>(`/files/${fileId}/segments`).then(r => r.data);

// Translations
export const listTranslations = (fileId: number, lang: string) =>
  request<{ data: Translation[] }>(`/files/${fileId}/translations?lang=${lang}`).then(r => r.data);

/**
 * Every endpoint that writes translation text answers `task_reopened: true`,
 * beside its payload, when the write put the job's completed task back in
 * progress (a manager editing a finished task). The field is absent otherwise.
 * Callers reload the task on it — see `lib/task-edit.ts`.
 */
export interface TaskReopenedFlag {
  task_reopened?: boolean;
}

export const taskReopenedOf = (r: TaskReopenedFlag): boolean => r.task_reopened === true;

/** A translation write that answers the saved row. */
export interface SavedTranslation {
  translation: Translation;
  taskReopened: boolean;
}

/**
 * Result of confirming a translation. Bundles the saved entity with
 * side-effect metadata so callers can drive cache invalidation precisely:
 *
 *   - `translation` — the row as it exists post-save (consumed by
 *     `applySegmentSaveSuccess` to advance the segment cache + reset
 *     baselines).
 *   - `updatedTmIds` — IDs of TMs that received this entry.
 *     Empty array when no TMs were configured
 *     OR when all TM upserts errored. Frontend uses these to invalidate
 *     the right TM browser caches without a heuristic guess. Always
 *     present (the backend never omits the field) so callers can iterate
 *     without null-checks.
 *   - `taskReopened` — see `TaskReopenedFlag`.
 */
export interface UpdateHumanResult extends SavedTranslation {
  updatedTmIds: number[];
}

// Optimistic locking: pass `expectedUpdatedAt` (the `updated_at`
// the client last saw) so the backend can detect concurrent edits and return
// 409 instead of silently overwriting another user's work. Omitting it falls
// back to "last writer wins" (used by find-replace and other bulk operations).
export const updateTranslationHuman = (
  fileId: number,
  translationId: number,
  humanText: string,
  editingTimeMs?: number,
  humanRichText?: string,
  expectedUpdatedAt?: string | null,
): Promise<UpdateHumanResult> =>
  // `updated_tm_ids` is always sent (even as `[]`), so it is typed as
  // required: a dropped field fails typecheck instead of silently skipping
  // TM cache invalidation. The `?? []` below is defense in depth.
  request<{ data: Translation; updated_tm_ids: number[] } & TaskReopenedFlag>(`/files/${fileId}/translations/${translationId}`, {
    method: 'PUT',
    body: JSON.stringify({
      human_text: humanText,
      editing_time_ms: editingTimeMs ?? 0,
      ...(humanRichText ? { human_rich_text: humanRichText } : {}),
      ...(expectedUpdatedAt ? { expected_updated_at: expectedUpdatedAt } : {}),
    }),
  }).then(r => ({
    translation: r.data,
    updatedTmIds: r.updated_tm_ids ?? [],
    taskReopened: taskReopenedOf(r),
  }));

export const lockTranslation = (fileId: number, translationId: number) =>
  request<{ status: string }>(`/files/${fileId}/translations/${translationId}/lock`, { method: 'POST' });

export const unlockTranslation = (fileId: number, translationId: number) =>
  request<{ status: string }>(`/files/${fileId}/translations/${translationId}/unlock`, { method: 'POST' });

/** Lock, unlock or clear a selection of the job's segments (at most SEGMENT_BULK_MAX ids). */
export const bulkSegmentAction = (jobId: number, action: SegmentBulkAction, translationIds: number[]) =>
  request<{ data: SegmentBulkResult }>(`/jobs/${jobId}/translations/${action}`, {
    method: 'POST',
    body: JSON.stringify({ translation_ids: translationIds }),
  }).then(r => r.data);

/**
 * Confirm every translated, unapproved segment of the job — "Approve all and
 * complete" in the Complete step dialog. Same per-row rules as a bulk confirm;
 * rows a QA error blocks come back in `skipped`.
 */
export const confirmAllTranslations = (jobId: number) =>
  request<{ data: SegmentBulkResult }>(`/jobs/${jobId}/translations/confirm-all`, { method: 'POST' }).then(r => r.data);

export type ClearScope = 'non_confirmed' | 'all';

export const clearTranslation = (fileId: number, translationId: number): Promise<SavedTranslation> =>
  request<{ data: Translation } & TaskReopenedFlag>(`/files/${fileId}/translations/${translationId}/clear`, { method: 'POST' })
    .then(r => ({ translation: r.data, taskReopened: taskReopenedOf(r) }));

// held: segments kept because someone else has them open.
export const clearJobTranslations = (jobId: number, scope: ClearScope) =>
  request<{ cleared: number; held: number }>(`/jobs/${jobId}/clear-translations`, {
    method: 'POST',
    body: JSON.stringify({ scope }),
  });

export const clearProjectTranslations = (projectId: number, scope: ClearScope) =>
  request<{ cleared: number; held: number }>(`/projects/${projectId}/clear-translations`, {
    method: 'POST',
    body: JSON.stringify({ scope }),
  });

// Comments
export interface SegmentComment {
  id: number;
  translation_id: number;
  user_id: number;
  author_name: string;
  author_email: string;
  content: string;
  created_at: string;
}

export const listComments = (translationId: number) =>
  request<{ data: SegmentComment[] }>(`/translations/${translationId}/comments`).then(r => r.data);

export const createComment = (translationId: number, content: string) =>
  request<{ data: SegmentComment }>(`/translations/${translationId}/comments`, {
    method: 'POST', body: JSON.stringify({ content }),
  }).then(r => r.data);

export const deleteComment = (translationId: number, commentId: number) =>
  request<void>(`/translations/${translationId}/comments/${commentId}`, { method: 'DELETE' });

export const getCommentCounts = (fileId: number, lang: string) =>
  request<{ data: { translation_id: number; comment_count: number }[] }>(`/files/${fileId}/comment-counts?lang=${lang}`).then(r => r.data);

// See `updateTranslationHuman` for the `expectedUpdatedAt` contract.
// `insertToken` is the insert token of a Resources-panel TM match or MT
// suggestion this draft inserts; the server then records that TM / MT event.
export const saveTranslationDraft = (
  fileId: number,
  translationId: number,
  humanText: string,
  editingTimeMs?: number,
  humanRichText?: string,
  expectedUpdatedAt?: string | null,
  insertToken?: string,
): Promise<SavedTranslation> =>
  request<{ data: Translation } & TaskReopenedFlag>(`/files/${fileId}/translations/${translationId}/draft`, {
    method: 'PUT',
    body: JSON.stringify({
      human_text: humanText,
      editing_time_ms: editingTimeMs ?? 0,
      ...(humanRichText ? { human_rich_text: humanRichText } : {}),
      ...(expectedUpdatedAt ? { expected_updated_at: expectedUpdatedAt } : {}),
      ...(insertToken ? { suggestion: insertToken } : {}),
    }),
  }).then(r => ({ translation: r.data, taskReopened: taskReopenedOf(r) }));

/**
 * Project-level pre-translation fan-out.
 * Triggers pre-translate for every job in the project that isn't already
 * running or completed. Returns counts so the caller can render an honest
 * toast like "Queued 4 jobs (2 already running)".
 *
 * Server returns HTTP 200 in sync mode (no enqueuer wired) or 202 in
 * async mode. The FE doesn't differentiate — the response body is the
 * same shape; the toast wording is identical for both.
 */
export interface ProjectPretranslateResult {
  queued: number;
  skipped: number;
  total: number;
}

export const pretranslateProject = (projectId: number) =>
  request<ProjectPretranslateResult>(`/projects/${projectId}/pretranslate`, { method: 'POST' });

/**
 * Format options for the file export endpoint.
 *
 * - `'source'` (default): mirror the source format. DOCX → DOCX,
 *   JSON → JSON, etc. Reconstructs the original file with translations
 *   injected; intended as the customer deliverable.
 * - `'xliff'`: bilingual XLIFF 1.2 with both `<source>` and `<target>`
 *   for every segment, regardless of source format. Used for handing off
 *   to external translators in other CAT tools. The trans-unit `id` equals
 *   `segments.segment_key` so re-importing what they send back
 *   round-trips cleanly.
 */
export type ExportFormat = 'source' | 'xliff';

// Export
export const exportFile = (fileId: number, lang: string, format: ExportFormat = 'source') => {
  // `format=source` and the omitted form are equivalent on the backend.
  const qs = format === 'source' ? `lang=${lang}` : `lang=${lang}&format=${format}`;
  return downloadFrom(`/files/${fileId}/export?${qs}`, 'export.json');
};

// Job completion: marks the job completed and saves its approved segments to TM.
export interface CompleteJobResponse {
  message: string;
  job_id: number;
  approved_count: number;
  tm_saved_count: number;
  warning?: string;
}

export const completeJob = (jobId: number) =>
  request<CompleteJobResponse>(`/jobs/${jobId}/complete`, { method: 'POST' });

// ─── Auto-propagation ───

// `humanRichText` carries the XLIFF rich-text form so identical-source
// siblings inherit the same tag chips.
//
// The response carries the full updated row for each affected sibling, so the
// caller patches its React Query translations cache synchronously. Waiting for
// the `invalidateJobContext` refetch is not enough: a user re-activating a
// propagated sibling before it lands captures a stale `baselineUpdatedAtRef`,
// then 409s on the next save.
export const propagateTranslation = (
  fileId: number,
  sourceText: string,
  targetLanguage: string,
  humanText: string,
  excludeId: number,
  humanRichText?: string,
) =>
  // held: repetitions another user has open, left as they were.
  request<{ data: { propagated: number; held: number; siblings: Translation[] } } & TaskReopenedFlag>(`/files/${fileId}/propagate`, {
    method: 'POST',
    body: JSON.stringify({
      source_text: sourceText,
      target_language: targetLanguage,
      human_text: humanText,
      exclude_id: excludeId,
      ...(humanRichText ? { human_rich_text: humanRichText } : {}),
    }),
  }).then(r => ({ ...r.data, taskReopened: taskReopenedOf(r) }));

// ─── Find & Replace ───

/** What a replace mode of find-replace answers: what was written, and the
 *  matching segments left alone. Replace writes drafts and keeps inline tags. */
export interface ReplaceResult extends ReplaceCounts {
  taskReopened: boolean;
}

const replaceRequest = (jobId: number, body: Record<string, unknown>): Promise<ReplaceResult> =>
  request<{ data: ReplaceCounts } & TaskReopenedFlag>(`/jobs/${jobId}/find-replace`, {
    method: 'POST',
    body: JSON.stringify(body),
  }).then(r => ({ ...r.data, taskReopened: taskReopenedOf(r) }));

/** How find-replace matches. `includeLocked` adds locked segments: find lists
 *  them; replace writes them only for owner/admin (the server decides). */
export interface FindOptions {
  caseSensitive: boolean;
  includeLocked: boolean;
}

const findBody = (find: string, opts: FindOptions) =>
  ({ find, case_sensitive: opts.caseSensitive, include_locked: opts.includeLocked });

/** `translationIds` limits the replace to those rows (at most SEGMENT_BULK_MAX). */
export const findReplace = (jobId: number, find: string, replace: string, opts: FindOptions, translationIds?: number[]) =>
  replaceRequest(jobId, {
    ...findBody(find, opts), replace, mode: 'replace_all',
    ...(translationIds ? { translation_ids: translationIds } : {}),
  });

/** `translationIds` limits the search to those rows (at most SEGMENT_BULK_MAX). */
export const findMatches = (jobId: number, find: string, opts: FindOptions, translationIds?: number[]) =>
  request<{ data: { matches: number; segments: number; translation_ids: number[] } }>(`/jobs/${jobId}/find-replace`, {
    method: 'POST',
    body: JSON.stringify({
      ...findBody(find, opts), mode: 'find',
      ...(translationIds ? { translation_ids: translationIds } : {}),
    }),
  }).then(r => r.data);

export const replaceOne = (jobId: number, find: string, replace: string, opts: FindOptions, translationId: number) =>
  replaceRequest(jobId, { ...findBody(find, opts), replace, mode: 'replace_one', translation_id: translationId });

// ─── Deadlines ───

// The deadline lives on the step assignment. `stepAssignmentId` is
// the active step's assignment id (job.workflow_step.id). `deadline` is ISO
// 8601, or null to clear.
export const setStepDeadline = (jobId: number, stepAssignmentId: number, deadline: string | null) =>
  request(`/jobs/${jobId}/workflow/steps/${stepAssignmentId}/deadline`, {
    method: 'PATCH',
    body: JSON.stringify({ deadline }),
  });
