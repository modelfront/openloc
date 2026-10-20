import { request } from './request';
import { downloadFrom, downloadJobExport } from './download';
// `ExportFormat` is defined once in api/projects.ts (the file-level export
// path) and re-used here by the job-level path so both surfaces share
// one enum and one mental model. See ExportFormat docstring there.
import type { Translation, ExportFormat } from './projects';
import type { StepType } from './workflows';

// Jobs
export interface JobWorkflowStep {
  // id is the active step ASSIGNMENT id — the target for deadline edits
  // (PATCH /api/jobs/{jobId}/workflow/steps/{id}/deadline).
  id: number;
  step_name: string;
  // step_order lets row-level views (project page) gate first-step-only
  // actions (Apply TM / MT / Predict). The CAT editor doesn't need this
  // field — it loads the full steps list — but every consumer of `Job`
  // benefits from it being present.
  step_order: number;
  assigned_to?: number;
  assignee_name?: string;
  status: string;
  is_last_step: boolean;
  // True when the active step carries a QE+APE AI engine — gates the
  // manager "Re-run AI step" button in JobPipelineActions.
  is_ai_step: boolean;
  // human / hybrid / ai / pretranslate. Optional only for fixtures that
  // predate the field; the API always sends it.
  step_type?: StepType;
  // Deadline lives on the active assignment. ISO 8601 or null.
  deadline: string | null;
}

export type FailedPhase = 'ai_step' | 'pretranslation';

export interface Job {
  id: number;
  file_id: number;
  target_language: string;
  word_count: number;
  // Project context rides along with the job so the CAT editor works for
  // linguists too — they're barred from GET /api/projects (RequireManager).
  project_id: number;
  project_name: string;
  source_language: string;
  file_name: string;
  file_format: string;
  // False when the file's strings are plain text ("Strings contain HTML" off).
  file_inline_html?: boolean;
  file_created_at: string;
  segment_count: number;
  status: string;
  pipeline_status: string;
  current_step?: string;
  error_message?: string;
  // Which automation a failed job stopped in, and so what Re-run runs again.
  // Present only while pipeline_status is 'failed'.
  failed_phase?: FailedPhase;
  // Why the last pre-translation asked for MT and got none: the MT router has
  // no engine for this language, or an AI engine that can't translate it.
  mt_skip_reason?: 'no_route' | 'no_provider';
  // Set while a cooperative stop is pending on a running pipeline (ISO
  // timestamp). Cleared once the run lands on pipeline_status 'stopped'.
  stop_requested_at?: string | null;
  translated_count?: number;
  total_count?: number;
  workflow_step?: JobWorkflowStep;
  counts: {
    total: number;
    untranslated: number;
    pre_translated: number;
    auto_approved: number;
    needs_review: number;
    approved: number;
    // Exact clear counts (locked + empty rows excluded) — used by the
    // Clear-translations dialog to show the real number, not "up to N".
    clearable_non_confirmed: number;
    clearable_all: number;
  };
  created_at: string;
  updated_at: string;
}

export const listJobs = (projectId: number) =>
  request<{ data: Job[] }>(`/projects/${projectId}/jobs`).then(r => r.data);

export const getJob = (jobId: number) =>
  request<{ data: Job }>(`/jobs/${jobId}`).then(r => r.data);

export const listJobTranslations = (jobId: number) =>
  request<{ data: Translation[] }>(`/jobs/${jobId}/translations`).then(r => r.data);

// Wire literal must match pipeline.PhaseSkipNoSegments in
// internal/pipeline/translate.go.
export interface ApplyPhaseResponse {
  message: string;
  job_id: number;
  skip_reason?: 'no_segments';
  // True when the run was stopped mid-way (POST /jobs/{id}/stop); the
  // segments translated so far are kept.
  stopped?: boolean;
  // See `TaskReopenedFlag` in api/projects.ts.
  task_reopened?: boolean;
  // Segments skipped because someone else has them open.
  held?: number;
}

const applyPhase = (jobId: number, phase: 'tm' | 'mt', translationIds?: number[]) =>
  request<ApplyPhaseResponse>(`/jobs/${jobId}/${phase}-apply`, {
    method: 'POST',
    ...(translationIds ? { body: JSON.stringify({ translation_ids: translationIds }) } : {}),
  });

/** Apply TM on the whole job, or on the given rows (at most SEGMENT_BULK_MAX). */
export const applyJobTM = (jobId: number, translationIds?: number[]) => applyPhase(jobId, 'tm', translationIds);

/** Apply MT on the whole job, or on the given rows (at most SEGMENT_BULK_MAX). */
export const applyJobMT = (jobId: number, translationIds?: number[]) => applyPhase(jobId, 'mt', translationIds);

