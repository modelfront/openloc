// /mt/prompts — the MT-purpose system prompts the LLM translation engines
// reference. QE+APE prompts live on the AI page.
import { MTPageFrame } from './MTPageFrame'
import { useCanWriteAI } from '../../hooks/useCanWriteAI'
import { AIPromptsSection } from '../../components/ai/AIPromptsSection'

export default function MTPromptsPage() {
  const canWrite = useCanWriteAI()
  return (
    <MTPageFrame>
      <AIPromptsSection canWrite={canWrite} purpose="mt" guide="mt_prompts.list" />
    </MTPageFrame>
  )
}
