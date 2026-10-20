import { useMemo, useState } from 'react'
import { PageContainer } from '@/components/layout/PageContainer'
import { PageHeader } from '@/components/layout/PageHeader'
import { EmptyState } from '@/components/layout/EmptyState'
import { Button } from '@/components/ui/button'
import { ChoiceToggle } from '@/components/ui/choice-toggle'
import { useLLMDebugLog } from '@/hooks/useLLMDebugLog'
import { LLMCallRow } from '@/components/debug/LLMCallRow'
import { KIND_FILTERS, matchesKindFilter, type KindFilter } from '@/components/debug/format'

// /debug/llm — dev-only live viewer of every outbound AI provider call
// (OpenAI/Claude MT + QE+APE, extension calls): the assembled
// prompts, literal request/response bodies, latency, status and pipeline
// context. Hidden (not in nav), like /debug. Backed by the backend's
// in-memory ring — nothing persists, keys are redacted at capture time.

export default function LLMDebugPage() {
  const { entries, paused, setPaused, clear, unavailable } = useLLMDebugLog()
  const [filter, setFilter] = useState<KindFilter>('all')

  const visible = useMemo(
    () => entries.filter(e => matchesKindFilter(e.kind, filter)),
    [entries, filter],
  )

  return (
    <PageContainer width="default">
      <PageHeader
        title="LLM debug"
        actions={
          <>
            <Button variant="outline" size="sm" onClick={() => setPaused(!paused)} disabled={unavailable !== null}>
              {paused ? 'Resume' : 'Pause'}
            </Button>
            <Button variant="outline" size="sm" onClick={clear} disabled={entries.length === 0}>
              Clear
            </Button>
          </>
        }
      />

      <div className="flex items-center gap-2 mb-4">
        <ChoiceToggle size="sm" aria-label="Call kind" options={[...KIND_FILTERS]} value={filter} onChange={setFilter} />
        <span className="ml-auto text-xs text-gray-400">
          {visible.length} call{visible.length === 1 ? '' : 's'}
          {paused ? ' · paused' : ''}
        </span>
      </div>

      {unavailable === 'forbidden' ? (
        <EmptyState message="This feed spans every organization, so it's limited to platform owners." />
      ) : unavailable === 'not-mounted' ? (
        <EmptyState message="LLM debug capture is off — run the server with DEV_AUTO_LOGIN=true locally, or LLM_DEBUG_PERSIST=true to capture in a deployed environment." />
      ) : visible.length === 0 ? (
        <EmptyState message="Waiting for calls — run Apply MT with an AI engine or a QE+APE workflow step." />
      ) : (
        <div className="space-y-2">
          {visible.map(e => (
            <LLMCallRow key={e.seq} entry={e} />
          ))}
        </div>
      )}
    </PageContainer>
  )
}
