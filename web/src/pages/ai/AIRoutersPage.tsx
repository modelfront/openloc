// /ai — AI routers: the target-language → QE+APE engine maps that workflow
// steps point at.
//
// /ai/new is the same page with the create dialog open, so a workflow step's
// router picker can link straight to making one.
import { useLocation, useNavigate } from 'react-router-dom'
import { AIPageFrame } from './AIPageFrame'
import { useCanWriteAI } from '../../hooks/useCanWriteAI'
import { AIRoutersSection } from '../../components/ai/AIRoutersSection'

export default function AIRoutersPage() {
  const canWrite = useCanWriteAI()
  const navigate = useNavigate()
  const creating = useLocation().pathname.endsWith('/new')
  return (
    <AIPageFrame>
      <div data-guide="ai_routers.list">
        <AIRoutersSection
          canWrite={canWrite}
          createOnMount={creating}
          onCreateClosed={creating ? () => navigate('/ai', { replace: true }) : undefined}
        />
      </div>
    </AIPageFrame>
  )
}
