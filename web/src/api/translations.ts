import { request } from './request';

// One recorded change to a segment, from `translation_history`.
// Hand-written rather than tygo-generated: the backend serves a joined row
// struct (engine / TM / user / step names resolved), not the bare table model.
//
// Provenance fields are sparse by kind — `tm_name` + `tm_match_percent` on TM
// events, `mt_engine_name` on MT and APE events, the `created_by` trio on human
// and propagation events. All joins are LEFT, so a deleted engine or user
// leaves the event readable with a null name.
export interface SegmentHistoryEvent {
  id: number;
  /**
   * TranslationHistoryKind: tm | mt | ape | human | propagation | file_import |
   * cleared | approved | auto_approved | unconfirmed.
   */
  kind: string;
  /**
   * Target text after this event. Null on the kinds that change no text —
   * `cleared` and the three status events — which is what lets the panel
   * render those rows compact.
   */
  text: string | null;
  rich_text: string | null;
  created_at: string;
  tm_match_percent: number | null;
  edit_distance: number | null;
  /** Per-kind payload: predict_outcome, source, source_translation_id. */
  details: Record<string, unknown>;
  created_by: number | null;
  created_by_name: string | null;
  created_by_email: string | null;
  mt_engine_name: string | null;
  tm_name: string | null;
  step_name: string | null;
  /** The workflow step assignment the event happened in. Set on a person's
   *  saves and on AI-step results; null on pre-translation and imports. */
  job_step_assignment_id: number | null;
}

/** Newest first, capped at 100 events. */
export const listSegmentHistory = (translationId: number) =>
  request<{ data: SegmentHistoryEvent[] }>(`/translations/${translationId}/history`).then(r => r.data);

/** How one finished workflow step round left a segment. */
export interface StepStatusEntry {
  step_name: string;
  step_order: number;
  step_type: string;
  /** Which pass through the step, from 1. A step rejected back to runs again. */
  round: number;
  /** `rejected`: the step was sent back to the one before it. */
  end_reason: 'completed' | 'rejected';
  /** Raw status when the step ended: needs_review | auto_approved | approved. */
  status: string;
  /** QE/APE verdict produced in this round: approved | ape_approved | ape_needs_review | rejected. */
  ai_outcome: string | null;
  /** A person changed the text during the round. */
  edited: boolean;
  ended_at: string;
}

export interface PreviousStepStatus extends StepStatusEntry {
  translation_id: number;
}

/** The editor's view of the job's earlier steps. */
export interface JobStepStatuses {
  /** Per segment, the most recently finished step round. */
  previous: PreviousStepStatus[];
  /** Translations whose AI verdict came from an earlier step. */
  predicted_before_step: number[];
}

export const getJobStepStatuses = (jobId: number) =>
  request<{ data: JobStepStatuses }>(`/jobs/${jobId}/step-statuses`).then(r => r.data);

/** Every finished step round of one segment, oldest first. */
export const listSegmentStepStatuses = (translationId: number) =>
  request<{ data: StepStatusEntry[] }>(`/translations/${translationId}/step-statuses`).then(r => r.data);
