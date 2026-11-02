import { useEffect, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Controller, useForm, type UseFormSetError } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { listLanguages, saveQARegexRule } from '../api'
import type { RegexRuleConfig } from '../types/qa'
import { queryKeys } from '../lib/query-keys'
import { SEVERITIES, SEVERITY_LABELS } from '../lib/qa-findings'
import { REGEX_MODES, SPECIAL_SPACES, hasSpecialSpace, insertAt, splitSpecialSpaces, type RegexMode } from '../lib/qa-regex'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import { useScrollToError } from '../hooks/useScrollToError'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { Combobox, MultiCombobox } from '@/components/ui/combobox'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { Chip } from '@/components/ui/chip'
import { Alert } from '@/components/ui/alert'
import { toLangOptions, langPickerProps } from '@/components/lang-options'
import { RegexSearchHelp } from './RegexSearchHelp'
import { capitalize } from '@/lib/utils'

// Required text is checked for blankness but never trimmed: a pattern or a
// message may end in a space, and special spaces must reach the server as typed.
const required = (what: string) => z.string().refine(v => v.trim() !== '', `${what} is required`)

const schema = z
  .object({
    name: required('Name'),
    mode: z.enum(['target_must_not_match', 'target_must_match', 'source_match_target_not', 'source_must_not_match']),
    pattern: required('Pattern').refine(v => v.length <= 1000, 'Pattern: at most 1,000 characters'),
    source_pattern: z.string(),
    message: required('Message').refine(v => v.length <= 300, 'Message: at most 300 characters'),
    target_languages: z.array(z.string()),
    ignore_case: z.boolean(),
    severity: z.enum(SEVERITIES),
  })
  .superRefine((v, ctx) => {
    if (v.mode === 'source_match_target_not' && v.source_pattern.trim() === '') {
      ctx.addIssue({ code: 'custom', path: ['source_pattern'], message: 'Source pattern is required' })
    }
  })
export type RegexRuleFormValues = z.infer<typeof schema>

// Fields a server rejection can name; the longer name first, since
// "source_pattern" would otherwise match "pattern".
const FIELDS = ['source_pattern', 'pattern', 'name', 'message', 'mode', 'target_languages'] as const

const serverField = (message: string) => FIELDS.find(f => message.startsWith(f + ':'))

/** The server's short message as a sentence. */
const sentence = (text: string) => capitalize(text) + (/[.!?]$/.test(text) ? '' : '.')

/** Puts a server rejection such as "pattern: missing closing )" under its field. */
function showServerError(err: Error, setError: UseFormSetError<RegexRuleFormValues>) {
  const field = serverField(err.message)
  if (field) setError(field, { message: sentence(err.message.slice(field.length + 1).trim()) })
}

function SpecialSpaceButtons({ onInsert }: { onInsert: (char: string) => void }) {
  return (
    <div className="flex gap-1">
      {SPECIAL_SPACES.map(s => (
        <Button key={s.label} type="button" variant="outline" size="xs" title={`Insert a ${s.name.toLowerCase()}`} onClick={() => onInsert(s.char)}>
          {s.label}
        </Button>
      ))}
    </div>
  )
}

/** The pattern again, with each special space shown as a chip. */
function SpecialSpacePreview({ text }: { text: string }) {
  if (!hasSpecialSpace(text)) return null
  return (
    <div className="mt-1 flex flex-wrap items-center gap-0.5 font-mono text-xs text-muted-foreground" data-testid="special-space-preview">
      {splitSpecialSpaces(text).map((p, i) =>
        'text' in p ? <span key={i} className="whitespace-pre">{p.text}</span> : <Chip key={i} size="small" color="blue">{p.special.label}</Chip>,
      )}
    </div>
  )
}

