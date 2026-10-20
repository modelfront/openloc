import { useState } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { Chip } from '@/components/ui/chip'
import type { LLMDebugEntry } from '@/api'
import { formatDuration, kindLabel, kindColor } from './format'
import { LLMCallDetail } from './LLMCallDetail'

// One captured call in the /debug/llm feed: a collapsed summary row that
// expands to the full request/response detail.

export function LLMCallRow({ entry }: { entry: LLMDebugEntry }) {
  const [open, setOpen] = useState(false)
  const failed = entry.status === 0 || entry.status >= 400
  const time = new Date(entry.time).toLocaleTimeString(undefined, { hour12: false })

  return (
    <div className="border border-gray-200 dark:border-gray-800 rounded-md bg-white dark:bg-gray-950">
      {/* eslint-disable-next-line no-restricted-syntax -- whole-row expander (debug feed), not an action button */}
      <button
        type="button"
        onClick={() => setOpen(o => !o)}
        className="w-full flex items-center gap-2 px-3 py-2 text-left text-sm hover:bg-gray-50 dark:hover:bg-gray-900 rounded-md"
      >
        {open ? (
          <ChevronDown className="h-4 w-4 shrink-0 text-gray-400" />
        ) : (
          <ChevronRight className="h-4 w-4 shrink-0 text-gray-400" />
        )}
        <span className="font-mono text-xs text-gray-500 dark:text-gray-400 shrink-0">{time}</span>
        <Chip color={kindColor(entry.kind)}>{kindLabel(entry.kind)}</Chip>
        <span className="text-xs text-gray-600 dark:text-gray-300 shrink-0">
          {entry.provider}
          {entry.model ? ` · ${entry.model}` : ''}
        </span>
        <span className={`font-mono text-xs shrink-0 ${failed ? 'text-rose-600 dark:text-rose-400' : 'text-emerald-600 dark:text-emerald-400'}`}>
          {entry.status === 0 ? 'ERR' : entry.status}
        </span>
        <span className="font-mono text-xs text-gray-400 shrink-0">{formatDuration(entry.duration_ms)}</span>
        {/* How big the batch was, and why a small one is small. A feed of 10s
            with 1s scattered through it reads as broken batching until you can
            see which 1s are retries. */}
        {entry.segments ? (
          <span className="font-mono text-xs text-gray-500 dark:text-gray-400 shrink-0">
            {entry.segments} seg
          </span>
        ) : null}
        {entry.retry ? <Chip color="yellow" size="small">retry</Chip> : null}
        <span className="flex items-center gap-1 min-w-0 text-xs text-gray-400 truncate">
          {entry.project ? <span className="shrink-0">{entry.project}</span> : null}
          {entry.job_id ? <span className="shrink-0">job #{entry.job_id}</span> : null}
          {entry.file_id ? <span className="shrink-0">file #{entry.file_id}</span> : null}
          {entry.step ? <span className="shrink-0">{entry.step}</span> : null}
          {entry.engine ? <span className="shrink-0">{entry.engine}</span> : null}
          {entry.error ? <span className="text-rose-500 truncate">{entry.error.split('\n')[0]}</span> : null}
        </span>
      </button>
      {open && <LLMCallDetail entry={entry} />}
    </div>
  )
}