/** One answer for a run sent in chunks: stopped or reopened if any chunk was,
 *  "nothing to do" only if every chunk had nothing to do, held added up. */
export function mergeApplyResponses(parts: readonly ApplyPhaseResponse[]): ApplyPhaseResponse {
  return {
    message: parts[parts.length - 1]?.message ?? '',
    job_id: parts[0]?.job_id ?? 0,
    stopped: parts.some(p => p.stopped) || undefined,
    task_reopened: parts.some(p => p.task_reopened) || undefined,
    skip_reason: parts.length > 0 && parts.every(p => p.skip_reason === 'no_segments') ? 'no_segments' : undefined,
    held: parts.reduce((n, p) => n + (p.held ?? 0), 0),
  };
}

// Re-run the job's active QE+APE workflow step.
// Manager-gated.
export const runJobAIStep = (jobId: number) =>
  request<ApplyPhaseResponse>(`/jobs/${jobId}/ai-step`, { method: 'POST' });

// MT preview for a single source segment. When translationId is passed and
// the pipeline already MT'd that segment, the backend returns the stored
// output (stored=true) instead of live-calling the engine — with AI-as-MT
// a live call is a paid LLM round trip per segment click. Segments without
// stored MT are fetched live on demand and not persisted. Cached client-side via React Query staleTime.
export const getMTPreview = (jobId: number, sourceText: string, translationId?: number | null) =>
  request<{ text: string; rich_text?: string; engine: string; stored?: boolean; insert_token?: string }>(
    `/jobs/${jobId}/mt-preview?source_text=${encodeURIComponent(sourceText)}${translationId ? `&translation_id=${translationId}` : ''}`,
  );

export const exportJob = (jobId: number, format: ExportFormat = 'source') =>
  downloadJobExport(jobId, format);

export const deleteJob = (jobId: number) =>
  request<void>(`/jobs/${jobId}`, { method: 'DELETE' });

// Runs a failed job's automation again — the AI step or pre-translation, per
// `failed_phase`. 202 once queued; 409 when the job is not failed or already
// running; 403 for a non-manager on an AI-step error.
export const rerunJob = (jobId: number) =>
  request<{ status: 'queued'; phase: FailedPhase }>(`/jobs/${jobId}/rerun`, { method: 'POST' });

// Accepts a failed AI run on a hybrid step: the error is cleared and the step
// stays in progress without the AI pass. 204; 403 for a non-manager; 409 when
// the job has no AI-step error or its step is not hybrid.
export const skipJobAIStep = (jobId: number) =>
  request<void>(`/jobs/${jobId}/skip-ai-step`, { method: 'POST' });

// Cooperative stop of the running pipeline: lands at the next chunk boundary.
// 202 while the stop is pending; 409 when nothing is running or a stop is
// already pending. Same access as Apply TM/MT (managers + assigned linguist).
export const stopJob = (jobId: number) =>
  request<{ status: 'stopping'; job_id: number }>(`/jobs/${jobId}/stop`, { method: 'POST' });

// --- Analysis ---

export interface MatchBand {
  segments: number;
  words: number;
  characters: number;
  weighted_words: number;
  weight_pct: number;
}

export interface QPBreakdown {
  approved: MatchBand;
  ape_approved: MatchBand;
  ape_needs_review: MatchBand;
  rejected: MatchBand;
  not_run: MatchBand;
  saved_from_review: MatchBand;
}

export interface AnalysisResponse {
  tm_exact: MatchBand;
  tm_fuzzy_95: MatchBand;
  tm_fuzzy_85: MatchBand;
  tm_fuzzy_75: MatchBand;
  tm_fuzzy_50: MatchBand;
  qe: MatchBand;
  new: MatchBand;
  total: MatchBand;
  qp: QPBreakdown;
}

export interface ProjectAnalysisResponse {
  languages: Record<string, AnalysisResponse>;
  total: AnalysisResponse;
}

export interface FileAnalysis {
  file_id: number;
  file_name: string;
  target_language: string;
  analysis: AnalysisResponse;
  repetitions: MatchBand;
}

export interface ProjectAnalysisV2Response {
  files: FileAnalysis[];
  languages: Record<string, AnalysisResponse>;
  total: AnalysisResponse;
  total_repetitions: MatchBand;
}

export const getJobAnalysis = (jobId: number) =>
  request<{ data: AnalysisResponse }>(`/jobs/${jobId}/analysis`).then(r => r.data);

export const getProjectAnalysis = (projectId: number) =>
  request<{ data: ProjectAnalysisResponse }>(`/projects/${projectId}/analysis`).then(r => r.data);

