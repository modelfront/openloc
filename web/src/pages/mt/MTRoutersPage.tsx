// /mt — MT routers: the target-language → engine maps that projects and
// templates point at for machine translation. Manager surface: every
// manager may create and edit them (the same posture as MT engines).
import { MTPageFrame } from './MTPageFrame'
import { MTRoutersSection } from '../../components/mt/MTRoutersSection'

export default function MTRoutersPage() {
  return (
    <MTPageFrame>
      <div data-guide="mt_routers.list">
        <MTRoutersSection canWrite />
      </div>
    </MTPageFrame>
  )
}
