import type { PreviousStepStatus } from '../api'
import type { ChipColor } from '@/components/ui/chip'
import { STATUS_CHIP_COLORS } from './SegmentRow.shared'

// Status a finished workflow step left a segment in. A step that ended with
// the segment at needs_review never confirmed it.
const PREVIOUS_STATUS_LABELS: Record<string, string> = {
  needs_review:  'Not confirmed',
  auto_approved: 'Auto-approved',
  approved:      'Approved',
}

/** Label for the status a finished step left a segment in. */
export function stepEndStatusLabel(status: string): string {
  return PREVIOUS_STATUS_LABELS[status] ?? PREVIOUS_STATUS_LABELS.needs_review
}

/** Same colours as the Status column; a step that ended unconfirmed is grey,
 *  since it is history rather than work waiting in this step. */
export function stepEndStatusColor(status: string): ChipColor {
  return status === 'approved' || status === 'auto_approved' ? STATUS_CHIP_COLORS[status] : 'grey'
}

/** Translations the previous step left auto-approved — the only previous
 *  result a segment row shows. */
export function autoApprovedBeforeStep(previous: PreviousStepStatus[]): Set<number> {
  return new Set(previous.filter(p => p.status === 'auto_approved').map(p => p.translation_id))
}

/** Tooltip on the grey Auto-approved chip of a segment the previous step auto-approved. */
export const PREVIOUS_AUTO_APPROVED_TOOLTIP = 'Auto-approved in the previous step.'