export const getProjectAnalysisV2 = (projectId: number) =>
  request<{ data: ProjectAnalysisV2Response }>(`/projects/${projectId}/analysis/v2`).then(r => r.data);

// CSV export of project analysis, in the standard CAT analysis layout
// (one row per file × language).
// Backend emits the same numbers the V2 endpoint serves — they share
// `computeProjectAnalysis` so spreadsheet and on-screen tab can't drift.
//
// Filename comes from the Content-Disposition header (server-generated:
// "<project>-analysis-<date>.csv").
export const exportProjectAnalysisCSV = (projectId: number) =>
  downloadFrom(`/projects/${projectId}/analysis.csv`, 'analysis.csv');

// --- Analytics Weights ---

export interface WeightEntry {
  band: string;
  weight_pct: number;
}

export const getAnalyticsWeights = () =>
  request<{ data: WeightEntry[] }>('/analytics/weights').then(r => r.data);

export const updateAnalyticsWeights = (weights: WeightEntry[]) =>
  request<{ data: WeightEntry[] }>('/analytics/weights', {
    method: 'PUT',
    body: JSON.stringify({ weights }),
  }).then(r => r.data);

// --- Productivity ---

export interface ProductivityEntry {
  user_id: number;
  user_name: string;
  target_language?: string;
  segments_edited: number;
  words_edited: number;
  total_editing_time_ms: number;
  words_per_hour: number;
  avg_edit_distance: number;
  avg_ter?: number;
}

export const getProjectProductivity = (projectId: number) =>
  request<{ data: ProductivityEntry[] }>(`/projects/${projectId}/productivity`).then(r => r.data);

// Per-workflow-step productivity. Same metrics as
// ProductivityEntry plus the step identity, read from translation_history so
// every step's contribution is visible (not just the last editor).
export interface StepProductivityEntry extends ProductivityEntry {
  step_assignment_id: number;
  step_name: string;
  step_order: number;
}

export const getProjectStepProductivity = (projectId: number) =>
  request<{ data: StepProductivityEntry[] }>(`/projects/${projectId}/productivity/by-step`).then(r => r.data);

// ─── Workflows ───

export interface JobStepAssignment {
  id: number;
  job_id: number;
  workflow_step_id: number;
  assigned_to: number | null;
  status: string;     // pending, in_progress, completed, rejected
  started_at: string | null;
  completed_at: string | null;
  created_at: string;
  step_name: string;
  step_order: number;
  role_required: string;
  step_type: StepType;
  /** The hybrid or AI step's AI router; null on other steps. */
  ai_router_id: number | null;
  assignee_name: string | null;
  assignee_email: string | null;
  rejection_reason: string;
  /** The comment written when the step before was completed, for whoever
   *  works this step; null when that hand-off came without one. */
  handoff_note: string | null;
  /** When this step is due for this job, ISO 8601 (the deadline lives
   *  on the assignment, resolved for the job's target language). */
  deadline: string | null;
}

export const listJobSteps = (jobId: number) =>
  request<{ data: JobStepAssignment[] }>(`/jobs/${jobId}/workflow/steps`).then(r => r.data);

/** One job's assignment for one workflow step — a cell of the project task matrix. */
export interface ProjectTask {
  /** job_step_assignments.id — the target for assign / deadline edits. */
  id: number;
  job_id: number;
  workflow_step_id: number;
  status: 'pending' | 'in_progress' | 'completed' | 'rejected';
  deadline: string | null;
  assigned_to: number | null;
  assignee_name: string | null;
  completed_at: string | null;
  rejection_reason: string;
  /** The comment this task was handed by the step before; null when none. */
  handoff_note: string | null;
  step_name: string;
  step_order: number;
  step_type: StepType;
}

/** Every step assignment of every job in the project. Manager-only. */
export const listProjectTasks = (projectId: number) =>
  request<{ data: ProjectTask[] }>(`/projects/${projectId}/tasks`).then(r => r.data);

/** The server refuses a hand-off comment or a rejection reason longer than this. */
export const MAX_NOTE_LENGTH = 4000;

/** Completes the job's current step. `comment` is the hand-off note shown to
 *  whoever works the next step. */
export const advanceJobStep = (jobId: number, comment?: string) =>
  request<{ data: { status: string; message?: string; next_step_id?: number } }>(`/jobs/${jobId}/workflow/advance`, {
    method: 'POST',
    body: JSON.stringify({ comment: comment ?? '' }),
  }).then(r => r.data);

