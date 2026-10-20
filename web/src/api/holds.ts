import { request } from './request';
import type { Translation } from './projects';

// Segment holds: who has a segment open in the CAT editor. While another user
// holds a segment, it is read-only here and writes to it are refused.

/** A live hold in a job. */
export interface SegmentHold {
  translation_id: number;
  user_id: number;
  holder_name: string;
  acquired_at: string;
  expires_at: string;
}

/** A hold the caller now has, with the segment as it stands. */
export interface AcquiredHold {
  translation_id: number;
  expires_at: string;
  translation: Translation;
}

/** Error code of a refusal because another user holds the segment. */
export const SEGMENT_HELD_CODE = 'segment_held';

export const listHolds = (jobId: number) =>
  request<{ data: SegmentHold[] }>(`/jobs/${jobId}/holds`).then(r => r.data);

export const acquireHold = (jobId: number, translationId: number, tabId: string) =>
  request<{ data: AcquiredHold }>(`/jobs/${jobId}/holds/${translationId}`, {
    method: 'PUT',
    body: JSON.stringify({ tab_id: tabId }),
  }).then(r => r.data);

/** keepalive lets the release finish while the page unloads. */
export const releaseHold = (jobId: number, translationId: number, tabId: string, opts?: { keepalive?: boolean }) =>
  request<void>(`/jobs/${jobId}/holds/${translationId}?tab_id=${encodeURIComponent(tabId)}`, {
    method: 'DELETE',
    keepalive: opts?.keepalive,
  });
