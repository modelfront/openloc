import { useMemo, useRef, useState } from 'react'
import { useGuideReveal } from '@/lib/guide-reveal'
import { useForm, useWatch, Controller } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Copy } from 'lucide-react'
import type { AIPromptWithAdditions, AIPromptInput, EnginePurpose } from '../../api'
import { Button } from '@/components/ui/button'
import { FormField } from '@/components/ui/form-field'
import { Input } from '@/components/ui/input'
import { Chip, chipVariants } from '@/components/ui/chip'
import { cn } from '@/lib/utils'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { MODE_FLAGS, MODE_LABELS, MODE_DESCRIPTIONS, flagsToMode, defaultPromptName, type OutputMode } from './output-mode'
import { DisclosureToggle } from '@/components/ui/disclosure-toggle'
import { PromptFormats } from './PromptFormats'
import { TokenTextField, type TokenTextFieldHandle } from './TokenTextField'
import { LANGUAGE_TOKENS } from '@/lib/tiptap-language-token'
import { ModeCards } from './mode-cards'
import { SegmentContextField } from './segment-context'
import { LanguageAdditionsField, isCompleteAddition, isUntouchedAddition, type LanguageAdditionRow } from './LanguageAdditionsField'
import { LanguageCode } from '@/components/ui/language-code'
import { useScrollToError } from '../../hooks/useScrollToError'

// The editor for one system prompt: what it does, the instructions, the
// per-language rules, the context each segment carries, and the output format.
//
// A page rather than a dialog. The form is a dozen fields deep and its preview
// is the length of the prompt itself — in a modal that is a scroll box on top
// of a scroll box, and the list underneath is not worth keeping in view.

// Starter templates, inserted once when the type is picked at creation. Drafts.
//
// Each carries the meanings of the fields its type returns — meanings are
// instruction text and the tenant's to reword. The SHAPE stays generated — the
// schema and the segment-context checkboxes own it — so an edit can cost output
// quality but never a broken reply.
const MODE_TEMPLATES: Record<OutputMode, string> = {
  mt: `You are a professional <source_lang>→<target_lang> translator. Translate the source segment into <target_lang> faithfully and idiomatically, preserving meaning, tone, formatting tags, and placeholders.

For each segment return "target" with the translation.`,
  ape: `You are a <source_lang>→<target_lang> post-editor. Correct the <target_lang> translation of each source segment.

For each segment return "postedit" with the corrected translation, or null when you change nothing. "comment" is a short remark for the human reviewer, or null.`,
  qe: `You are a <source_lang>→<target_lang> quality evaluator. Judge the <target_lang> translation of each source segment.

For each segment return "approved" true when the translation is acceptable, "score" for the translation, and "comment" with a short remark for the human reviewer, or null.`,
  qe_ape: `You are a <source_lang>→<target_lang> reviewer. Judge the <target_lang> translation of each source segment and correct it when needed.

For each segment return "approved" and "score" for the original translation, "postedit" with your corrected translation or null, "postedit_approved" and "postedit_score" for your postedit or null, and "comment" with a short remark for the human reviewer, or null.`,
}

// The collapsed disclosure's one line: how many additions, then the languages
// of the first few, so the toggle says what is inside without opening it.
const SUMMARY_ROWS = 2

function languagesSummary(rows: LanguageAdditionRow[]) {
  const hidden = rows.length - SUMMARY_ROWS
  return (
    <span className="inline-flex flex-wrap items-baseline gap-1">
      {`${rows.length} · `}
      {rows.slice(0, SUMMARY_ROWS).map((a, i) => (
        <span key={i} className="inline-flex items-baseline">
          <LanguageCode code={a.target_languages.join(', ') || 'any'} />
          {i < Math.min(rows.length, SUMMARY_ROWS) - 1 && ','}
        </span>
      ))}
      {hidden > 0 && <span className="font-mono text-xs">+{hidden}</span>}
    </span>
  )
}

