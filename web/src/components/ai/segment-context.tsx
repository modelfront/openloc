import { CheckboxField } from '@/components/ui/checkbox-field'
import { useState } from 'react'
import { useGuideReveal } from '@/lib/guide-reveal'
import { Info } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { DisclosureToggle } from '@/components/ui/disclosure-toggle'
import { usePromptPreview } from './PromptSends'
import type { OutputMode } from './output-mode'

// What each segment carries to the model. The body is generated from these
// choices and previewed live beside them (PromptSends).
//
// Keys mirror segmentprompt.Optional in Go — the server drops anything it does
// not recognise, so the two lists disagreeing is a missing checkbox, never a
// broken request.

export interface SegmentContextField {
  key: string
  label: string
  description: string
}

/**
 * The optional context, in the order it appears in the generated body. Both
 * describe the SOURCE, which is why they suit every mode.
 */
export const SEGMENT_CONTEXT_FIELDS: SegmentContextField[] = [
  {
    key: 'tm_match',
    label: 'Translation memory match',
    description: 'The closest TM entry: source, target and match score after penalties. Lets the model spot a fuzzy or penalized match.',
  },
  {
    key: 'terms',
    label: 'Glossary terms',
    description: 'The approved and forbidden terms that appear in this segment’s source text.',
  },
]

export function SegmentContextField({
  mode, value, onChange, readOnly, revealAnchor,
}: {
  mode: OutputMode
  value: string[]
  onChange: (next: string[]) => void
  readOnly?: boolean
  /** A tour step pointing here opens the section. */
  revealAnchor?: string
}) {
  const [open, setOpen] = useState(false)
  useGuideReveal(revealAnchor, () => setOpen(true))
  const toggle = (key: string, checked: boolean) => {
    // Rebuilt from the canonical list rather than pushed/spliced, so the stored
    // order always matches the order the body generates in — and a retired key
    // drops out on the next edit instead of lingering unseen.
    onChange(SEGMENT_CONTEXT_FIELDS
      .map(f => f.key)
      .filter(k => (k === key ? checked : value.includes(k))))
  }

  const row = (f: SegmentContextField, checked: boolean, disabled: boolean) => (
    <div key={f.key} className="flex items-center gap-2">
      <CheckboxField
        checked={checked}
        disabled={disabled}
        onChange={c => toggle(f.key, c)}
        label={f.label}
        data-testid={`segment-context-${f.key}`}
      />
      <TooltipProvider delay={200}>
        <Tooltip>
          <TooltipTrigger
            render={<span tabIndex={0} className="inline-flex cursor-help text-muted-foreground" />}
            aria-label={`About ${f.label}`}
          >
            <Info className="size-3" />
          </TooltipTrigger>
          <TooltipContent className="max-w-xs">{f.description}</TooltipContent>
        </Tooltip>
      </TooltipProvider>
    </div>
  )

  return (
    <div className="space-y-2">
      <DisclosureToggle
        open={open}
        onToggle={() => setOpen(v => !v)}
        controls="ai-prompt-context"
        variant="field"
        className="text-sm"
        data-testid="ai-prompt-context-toggle"
      >
        Context
      </DisclosureToggle>
      {open && (
        <div id="ai-prompt-context" className="pl-5" data-testid="segment-context">
          <div className="flex flex-wrap items-center gap-x-6 gap-y-1">
          <CheckboxField label="Source text" checked disabled onChange={() => {}} data-testid="segment-context-source_text" />
          {mode !== 'mt' && (
            <CheckboxField label="Target text" checked disabled onChange={() => {}} data-testid="segment-context-target_text" />
          )}
          {SEGMENT_CONTEXT_FIELDS.map(f => row(f, value.includes(f.key), !!readOnly))}
          </div>
          <ContextPreview mode={mode} value={value} />
        </div>
      )}
    </div>
  )
}

function ContextPreview({ mode, value }: { mode: OutputMode; value: string[] }) {
  const { data, isLoading, isError } = usePromptPreview(mode, value)
  if (isLoading) return <p className="text-xs text-muted-foreground">Loading…</p>
  if (isError || !data) return <p className="text-xs text-muted-foreground">Context preview could not be loaded.</p>
  return <pre data-testid="context-json-preview" className="mt-3 overflow-x-auto font-mono text-xs leading-relaxed text-muted-foreground/90">
    {JSON.stringify(data.context_preview, null, 2)}
  </pre>
}
