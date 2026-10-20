// /mt/engines — everything this org can translate with: provider engines
// (a key from AI settings plus that provider's options) and
// LLM translation engines (an AI engine with purpose 'mt').
import { useCanWriteAI } from '../../hooks/useCanWriteAI'
import { MTPageFrame } from './MTPageFrame'
import { MTProviderEnginesSection } from '../../components/mt/MTProviderEnginesSection'
import { AIEnginesSection } from '../../components/ai/AIEnginesSection'

export default function MTEnginesPage() {
  const canWriteAI = useCanWriteAI()

  return (
    <MTPageFrame>
      {/* MT engines are a manager carve-out: the route already keeps
          linguists out, and every role that gets this far may write one, which
          is what `RequireManager` on /api/mt-engines enforces. LLM engines are
          not in that carve-out — they stay owner/admin-write. */}
      <div data-guide="mt_engines.providers">
        <MTProviderEnginesSection canWrite />
      </div>

      <div className="mt-12" data-guide="mt_engines.llm">
        <AIEnginesSection canWrite={canWriteAI} purpose="mt" title="LLM" layout="static" />
      </div>
    </MTPageFrame>
  )
}
