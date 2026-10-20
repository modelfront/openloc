import { UserPen } from 'lucide-react'
import { Chip } from '@/components/ui/chip'
import { SectionInfo } from '@/components/ui/section-info'

// Another user has the segment open in the editor (a segment hold). The row
// is read-only for everyone else until they move on.

const heldText = (name: string) => `${name} is editing this segment`

/** Small icon in the row's # cell, with the holder's name on hover and focus. */
export function SegmentHeldIcon({ name }: { name: string }) {
  return (
    <SectionInfo tone="amber" size="sm" icon={UserPen} label={heldText(name)} data-testid="segment-held-icon">
      {heldText(name)}
    </SectionInfo>
  )
}

/** Notice above the active row's editor. */
export function SegmentHeldNotice({ name }: { name: string }) {
  return (
    <div className="mb-1.5">
      <Chip color="yellow" icon={<UserPen />} data-testid="segment-held-notice">
        {heldText(name)}
      </Chip>
    </div>
  )
}
