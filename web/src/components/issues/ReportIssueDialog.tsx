import { useEffect, useMemo, useState } from 'react'
import { useForm, Controller } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { useLocation, useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { X } from 'lucide-react'
import {
  ApiError, createIssue, fetchChatScreenshot, type Issue, type IssueArea, type IssueCheck, type IssueKind, type IssueRecording,
  type IssueSeverity, type TrailEvent,
} from '@/api'
import { clearHeldRecording, heldRecording } from '@/lib/watch-recording'
import { actionTrail } from '@/lib/action-trail'
import { queryKeys } from '@/lib/query-keys'
import { ISSUE_VIA_ASSISTANT, currentIssueContext } from '@/lib/issue-context'
import {
  DEFAULT_ISSUE_SEVERITY, ISSUE_AREA_LABELS, ISSUE_AREA_OPTIONS, ISSUE_CONTEXT_LABELS, ISSUE_KIND_ICONS, ISSUE_KIND_LABELS,
  ISSUE_SEVERITY_OPTIONS, ISSUE_SEVERITY_ORDER,
} from '@/lib/issue-labels'
import { useAppConfig } from '@/pages/docs/DocsHost'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { ChoiceToggle } from '@/components/ui/choice-toggle'
import { Combobox } from '@/components/ui/combobox'
import { TextLink } from '@/components/ui/text-link'
import { useChatAvailable } from '@/hooks/useChatAvailable'
import { openChatWithPrompt } from '@/lib/chat-bus'
import { IssueTextField } from './IssueTextField'
import { IssueTrail } from './IssueTrail'

const AREAS = Object.keys(ISSUE_AREA_LABELS) as [IssueArea, ...IssueArea[]]

const schema = z.object({
  kind: z.enum(['bug', 'enhancement', 'feature_request']),
  severity: z.enum(ISSUE_SEVERITY_ORDER as [IssueSeverity, ...IssueSeverity[]]),
  area: z.enum(AREAS),
  title: z.string().trim().min(1, 'Add a short title.').max(200, 'Keep the title under 200 characters.'),
  description: z.string().trim().min(1, 'Describe what happened.').max(10000, 'Keep the description under 10,000 characters.'),
})
type Values = z.infer<typeof schema>

const KIND_OPTIONS = (Object.keys(ISSUE_KIND_LABELS) as IssueKind[])
  .map(value => ({ value, label: ISSUE_KIND_LABELS[value], icon: ISSUE_KIND_ICONS[value] }))


const DESCRIPTION_PLACEHOLDER: Record<IssueKind, string> = {
  bug: 'What did you do, what did you expect, and what happened instead?',
  enhancement: 'What works today, and how should it work better?',
  feature_request: 'What would you like OpenLoc to do, and what would you use it for?',
}

export interface ReportIssueDefaults {
  kind?: IssueKind
  severity?: IssueSeverity
  area?: IssueArea
  title?: string
  description?: string
  /** The AI assistant wrote it: the report says so, and the form does not offer the assistant. */
  fromAssistant?: boolean
  /** The assistant's check: sent with the report, shown only to the support team. */
  verification?: IssueCheck
  /** Screenshots the user sent the assistant: the form attaches them as files. */
  screenshotUploadIds?: string[]
}

/**
 * Reports an issue to the support team: a kind (with a severity for a bug), the
 * area, a title, what happened and any files. The page the user is on, their browser and their role go
 * with it; the dialog shows exactly what.
 */
export function ReportIssueDialog({ open, onClose, defaults, onCreated }: {
  open: boolean
  onClose: () => void
  defaults?: ReportIssueDefaults
  /** After the report is saved, before the dialog closes. */
  onCreated?: (issue: Issue) => void
}) {
  const navigate = useNavigate()
  const { pathname } = useLocation()
  const { data: config } = useAppConfig()
  const [files, setFiles] = useState<File[]>([])
  // The action trail as it stood when the form opened, and the recording of
  // the steps the user made for the assistant.
  const [trail, setTrail] = useState<TrailEvent[]>([])
  const [recording, setRecording] = useState<IssueRecording | undefined>()
  const initial = (): Values => ({
    kind: defaults?.kind ?? 'bug', severity: defaults?.severity ?? DEFAULT_ISSUE_SEVERITY,
    area: defaults?.area ?? 'other', title: defaults?.title ?? '', description: defaults?.description ?? '',
  })
  const { control, register, handleSubmit, reset, watch, formState: { errors } } = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: initial(),
  })

  // Screenshots from the chat still on their way: Send report waits for them.
  const [pending, setPending] = useState(0)
  const [offeredRecording, setOfferedRecording] = useState(false)

  // The dialog stays mounted; each opening starts from the defaults. The
  // assistant's screenshots, and the recording's, join the files when they
  // arrive, where the user sees them and may remove them.
  useEffect(() => {
    if (!open) return
    reset(initial())
    const held = defaults?.fromAssistant ? heldRecording() : undefined
    setFiles([])
    setTrail(actionTrail())
    setRecording(held?.recording)
    setOfferedRecording(!!held)
    setPending(0)
    let current = true
    const add = (load: () => Promise<File | undefined>) => {
      setPending(n => n + 1)
      load()
        .then(file => { if (current && file) setFiles(prev => [...prev, file]) })
        .catch((err: unknown) => {
          if (!current) return
          if (err instanceof ApiError && err.status === 404) {
            toast.warning('A screenshot from the chat has expired. Add it again if you need it.')
          } else {
            toast.error(`Could not add a screenshot from the chat: ${(err as Error).message}`,
              { action: { label: 'Try again', onClick: () => { if (current) add(load) } } })
          }
        })
        .finally(() => { if (current) setPending(n => n - 1) })
    }
    if (held) add(() => held.screenshot)
    for (const id of defaults?.screenshotUploadIds ?? []) add(() => fetchChatScreenshot(id))
    return () => { current = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  const fromAssistant = !!defaults?.fromAssistant
  const context = useMemo(
    () => ({ ...currentIssueContext(pathname, config?.version), ...(fromAssistant && { via: ISSUE_VIA_ASSISTANT }) }),
    [pathname, config?.version, fromAssistant],
  )

  const createMut = useMutationWithToast({
    mutationFn: ({ severity, ...values }: Values) =>
      createIssue({
        ...values, severity: values.kind === 'bug' ? severity : undefined, context, files,
        verification: defaults?.verification, trail, recording,
      }),
    // The thanks carries a View action, so it is shown here.
    successMessage: () => null,
    // The operators' list too: an operator reports from it as well.
    invalidateKeys: [queryKeys.issues.all, queryKeys.platform.issues],
    onSuccess: (issue) => {
      if (offeredRecording) clearHeldRecording()
      onCreated?.(issue)
      toast.success('Thanks, the support team has your report.', {
        action: { label: 'View', onClick: () => navigate(`/issues/${issue.id}`) },
      })
      onClose()
    },
  })

  const kind = watch('kind')
  const chatAvailable = useChatAvailable()
  // The assistant asks for what it needs, fills in the report and shows it for
  // confirmation; the page the user is on goes with the chat message.
  const askAssistant = () => {
    onClose()
    openChatWithPrompt(defaults?.title
      ? `Report this error to the support team as a bug: "${defaults.title}". `
      : 'I want to report an issue to the support team: ')
  }
  const contextRows = Object.entries(context).filter(([, v]) => v !== undefined && v !== '')

  return (
    <Dialog open={open} keepOpen={createMut.isPending} onOpenChange={o => { if (!o) onClose() }}>
      {/* A column capped at 90vh in which only the form scrolls: the header and
          the footer stay put, so Send report is always on screen. */}
      <DialogContent className="flex max-h-[90vh] flex-col sm:max-w-lg" data-testid="report-issue-dialog">
        <DialogHeader>
          <DialogTitle>Report an issue</DialogTitle>
          <DialogDescription>The support team reads every report. You can follow it under Reported issues.</DialogDescription>
        </DialogHeader>
        {chatAvailable && !fromAssistant && (
          <p className="text-sm text-muted-foreground" data-testid="report-issue-assistant">
            Prefer to tell it in your own words?{' '}
            <TextLink onClick={askAssistant} data-testid="report-issue-ask-assistant">Ask the assistant</TextLink>
            {' '}to write the report for you.
          </p>
        )}
        {/* -mx-1 px-1: room for the fields' focus rings inside the scroll area. */}
        <form id="report-issue-form" className="-mx-1 min-h-0 flex-1 space-y-4 overflow-y-auto px-1" onSubmit={handleSubmit(v => createMut.mutate(v))}>
          <Controller
            control={control}
            name="kind"
            render={({ field }) => (
              <ChoiceToggle value={field.value} onChange={field.onChange} options={KIND_OPTIONS}
                aria-label="Kind of report" data-testid="report-issue-kind" size="sm" />
            )}
          />
          <div className="flex flex-wrap gap-4">
            {kind === 'bug' && (
              <FormField label="Severity">
                <Controller
                  control={control}
                  name="severity"
                  render={({ field }) => (
                    <ChoiceToggle value={field.value} onChange={field.onChange} options={ISSUE_SEVERITY_OPTIONS}
                      aria-label="Severity" data-testid="report-issue-severity" size="sm" />
                  )}
                />
              </FormField>
            )}
            <FormField label="Area">
              <Controller
                control={control}
                name="area"
                render={({ field }) => (
                  <Combobox value={field.value} onChange={v => field.onChange(v as IssueArea)} options={ISSUE_AREA_OPTIONS}
                    className="w-52" aria-label="Area" data-testid="report-issue-area" />
                )}
              />
            </FormField>
          </div>
          <FormField label="Title" error={errors.title?.message} required>
            <Input {...register('title')} placeholder="In a few words" data-testid="report-issue-title" />
          </FormField>
          <FormField label="What happened?" error={errors.description?.message} required>
            <Controller
              control={control}
              name="description"
              render={({ field }) => (
                <IssueTextField
                  value={field.value}
                  onChange={field.onChange}
                  onBlur={field.onBlur}
                  name={field.name}
                  files={files}
                  onFilesChange={setFiles}
                  disabled={createMut.isPending}
                  rows={5}
                  placeholder={DESCRIPTION_PLACEHOLDER[kind]}
                  data-testid="report-issue-description"
                  filesTestId="report-issue-files"
                  hint="Screenshots, logs, the file you were working on: up to 10 files of 25 MB. Paste or drop a screenshot where it belongs in the text."
                />
              )}
            />
          </FormField>
          <div className="rounded-md bg-muted px-3 py-2 text-xs text-muted-foreground" data-testid="report-issue-context">
            <p className="mb-1 font-medium text-foreground">We&rsquo;ll include</p>
            <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
              {contextRows.map(([k, v]) => (
                <div key={k} className="contents">
                  <dt>{ISSUE_CONTEXT_LABELS[k] ?? k}</dt>
                  <dd className="truncate font-mono">{String(v)}</dd>
                </div>
              ))}
              <dt>Role</dt>
              <dd>Your role in this organization</dd>
            </dl>
            <IssueTrail events={trail} label="Your recent actions" testId="report-issue-trail" />
            {recording && (
              <div className="flex items-start gap-1">
                <IssueTrail events={recording.events} label="The steps you recorded" testId="report-issue-recording" />
                <Button variant="ghost" size="icon-xs" onClick={() => setRecording(undefined)}
                  aria-label="Leave out the steps you recorded" title="Leave out the steps you recorded"
                  data-testid="report-issue-recording-remove">
                  <X />
                </Button>
              </div>
            )}
          </div>
        </form>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={createMut.isPending}>Cancel</Button>
          <Button type="submit" form="report-issue-form" loading={createMut.isPending} disabled={pending > 0}
            disabledReason={pending > 0 ? 'Adding the screenshots from the chat.' : undefined} data-testid="report-issue-submit">
            Send report
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