const promptSchema = z.object({
  name: z.string().min(1, 'Name is required'),
  prompt_text: z.string().min(1, 'Prompt text is required'),
  mode: z.enum(['qe', 'qe_ape', 'ape', 'mt']),
  segment_context: z.array(z.string()),
  // An addition needs a target language plus text. A row left completely alone
  // is someone who changed their mind and is dropped on submit; anything in
  // between is rejected, because a rule with no language would apply to every
  // job and one with no text would apply nothing.
  language_additions: z.array(z.object({
    target_languages: z.array(z.string()),
    text: z.string(),
  })).refine(
    rows => rows.every(r => isCompleteAddition(r) || isUntouchedAddition(r)),
    { message: 'Each addition needs a target language, and some text' },
  ),
})

type PromptFormValues = z.infer<typeof promptSchema>

// Assistant anchors (internal/chat/pages_registry.go): the MT and AI prompt
// pages share this form, and each page has its own anchor ids.
type PromptGuideAnchors = { form: string; instructions: string; languages: string; context: string; schema: string }
const PROMPT_GUIDE_ANCHORS: Record<EnginePurpose, PromptGuideAnchors> = {
  mt: { form: 'mt_prompt.form', instructions: 'mt_prompt.instructions', languages: 'mt_prompt.languages', context: 'mt_prompt.context', schema: 'mt_prompt.schema' },
  qe_ape: { form: 'ai_prompt.form', instructions: 'ai_prompt.instructions', languages: 'ai_prompt.languages', context: 'ai_prompt.context', schema: 'ai_prompt.schema' },
}

