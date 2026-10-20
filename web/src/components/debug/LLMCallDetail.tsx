import { Copy } from 'lucide-react'
import { CopyLinkButton } from '@/components/ui/copy-link-button'
import type { LLMDebugEntry } from '@/api'
import { KindExtension } from '@/types/llmdebug'
import { tryPretty } from './format'

// Expanded view of one captured provider call. For LLM entries the assembled
// prompts (system + user) come first — seeing exactly how the final prompt was
// built is the point of the tool; the literal wire bodies follow for the
// full picture. Extension entries have no prompts, just URL + bodies, and
// their URL already carries the HTTP method.

function CopyBlock({ label, value, pretty }: { label: string; value: string; pretty?: boolean }) {
  if (!value) return null
  return (
    <div>
      <div className="flex items-center justify-between mb-1">
        <span className="text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wide">{label}</span>
        <CopyLinkButton
          url={value}
          label="Copy"
          size="xs"
          icon={Copy}
          toastMessage="Copied to clipboard"
          errorMessage="Could not copy — select the text and copy it manually"
        />
      </div>
      <pre className="text-xs bg-gray-50 dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-md p-3 whitespace-pre-wrap break-words max-h-80 overflow-y-auto">
        {pretty ? tryPretty(value) : value}
      </pre>
    </div>
  )
}

export function LLMCallDetail({ entry }: { entry: LLMDebugEntry }) {
  const isLLM = entry.kind !== KindExtension
  return (
    <div className="space-y-3 px-3 pb-3">
      <div className="text-xs text-gray-500 dark:text-gray-400 break-all">
        {isLLM && <span className="font-medium">POST </span>}{entry.url}
      </div>
      {isLLM && (
        <>
          <CopyBlock label="System prompt" value={entry.system ?? ''} />
          <CopyBlock label="User message" value={entry.user ?? ''} />
        </>
      )}
      <CopyBlock label="Request body (wire)" value={entry.request_body} pretty />
      <CopyBlock label="Response body" value={entry.response_body} pretty />
      {entry.error && (
        <div className="text-xs text-rose-600 dark:text-rose-400 whitespace-pre-wrap break-words">{entry.error}</div>
      )}
    </div>
  )
}
