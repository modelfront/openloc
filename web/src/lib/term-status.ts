import { Ban, Clock, ShieldCheck, type LucideIcon } from 'lucide-react'

import type { ChipColor } from '@/components/ui/chip'

// Human labels for term (glossary) statuses. The raw values come from the
// backend term schema (approved | forbidden | pending); never render them
// directly.
//
// One table for both the rendered label and the status pickers' options, so a
// picker cannot offer wording a chip does not use.

export const TERM_STATUS_LABELS: Record<string, string> = {
  approved: 'Approved',
  forbidden: 'Forbidden',
  pending: 'Pending',
}

/** Polished label for a term status; falls back to the raw value if unknown. */
export function termStatusLabel(status: string | undefined | null): string {
  return (status && TERM_STATUS_LABELS[status]) || status || ''
}

/** Options for a status picker, in the order a user should see them. */
export const TERM_STATUS_OPTIONS = Object.entries(TERM_STATUS_LABELS).map(
  ([value, label]) => ({ value, label }),
)

// approved = use this term, forbidden = never use it, pending = nobody has
// decided, so it needs a person.
export const TERM_STATUS_COLOR: Record<string, ChipColor> = {
  approved: 'green',
  forbidden: 'red',
  pending: 'yellow',
}

// Icon for an icon-only status chip where space is tight (the editor sidebar);
// the label goes in its tooltip.
export const TERM_STATUS_ICON: Record<string, LucideIcon> = {
  approved: ShieldCheck,
  forbidden: Ban,
  pending: Clock,
}