export function PromptForm({
  initial, onSubmit, onCancel, onDuplicate, isPending, readOnly, asCopy, purpose,
  takenNames = [],
}: {
  initial?: AIPromptWithAdditions
  onSubmit: (input: AIPromptInput) => void
  onCancel: () => void
  // Shown in the read-only (default template) view: jump to a pre-filled copy.
  onDuplicate?: () => void
  isPending: boolean
  // Built-in templates render as a view-only form (no save).
  readOnly?: boolean
  // Create-mode form pre-filled from `initial` (Duplicate) — submits
  // a NEW prompt, so the name gets a "(copy)" suffix to dodge the unique key.
  asCopy?: boolean
  // Names already in use, so picking a type can hand back a free one. Names are
  // unique per tenant; a collision is a 400 the user has to undo by hand.
  takenNames?: string[]
  // The owning section's purpose. Restricts the output-mode choice to the ones
  // that keep the prompt on this page: an MT-page prompt saved as QE would
  // vanish from the list that created it.
  purpose: EnginePurpose
}) {
  const isEdit = !!initial && !asCopy
  const [typeChosen, setTypeChosen] = useState(!!initial || purpose === 'mt')
  const modeOptions = useMemo<OutputMode[]>(
    () => (purpose === 'mt' ? ['mt'] : ['qe', 'qe_ape', 'ape']), [purpose])
  const defaultMode: OutputMode = purpose === 'mt' ? 'mt' : 'qe_ape'
  const guide = PROMPT_GUIDE_ANCHORS[purpose]
  const { register, control, getValues, setValue, handleSubmit, formState: { errors, isDirty } } = useForm<PromptFormValues>({
    resolver: zodResolver(promptSchema),
    defaultValues: {
      // MT has one type and skips the card step, so its name has to be right
      // from the start; on the other pages the card click replaces this.
      name: initial ? (asCopy ? `${initial.name} (copy)` : initial.name) : defaultPromptName(defaultMode, takenNames),
      prompt_text: initial ? initial.prompt_text : MODE_TEMPLATES[defaultMode],
      mode: initial ? flagsToMode(initial) : defaultMode,
      // A new prompt starts with the TM match on: it is the context that most
      // often decides a verdict, and the built-in templates ship with it.
      segment_context: initial?.segment_context ?? ['tm_match'],
      // A duplicate carries the source prompt's additions — they are part of
      // what makes it worth copying.
      language_additions: initial?.language_additions ?? [],
    },
  })
  useScrollToError(errors)

  const [mode, segmentContext, languageAdditions] = useWatch({ control, name: ['mode', 'segment_context', 'language_additions'] })
  // The array-level refine message. Kept in a variable because it drives two
  // things: the field's own error slot, and forcing the disclosure open — a
  // validation message inside a collapsed panel is a form that refuses to
  // submit for no visible reason.
  const languageError = errors.language_additions?.root?.message ?? errors.language_additions?.message
  const [showLanguages, setShowLanguages] = useState(false)
  const [showBase, setShowBase] = useState(true)
  // A tour step pointing at a collapsed section opens it.
  useGuideReveal(guide.instructions, () => setShowBase(true))
  useGuideReveal(guide.languages, () => setShowLanguages(true))

  // Insertion is the editor's job — it knows where the caret is and puts a
  // whole chip there, so there is no offset arithmetic here.
  const tokenFieldRef = useRef<TokenTextFieldHandle>(null)

  // Insert a mode's starter template at the TOP of the prompt, swapping out a
  // previous default template if the box still starts with one.
  const fillTemplate = (m: OutputMode) => {
    const tpl = MODE_TEMPLATES[m]
    let rest = getValues('prompt_text')
    for (const t of Object.values(MODE_TEMPLATES)) {
      if (rest.startsWith(t)) {
        rest = rest.slice(t.length).replace(/^\s+/, '')
        break
      }
    }
    const next = rest.trim() ? `${tpl}\n\n${rest}` : tpl
    setValue('prompt_text', next, { shouldValidate: true, shouldDirty: true })
  }

  const onFormSubmit = (data: PromptFormValues) => {
    if (!typeChosen) return
    onSubmit({
      name: data.name.trim(),
      prompt_text: data.prompt_text.trim(),
      ...MODE_FLAGS[data.mode],
      segment_context: data.segment_context,
      // Drop the rows the author opened and left alone; the schema already
      // rejected the partly-filled ones.
      language_additions: data.language_additions
        .filter(isCompleteAddition)
        .map(a => ({ ...a, text: a.text.trim() })),
    })
  }

  // Saved prompts keep their type as a label.
  const modeChip = (
    <Chip title={MODE_DESCRIPTIONS[mode]} className="shrink-0">{MODE_LABELS[mode]}</Chip>
  )

  return (
    <form className="space-y-4" onSubmit={handleSubmit(onFormSubmit)} data-testid="ai-prompt-form" data-guide={guide.form}>
      {!typeChosen ? (
        <>
          <FormField label="What this prompt does">
            <ModeCards
              options={modeOptions}
              // Nothing is chosen yet, so no card reads as selected. Picking one
              // is what reveals the rest of the form.
              value={undefined}
              readOnly={readOnly}
              onChange={next => {
                setValue('mode', next, { shouldDirty: true })
                // The name field is still hidden, so nothing the user typed can
                // be lost here.
                setValue('name', defaultPromptName(next, takenNames), { shouldDirty: true })
                fillTemplate(next)
                setTypeChosen(true)
              }}
            />
          </FormField>
        </>
      ) : (
        <div className="flex items-center gap-2">
          {modeChip}
          {readOnly && <span className="text-xs text-muted-foreground">Built-in — duplicate it to make changes.</span>}
        </div>
      )}

      {typeChosen && <div className="space-y-4">
        {!isEdit && <FormField className="w-full sm:w-1/2" label="Name" error={errors.name?.message} required>
          <Input {...register('name')} placeholder="e.g. QE reviewer" disabled={readOnly} data-testid="ai-prompt-name" />
        </FormField>}

        <div className="space-y-2" data-guide={guide.instructions}>
          <DisclosureToggle
            open={showBase || !!errors.prompt_text}
            onToggle={() => setShowBase(v => !v)}
            controls="ai-prompt-base"
            variant="field"
            className="text-sm"
            data-testid="ai-prompt-base-toggle"
          >
            Base prompt
          </DisclosureToggle>
          {(showBase || !!errors.prompt_text) && <div id="ai-prompt-base" className="pl-5">
            <Controller
              control={control}
              name="prompt_text"
              render={({ field }) => (
                <TokenTextField
                  value={field.value}
                  onChange={field.onChange}
                  placeholder="What should the model do with each segment?"
                  disabled={readOnly}
                  handleRef={tokenFieldRef}
                  className="[&_.ProseMirror]:text-xs"
                  data-testid="ai-prompt-instructions"
                />
              )}
            />
            {!readOnly && (
              <TooltipProvider delay={200}>
                <div className="mt-1 flex flex-wrap items-center gap-1">
                <span className="text-[10px] text-muted-foreground">Insert</span>
                {LANGUAGE_TOKENS.map(p => (
                  <Tooltip key={p}>
                    <TooltipTrigger
                      type="button"
                      onClick={() => tokenFieldRef.current?.insertToken(p)}
                      className={cn(
                        chipVariants({ color: 'blue', size: 'small' }),
                        'cursor-pointer font-mono transition-colors not-hover:bg-transparent not-hover:text-muted-foreground not-hover:ring-transparent dark:not-hover:bg-transparent dark:not-hover:text-muted-foreground dark:not-hover:ring-transparent',
                        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
                      )}
                    >
                      {p}
                    </TooltipTrigger>
                    <TooltipContent className="max-w-xs" data-testid="ai-prompt-placeholder-hint">
                      Replaced with the job's {p === '<source_lang>' ? 'source' : 'target'} language when the prompt runs,
                      as a name and code — for example, {p === '<source_lang>' ? 'English (en)' : 'Austrian German (de-AT)'}.
                    </TooltipContent>
                  </Tooltip>
                ))}
                </div>
              </TooltipProvider>
            )}
            {errors.prompt_text && <p role="alert" className="mt-1 text-xs text-destructive">{errors.prompt_text.message}</p>}
          </div>}
        </div>

        {/* Directly under the instructions, because that is what an addition is:
            more instruction text, appended for the target languages it names.
            Closed to start — most prompts carry none. */}
        <div className="space-y-2" data-guide={guide.languages}>
          <DisclosureToggle
            open={showLanguages || !!languageError}
            onToggle={() => setShowLanguages(v => !v)}
            controls="ai-prompt-languages"
            summary={languageAdditions.length ? languagesSummary(languageAdditions) : undefined}
            variant="field"
            className="text-sm"
            data-testid="ai-prompt-languages-toggle"
          >
            Additional prompts
          </DisclosureToggle>
          {(showLanguages || !!languageError) && (
            <div id="ai-prompt-languages" className="pl-5">
              <LanguageAdditionsField control={control as never} readOnly={readOnly} error={languageError} />
            </div>
          )}
        </div>

        <div data-guide={guide.context}>
          <Controller
            control={control}
            name="segment_context"
            render={({ field }) => (
              <SegmentContextField
                mode={mode}
                value={field.value}
                onChange={field.onChange}
                readOnly={readOnly}
                revealAnchor={guide.context}
              />
            )}
          />
        </div>

        <div data-guide={guide.schema}>
          <PromptFormats mode={mode} segmentContext={segmentContext} revealAnchor={guide.schema} />
        </div>

      </div>}

      {typeChosen && <div className="flex items-center justify-end gap-2 border-t border-border pt-4">
        {readOnly && onDuplicate && (
          <Button type="button" onClick={onDuplicate} data-testid="ai-prompt-duplicate-form"><Copy /> Duplicate</Button>
        )}
        <Button type="button" variant="outline" onClick={onCancel}>{readOnly ? 'Back' : 'Cancel'}</Button>
        {!readOnly && (
          <Button type="submit" disabled={isPending || !typeChosen || (isEdit && !isDirty)} data-testid="ai-prompt-submit">
            {isPending ? 'Saving…' : isEdit ? 'Update' : 'Create'}
          </Button>
        )}
      </div>}
    </form>
  )
}
