// /ai/engines — the QE+APE AI engines a router sends jobs to. Providers +
// vendor API keys live under AI settings (/ai-settings). LLM-as-MT engines
// live on the MT engines page, next to the other things that translate.
//
// /ai/engines/new is the same page with the create dialog open, so anything
// that needs an engine (a router's picker) can link straight to making one.
import { useLocation, useNavigate } from 'react-router-dom'
import { AIPageFrame } from './AIPageFrame'
import { useCanWriteAI } from '../../hooks/useCanWriteAI'
import { AIEnginesSection } from '../../components/ai/AIEnginesSection'

export default function AIEnginesPage() {
  const canWrite = useCanWriteAI()
  const navigate = useNavigate()
  const creating = useLocation().pathname.endsWith('/new')
  return (
    <AIPageFrame>
      <div data-guide="ai_engines.list">
        <AIEnginesSection
          canWrite={canWrite}
          purpose="qe_ape"
          layout="static"
          createOnMount={creating}
          onCreateClosed={creating ? () => navigate('/ai/engines', { replace: true }) : undefined}
        />
      </div>
    </AIPageFrame>
  )
}
