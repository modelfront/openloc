import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { cn } from '@/lib/utils'
import { Chip } from '@/components/ui/chip'
import { LANGUAGE_TOKENS, isLanguageToken } from '@/lib/tiptap-language-token'
import { previewAIPrompt, type AIPromptPreviewInput } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { MODE_FLAGS, type OutputMode } from './output-mode'

// What the platform appends to a prompt: the language direction and the output
// contract. It comes from the server so there is one assembly implementation
// (internal/ai/assemble.go) rather than a Go copy and a TypeScript copy that
// drift.
//
// It depends on the output mode and the chosen context — not on the
// instruction text — which is what lets the query key be those two and the
// panel need no debounce while the author types.
// `TestAssemblePrompt_AppendedIgnoresInstruction` pins that on the server side.

function draft(mode: OutputMode, segmentContext: string[], model?: string, provider?: string): AIPromptPreviewInput {
  return {
    prompt_text: '',
    ...MODE_FLAGS[mode],
    segment_context: segmentContext,
    ...(model ? { model } : {}),
    ...(provider ? { provider } : {}),
  }
}

export function usePromptPreview(mode: OutputMode, segmentContext: string[], model?: string, provider?: string) {
  const input = draft(mode, segmentContext, model, provider)
  return useQuery({
    queryKey: queryKeys.aiPrompts.preview(input),
    queryFn: () => previewAIPrompt(input),
    // Keep the preview mounted while context changes, avoiding a loading-state
    // collapse that clamps the page scroll position.
    placeholderData: keepPreviousData,
    // A mode + context pair resolves the same for the life of the build.
    staleTime: Infinity,
  })
}

const preClass = 'whitespace-pre-wrap font-mono text-xs leading-relaxed'

const TOKEN_SPLIT = new RegExp(`(${LANGUAGE_TOKENS.join('|')})`, 'g')

/**
 * Renders prompt text with the language tokens as chips, so the places that
 * become a real language at send time are visible at a glance instead of
 * hiding inside a wall of monospace.
 *
 * Split rather than replaced: the surrounding text has to survive exactly,
 * whitespace and all, because this panel's whole job is showing what goes on
 * the wire. And matched by equality rather than by re-testing the regex — a
 * `/g` regex carries `lastIndex` between calls, so `.test()` on one alternates
 * true and false down the list.
 *
 * The same token list the editor uses, so what renders as a chip while you
 * type renders as one here too.
 */
function withTokenChips(text: string) {
  return text.split(TOKEN_SPLIT).map((part, i) =>
    isLanguageToken(part)
      ? <Chip key={i} color="blue" size="small" className="mx-px font-mono">{part}</Chip>
      : part,
  )
}

/**
 * The system prompt as the model receives it.
 *
 * Everything the platform adds is shown, everywhere: the author cannot change
 * any of it, which is the reason to show it rather than hide it. Nothing
 * labels the two halves — the author's own words are tinted to stand out and
 * the platform's are muted, which says the same thing without a header on
 * every block.
 *
 * `instruction` is the author's live text, rendered client-side rather than
 * fetched: the server preview is keyed on the mode and the chosen context
 * alone, which is what lets it stay fresh with no debounce while someone
 * types. A caller that shows the instruction elsewhere (the engine row has its
 * own prompt panel) passes none and gets the appended half by itself.
 *
 * `model` is the one part that needs a concrete engine — only then can we say
 * whether that model is handed the shape as an enforced schema or merely asked
 * for it in words. A prompt is model-independent, so the prompt editor passes
 * none and that sentence is left off.
 */
export function PromptSends({
  mode, segmentContext, model, instruction,
}: {
  mode: OutputMode
  /** What the segments carry — the previewed body is built from exactly these. */
  segmentContext: string[]
  model?: string
  instruction?: string
}) {
  const { data, isLoading, isError } = usePromptPreview(mode, segmentContext, model)

  if (isLoading) {
    return <p className="text-xs italic text-muted-foreground">Loading…</p>
  }
  if (isError || !data) {
    return <p className="text-xs italic text-muted-foreground">This could not be loaded.</p>
  }

  const authored = instruction?.trim()
  return (
    <div className="space-y-3">
      {authored && (
        <pre data-testid="prompt-instruction" className={cn(preClass, 'text-foreground')}>
          {withTokenChips(authored)}
        </pre>
      )}
      <div>
        <pre data-testid="prompt-contract" className={cn(preClass, 'text-muted-foreground/90')}>
          {withTokenChips(data.appended.trimStart())}
        </pre>
        {model && (
          <p className="mt-1.5 text-[11px] text-muted-foreground" data-testid="prompt-contract-enforcement">
            {data.schema_enforced
              ? 'This model also receives the format as a schema it must follow, so replies always arrive in the expected shape.'
              : 'This model cannot be given a schema, so the format is requested in the prompt above.'}
          </p>
        )}
      </div>
    </div>
  )
}

