import { useState } from 'react'
import { useGuideReveal } from '@/lib/guide-reveal'
import { DisclosureToggle } from '@/components/ui/disclosure-toggle'
import { usePromptPreview } from './PromptSends'
import type { OutputMode } from './output-mode'

// Only the output format is shown. The INPUT needs no description: a segment
// arrives as JSON with named keys, and a model reads "source" or "tm_matches"
// without being told what they are. The Context section already shows the exact
// body those keys are built into.
export function PromptFormats({ mode, segmentContext, revealAnchor }: {
  mode: OutputMode
  segmentContext: string[]
  /** A tour step pointing here opens the section. */
  revealAnchor?: string
}) {
  const [open, setOpen] = useState(false)
  useGuideReveal(revealAnchor, () => setOpen(true))
  return (
    <div className="space-y-2">
      <DisclosureToggle
        open={open}
        onToggle={() => setOpen(v => !v)}
        controls="ai-prompt-output_format"
        variant="field"
        className="text-sm"
        data-testid="ai-prompt-output_format-toggle"
      >
        Output format
      </DisclosureToggle>
      {open && (
        <div id="ai-prompt-output_format" className="pl-5">
          <OutputFormat mode={mode} segmentContext={segmentContext} />
        </div>
      )}
    </div>
  )
}

// The schema follows from the mode, not the provider: OpenAI and Claude receive
// the same one and differ only in the request field it rides in. So there is no
// provider to choose here — one schema, shown once.
function OutputFormat({ mode, segmentContext }: { mode: OutputMode; segmentContext: string[] }) {
  const { data, isLoading, isError } = usePromptPreview(mode, segmentContext)
  if (isLoading) return <p className="text-xs text-muted-foreground">Loading…</p>
  if (isError || !data) return <p className="text-xs text-muted-foreground">This could not be loaded.</p>
  return <div className="space-y-3">
    <pre data-testid="prompt-output_format" className="whitespace-pre-wrap font-mono text-xs leading-relaxed text-muted-foreground/90">
      {data.output_format.trim()}
    </pre>
    {/* No toggle of its own: this already sits inside Output format, and the
        schema IS the output format for every model that takes one. Captioned
        rather than left bare — unlabelled JSON under prose reads as an example
        of the reply, not as the rule the provider enforces. */}
    {data.output_schema && (
      <div className="space-y-1">
        <p className="font-sans text-xs text-gray-500 dark:text-gray-400">
          JSON schema — the provider holds every reply to this shape.
        </p>
        <pre data-testid="prompt-output-schema" className="overflow-x-auto whitespace-pre-wrap font-mono text-xs leading-relaxed text-muted-foreground/90">
          {JSON.stringify(data.output_schema, null, 2)}
        </pre>
      </div>
    )}
  </div>
}
