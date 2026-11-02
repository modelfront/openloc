import { request } from './request'
import type { JoinRow, Result as SegmentEditResult, SplitRequest } from '../types/segmentedit'

export type { JoinRow, SegmentEditResult, SplitRequest }

/** Split one row of the job at an offset in its source text. Only this job
 *  changes; the file's other languages keep their segments. */
export const splitSegment = (jobId: number, req: SplitRequest) =>
  request<{ data: SegmentEditResult }>(`/jobs/${jobId}/segments/split`, {
    method: 'POST',
    body: JSON.stringify(req),
  }).then(r => r.data)

/** Join rows that follow each other in the job, in document order. */
export const joinSegments = (jobId: number, rows: JoinRow[]) =>
  request<{ data: SegmentEditResult }>(`/jobs/${jobId}/segments/join`, {
    method: 'POST',
    body: JSON.stringify({ rows }),
  }).then(r => r.data)