export interface RejectStepInput {
  reason?: string;
  /** The step assignment to reopen; omitted = the nearest earlier human or
   *  hybrid step. */
  toStepId?: number;
  /** Run the reopened hybrid step's AI again (managers). An AI step always
   *  runs again. */
  rerunAI?: boolean;
}

/** Sends the job back to an earlier step. */
export const rejectJobStep = (jobId: number, input: RejectStepInput = {}) =>
  request<{ data: { status: string; reopened_step_id?: number; rerun_ai?: boolean } }>(`/jobs/${jobId}/workflow/reject`, {
    method: 'POST',
    body: JSON.stringify({ reason: input.reason ?? '', to_step_id: input.toStepId, rerun_ai: input.rerunAI ?? false }),
  }).then(r => r.data);

export const assignJobStep = (jobId: number, stepId: number, userId: number) =>
  request<{ data: { assigned: boolean } }>(`/jobs/${jobId}/workflow/steps/${stepId}/assign`, {
    method: 'POST',
    body: JSON.stringify({ user_id: userId }),
  }).then(r => r.data);

// ─── Notifications ───

export interface Notification {
  id: number;
  tenant_id: number;
  user_id: number;
  event_type: string;
  subject: string;
  body_preview: string;
  reference_type: string;
  reference_id: number;
  read: boolean;
  emailed: boolean;
  created_at: string;
}

export interface NotificationPreferences {
  email_assignment: boolean;
  email_rejection: boolean;
  email_completion: boolean;
  email_deadline: boolean;
  email_issue_updates: boolean;
}

export const listNotifications = (opts?: { unread?: boolean; cursor?: number; limit?: number }) => {
  const params = new URLSearchParams();
  if (opts?.unread) params.set('unread', 'true');
  if (opts?.cursor) params.set('cursor', String(opts.cursor));
  if (opts?.limit) params.set('limit', String(opts.limit));
  const qs = params.toString();
  return request<{ data: Notification[]; cursor: number; has_more: boolean }>(`/notifications${qs ? '?' + qs : ''}`).then(r => r);
};

export const getUnreadCount = () =>
  request<{ count: number }>('/notifications/count').then(r => r.count);

export const markNotificationRead = (id: number) =>
  request(`/notifications/${id}/read`, { method: 'PATCH' });

export const markAllNotificationsRead = () =>
  request('/notifications/read-all', { method: 'POST' });

export const getNotificationPreferences = () =>
  request<{ data: NotificationPreferences }>('/notification-preferences').then(r => r.data);

export const updateNotificationPreferences = (prefs: NotificationPreferences) =>
  request<{ data: NotificationPreferences }>('/notification-preferences', {
    method: 'PUT',
    body: JSON.stringify(prefs),
  }).then(r => r.data);

// ─── Org notification rules ───

export interface OrgNotificationRules {
  notify_assignment: boolean;
  notify_rejection: boolean;
  notify_completion: boolean;
  notify_deadline: boolean;
}

export const getOrgNotificationRules = () =>
  request<{ data: OrgNotificationRules }>('/org/notification-rules').then(r => r.data);

export const updateOrgNotificationRules = (rules: OrgNotificationRules) =>
  request<{ data: OrgNotificationRules }>('/org/notification-rules', {
    method: 'PUT',
    body: JSON.stringify(rules),
  }).then(r => r.data);

// ─── Org Settings ───

export interface OrgSettings {
  open_registration: boolean;
  domain: string | null;
}

export const getOrgSettings = () =>
  request<OrgSettings>('/org/settings');

export const updateOrgSettings = (settings: Partial<OrgSettings>) =>
  request<OrgSettings>('/org/settings', {
    method: 'PUT',
    body: JSON.stringify(settings),
  });

// ─── Concordance search ───

export interface ConcordanceResult {
  id: number;
  source_text: string;
  target_text: string;
  // Inline-tag-bearing form for display. Null when the TM entry has no
  // XLIFF tags. Frontend's <SegmentText> renders chips when present,
  // falls back to plain text otherwise.
  source_rich_text?: string | null;
  target_rich_text?: string | null;
  source_language: string;
  target_language: string;
  tm_name: string;
}

export interface TermConcordanceResult {
  id: number;
  source_term: string;
  target_term: string;
  status: string;
  notes: string;
  term_base_name: string;
}

export interface ConcordanceResponse {
  tm_matches: ConcordanceResult[];
  term_matches: TermConcordanceResult[];
}

export const concordanceSearch = (projectId: number, sourceLang: string, targetLang: string, query: string) =>
  request<ConcordanceResponse>(`/tms/concordance?project_id=${projectId}&source_language=${sourceLang}&target_language=${targetLang}&q=${encodeURIComponent(query)}`);
