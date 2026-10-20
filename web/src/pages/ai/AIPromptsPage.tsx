// /ai/prompts — the QE+APE system prompts engines reference.
// MT prompts live under /mt/prompts.
import { AIPageFrame } from './AIPageFrame'
import { useCanWriteAI } from '../../hooks/useCanWriteAI'
import { AIPromptsSection } from '../../components/ai/AIPromptsSection'

export default function AIPromptsPage() {
  const canWrite = useCanWriteAI()
  return (
    <AIPageFrame>
      <AIPromptsSection canWrite={canWrite} purpose="qe_ape" guide="ai_prompts.list" />
    </AIPageFrame>
  )
}
