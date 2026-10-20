import { useQuery } from '@tanstack/react-query'
import { getMe } from '@/api'
import { SessionBanner, SessionBannerAction } from '@/components/layout/SessionBanner'
import { demoLeaveLabel, leavePlayground } from '@/lib/demo'
import { queryKeys } from '@/lib/query-keys'
import { PlaygroundTourEndDialog } from './PlaygroundTourEndDialog'

/** Shown on every page of the demo organization. */
export function DemoBanner() {
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  return (
    <>
      <SessionBanner action={<SessionBannerAction onClick={leavePlayground}>{demoLeaveLabel(me)}</SessionBannerAction>}>
        You&rsquo;re in the <strong>Playground</strong>: sample data, look around freely. Changes won&rsquo;t be saved.
      </SessionBanner>
      <PlaygroundTourEndDialog me={me} />
    </>
  )
}