interface Props {
  profileId: number
  /** The rule to edit; null opens an empty form for a new rule. */
  rule: { id: number; severity: string; enabled: boolean; config: RegexRuleConfig } | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function QARegexRuleDialog({ profileId, rule, open, onOpenChange }: Props) {
  const { data: languages } = useQuery({ queryKey: queryKeys.languages.all, queryFn: listLanguages, enabled: open })
  const { register, control, handleSubmit, reset, watch, setValue, setError, formState: { errors } } = useForm<RegexRuleFormValues>({
    resolver: zodResolver(schema),
  })
  useScrollToError(errors)
  const fieldRefs = useRef<Record<'pattern' | 'source_pattern', HTMLInputElement | null>>({ pattern: null, source_pattern: null })

  const saveMut = useMutationWithToast({
    mutationFn: (v: RegexRuleFormValues) =>
      saveQARegexRule(profileId, {
        ruleId: rule?.id,
        enabled: rule?.enabled ?? true,
        severity: v.severity,
        config: {
          name: v.name, mode: v.mode, pattern: v.pattern, message: v.message,
          target_languages: v.target_languages, ignore_case: v.ignore_case,
          ...(v.mode === 'source_match_target_not' ? { source_pattern: v.source_pattern } : {}),
        },
      }),
    successMessage: rule ? 'Regex rule saved' : 'Regex rule added',
    errorToast: false,
    onError: err => showServerError(err, setError),
    invalidateKeys: [queryKeys.qaProfile.detail(profileId)],
    onSuccess: () => onOpenChange(false),
  })

  const resetSave = saveMut.reset
  useEffect(() => {
    if (!open) return
    resetSave()
    const c = rule?.config
    reset({
      name: c?.name ?? '',
      mode: (c?.mode as RegexMode) ?? 'target_must_not_match',
      pattern: c?.pattern ?? '',
      source_pattern: c?.source_pattern ?? '',
      message: c?.message ?? '',
      target_languages: c?.target_languages ?? [],
      ignore_case: c?.ignore_case ?? false,
      severity: (rule?.severity as RegexRuleFormValues['severity']) ?? 'error',
    })
  }, [open, rule, reset, resetSave])

  const insertSpecial = (field: 'pattern' | 'source_pattern', char: string) => {
    const el = fieldRefs.current[field]
    const value = watch(field)
    const next = insertAt(value, el?.selectionStart ?? value.length, el?.selectionEnd ?? value.length, char)
    setValue(field, next.value, { shouldDirty: true })
    requestAnimationFrame(() => {
      el?.focus()
      el?.setSelectionRange(next.caret, next.caret)
    })
  }

  const patternField = (field: 'pattern' | 'source_pattern', placeholder: string) => {
    const { ref, ...rest } = register(field)
    return (
      <>
        <Input
          {...rest}
          ref={el => { ref(el); fieldRefs.current[field] = el }}
          placeholder={placeholder}
          inputClassName="font-mono"
          clearable={false}
          spellCheck={false}
          aria-invalid={!!errors[field]}
          data-testid={`regex-${field}`}
          action={<SpecialSpaceButtons onInsert={c => insertSpecial(field, c)} />}
        />
        <SpecialSpacePreview text={watch(field) ?? ''} />
      </>
    )
  }

  const mode = watch('mode')
  const modeInfo = REGEX_MODES.find(m => m.value === mode)

  return (
    <Dialog open={open} onOpenChange={onOpenChange} keepOpen={saveMut.isPending}>
      <DialogContent className="sm:max-w-xl" aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle>{rule ? 'Edit regex rule' : 'New regex rule'}</DialogTitle>
        </DialogHeader>
        <form id="regex-rule-form" onSubmit={handleSubmit(v => saveMut.mutate(v))} className="space-y-4">
          <FormField label="Name" error={errors.name?.message} required>
            <Input {...register('name')} placeholder="e.g. Space before unit" autoFocus data-testid="regex-name" />
          </FormField>
          <FormField label="Mode" error={errors.mode?.message} required>
            <div data-field="mode">
              <Controller
                control={control}
                name="mode"
                render={({ field }) => (
                  <Combobox
                    value={field.value}
                    onChange={v => field.onChange(v as RegexMode)}
                    options={REGEX_MODES.map(m => ({ value: m.value, label: m.label }))}
                    searchable={false}
                    aria-label="Mode"
                    className="w-full"
                    data-testid="regex-mode"
                  />
                )}
              />
              {modeInfo && <p className="mt-1 text-xs text-muted-foreground">{modeInfo.description}</p>}
            </div>
          </FormField>
          {mode === 'source_match_target_not' && (
            <FormField label="Source pattern" error={errors.source_pattern?.message} required>
              {patternField('source_pattern', 'e.g. \\bACME\\b')}
            </FormField>
          )}
          <FormField label={mode === 'source_must_not_match' ? 'Pattern (source)' : 'Pattern (target)'} error={errors.pattern?.message} required action={<RegexSearchHelp variant="qa" />}>
            {patternField('pattern', 'e.g. \\d (kg|cm|%)')}
          </FormField>
          <Controller
            control={control}
            name="ignore_case"
            render={({ field }) => <CheckboxField checked={field.value} onChange={field.onChange} label="Ignore case" size="compact" />}
          />
          <FormField label="Message" error={errors.message?.message} required>
            <Input {...register('message')} placeholder="Shown in every finding of this rule" clearable={false} data-testid="regex-message" />
          </FormField>
          <FormField label="Target languages">
            <div data-field="target_languages">
              <Controller
                control={control}
                name="target_languages"
                render={({ field }) => (
                  <MultiCombobox
                    {...langPickerProps}
                    value={field.value}
                    onChange={field.onChange}
                    options={toLangOptions(languages ?? [])}
                    placeholder="All languages"
                    aria-label="Target languages"
                    className="w-full"
                    wrap
                  />
                )}
              />
              <p className="mt-1 text-xs text-muted-foreground">A language without a region, such as French, covers all its regional variants.</p>
            </div>
          </FormField>
          <FormField label="Severity" required>
            <Controller
              control={control}
              name="severity"
              render={({ field }) => (
                <Combobox
                  value={field.value}
                  onChange={v => field.onChange(v as RegexRuleFormValues['severity'])}
                  options={SEVERITIES.map(s => ({ value: s, label: SEVERITY_LABELS[s] }))}
                  searchable={false}
                  aria-label="Severity"
                  className="w-full"
                  data-testid="regex-severity"
                />
              )}
            />
          </FormField>
          {saveMut.error && !serverField(saveMut.error.message) && (
            <Alert tone="error">{sentence(saveMut.error.message)}</Alert>
          )}
        </form>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={saveMut.isPending}>Cancel</Button>
          <Button type="submit" form="regex-rule-form" disabled={saveMut.isPending} data-testid="regex-save">
            {saveMut.isPending ? 'Saving...' : 'Save'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
