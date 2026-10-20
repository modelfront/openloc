import { useEffect, useState } from 'react'
import ConfirmDialog from '@/components/ConfirmDialog'
import type { UserProfile } from '@/api'
import { PLAYGROUND_TOUR_END_EVENT, demoLeaveLabel, leavePlayground } from '@/lib/demo'

/**
 * Opens when a saved tour in the Playground ends, after its card has closed,
 * and offers the way back to the visitor's organization.
 */
export function PlaygroundTourEndDialog({ me }: { me: UserProfile | undefined }) {
  const [open, setOpen] = useState(false)
  // Kept after closing, so the title does not change while the dialog fades out.
  const [finished, setFinished] = useState(false)
  useEffect(() => {
    const onEnd = (e: Event) => {
      setFinished((e as CustomEvent<boolean>).detail)
      setOpen(true)
    }
    window.addEventListener(PLAYGROUND_TOUR_END_EVENT, onEnd)
    return () => window.removeEventListener(PLAYGROUND_TOUR_END_EVENT, onEnd)
  }, [])
  const back = me?.demo_return_org_name
  const where = back ? `go back to ${back}` : 'leave the Playground'
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={setOpen}
      title={finished ? 'Tour complete' : 'Tour closed'}
      description={`You can ${where}, or stay and look around the Playground. Nothing you do here is saved.`}
      cancelLabel="Stay in the Playground"
      actionLabel={demoLeaveLabel(me)}
      variant="default"
      onConfirm={leavePlayground}
    />
  )
}
