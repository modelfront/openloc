import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { LayoutTemplate, Plus, Trash2 } from 'lucide-react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useForm, Controller } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import {
  publishProject,
  listLanguages,
  listFiles,
  listTMCollections,
  listMTRouters,
  listTermBases,
  listQAProfiles,
  listTMProfiles,
  createTemplate,
  getMe,
  listLabels,
  listProjectLabels,
  setProjectLabels,
  listTemplateLabels,
  setTemplateLabels,
  type Project,
} from '../api'
import { toast } from 'sonner'
import { queryKeys } from '../lib/query-keys'
import { useProjectCreateSettings, workflowStepsError, pretranslateRouterError } from '../hooks/useProjectCreateSettings'
import { useProjectDraft } from '../hooks/useProjectDraft'
import { isAdminRole, isManagerRole } from '../lib/roles'
import { buildProjectCreatePayload, buildProjectSetupPayload } from '../lib/project-create-payload'
import { ProjectCreateWorkflowCard } from '../components/project/ProjectCreateWorkflowCard'
import { focusMTRouter } from '../components/workflow/RawMTPane'
import { TemplatePickerDialog } from '../components/project/TemplatePickerDialog'
import { SaveAsTemplateDialog } from '../components/project/SaveAsTemplateDialog'
import { ProjectCreateFilesCard } from '../components/project/ProjectCreateFilesCard'
import { ManageLabelsDialog } from '../components/project/ManageLabelsDialog'
import { ProjectLabelFields, type LabelPick } from '../components/project/ProjectLabelFields'
import ConfirmDialog from '../components/ConfirmDialog'
import { Button } from '@/components/ui/button'
import { Combobox, MultiCombobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { DeadlinePicker } from '@/components/ui/deadline-picker'
import { useScrollToError } from '../hooks/useScrollToError'
import { langPickerProps, toLangOptions } from '../components/lang-options'
import { SettingsEditor } from '../components/SettingsEditor'
import { PageHeader } from '@/components/layout/PageHeader'
import { Chip } from '@/components/ui/chip'
import { STATUS_COLORS, STATUS_LABELS } from '../components/project/ProjectStatusSelect'
import { PageContainer } from '@/components/layout/PageContainer'
import { TextLink } from '@/components/ui/text-link'
import { errorText } from '@/lib/error-text'

const projectSchema = z.object({
  name: z.string().min(1, 'Project name is required'),
  source_language: z.string().min(1, 'Source language is required'),
  target_languages: z.array(z.string()).min(1, 'Add at least one target language'),
})

type ProjectFormValues = z.infer<typeof projectSchema>

type StartMode = 'scratch' | 'template'

// Autosave state, shown beside the Draft chip so the user can see that
// everything they type is being kept.
const SAVE_STATE_LABEL = {
  idle: '',
  saving: 'Saving…',
  saved: 'Saved',
  error: 'Draft not saved',
} as const

// The New Project wizard. The form is a draft project on
// the server from the first edit on: every change autosaves through
// useProjectDraft, so leaving the page loses nothing and /projects/new?draft=
// brings the form back. "Create project" only publishes the draft.
export default function ProjectCreate() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [searchParams] = useSearchParams()
  const resumedId = useMemo(() => {
    const raw = searchParams.get('draft')
    const n = raw ? Number(raw) : NaN
    return Number.isInteger(n) && n > 0 ? n : null
  }, [searchParams])

  const {
    register,
    handleSubmit,
    control,
    setValue,
    getValues,
    getFieldState,
    watch,
    reset,
    formState: { errors, isSubmitting },
  } = useForm<ProjectFormValues>({
    resolver: zodResolver(projectSchema),
    defaultValues: {
      name: '',
      source_language: 'en',
      target_languages: [],
    },
  })

  useScrollToError(errors)

  // Project metadata outside the zod schema: all optional, so local
  // state suffices. The deadline is a UTC ISO string, null for none.
  const [customerID, setCustomerID] = useState<number | null>(null)
  const [deadline, setDeadline] = useState<string | null>(null)
  const [domain, setDomain] = useState<string>('')
  const [analyzeUploaded, setAnalyzeUploaded] = useState(true)
  const [labelsDialogOpen, setLabelsDialogOpen] = useState(false)
  // The project's label picks. Not part of the draft payload — labels have
  // their own endpoint, so they are saved against the draft as they change.
  const [labelPicks, setLabelPicks] = useState<LabelPick[]>([])
  // Set once the ?draft= prefill has landed; ends the autosave's hydration.
  const [resumed, setResumed] = useState(false)

  // Tri-state: undefined while `me` loads.
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const isManager: boolean | undefined = me === undefined ? undefined : isManagerRole(me?.role)

  // Resource queries
  const { data: labels } = useQuery({ queryKey: queryKeys.labels.all, queryFn: listLabels })
  const { data: languages } = useQuery({ queryKey: queryKeys.languages.all, queryFn: listLanguages })
  const { data: tmCollections } = useQuery({ queryKey: queryKeys.tms.all, queryFn: listTMCollections })
  const { data: mtRouters } = useQuery({ queryKey: queryKeys.mtRouters.all, queryFn: listMTRouters })
  const { data: termBases } = useQuery({ queryKey: queryKeys.terminology.all, queryFn: listTermBases })
  const { data: qaProfiles } = useQuery({ queryKey: queryKeys.qaProfiles.all, queryFn: listQAProfiles })
  const { data: tmProfiles } = useQuery({ queryKey: queryKeys.tmProfiles.all, queryFn: listTMProfiles })

  const langOptions = languages ?? []

  // Settings draft + "Start from" prefill + workflow step draft. The prefill
  // (a template or an existing project) seeds languages + settings + name,
  // domain and customer — and is deep-linkable via ?fromTemplate= /
  // ?fromProject= (the Duplicate and "Use as template" actions use those).
  // ?draft= resumes a saved draft, metadata included (applyDraft).
  const {
    settings, currentSettings, workflows, templateId, templateName, fromProjectId, fromProjectName, workflowId, hasStartFromDeepLink,
    handleTemplateChange, handleWorkflowChange, setWorkflowSteps, resetToDefaults, handleSettingsChange,
  } = useProjectCreateSettings({
    ready: tmCollections !== undefined && termBases !== undefined,
    tmCollections,
    termBases,
    applyLanguages: (source, targets) => {
      if (source) setValue('source_language', source)
      if (targets.length > 0) setValue('target_languages', targets, { shouldValidate: true })
    },
    // A template brings its project name, customer and domain; a project its own.
    // The name fills an empty or untouched field only: the source's settings
    // arrive after the pick, and a name typed meanwhile is the user's.
    applyMetadata: ({ name, domain, customerId }) => {
      if (!getFieldState('name').isDirty || !getValues('name').trim()) {
        setValue('name', name, { shouldValidate: true })
      }
      setDomain(domain ?? '')
      setCustomerID(customerId)
    },
    applyDraft: (project: Project) => {
      setValue('name', project.name)
      // A draft may have no languages yet; applyLanguages skips empties.
      setValue('source_language', project.source_language)
      setValue('target_languages', project.target_languages)
      setCustomerID(project.customer_id ?? null)
      setDeadline(project.deadline ?? null)
      setDomain(project.domain ?? '')
      // Labels are not on the project payload, so a resume fetches them.
      // Straight into state: this is what the server already holds, so it must
      // not round-trip back out through handleLabelsChange.
      listProjectLabels(project.id)
        .then(rows => setLabelPicks(rows.map(r => ({ definition_id: r.label_definition_id, value_id: r.label_value_id }))))
        .catch(() => toast.error('Failed to load project labels'))
      setResumed(true)
    },
  })

  const draft = useProjectDraft({
    initialId: resumedId,
    buildPayload: () => {
      const v = getValues()
      return buildProjectCreatePayload({
        name: v.name,
        sourceLanguage: v.source_language,
        targetLanguages: v.target_languages,
        customerId: customerID,
        deadline,
        domain,
        templateId,
        workflowId,
      }, currentSettings())
    },
  })
  const { scheduleSave, scheduleSaveExisting, endHydration } = draft

  // Labels are stored per project, not inside the draft payload, so a change
  // needs a project to attach to: ensureDraft creates one on the first edit and
  // returns the existing id afterwards. Rows without a value yet are left out —
  // an unanswered label is not a pick.
  const handleLabelsChange = async (next: LabelPick[]) => {
    setLabelPicks(next)
    try {
      const id = await draft.ensureDraft()
      await setProjectLabels(id, next
        .filter(p => p.value_id != null)
        .map(p => ({ label_definition_id: p.definition_id, label_value_id: p.value_id! })))
    } catch (err) {
      toast.error(`Failed to save labels: ${(err as Error).message}`)
    }
  }
  useEffect(() => {
    if (resumed) endHydration()
  }, [resumed, endHydration])


  // Starting from a template brings its label presets, the same way it brings
  // languages and settings.
  useEffect(() => {
    const id = templateId ? Number(templateId) : null
    if (id == null || Number.isNaN(id) || resumedId != null) return
    let cancelled = false
    listTemplateLabels(id)
      .then(rows => {
        if (cancelled || rows.length === 0) return
        void handleLabelsChange(rows.map(r => ({ definition_id: r.label_definition_id, value_id: r.label_value_id })))
      })
      .catch(() => toast.error('Failed to copy the labels of the template this was started from'))
    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [templateId, resumedId])

  // Duplicating a project copies its classification too: labels live on their
  // own endpoint, so the prefill that carries name, languages and settings
  // cannot bring them along. Saved rather than just shown — the picks have to
  // reach the draft or the copy publishes without them. Skipped while resuming
  // a draft, which loads its own picks.
  useEffect(() => {
    const sourceId = fromProjectId ? Number(fromProjectId) : null
    if (sourceId == null || Number.isNaN(sourceId) || resumedId != null) return
    let cancelled = false
    listProjectLabels(sourceId)
      .then(rows => {
        if (cancelled || rows.length === 0) return
        void handleLabelsChange(rows.map(r => ({ definition_id: r.label_definition_id, value_id: r.label_value_id })))
      })
      .catch(() => toast.error('Failed to copy the labels of the project this was started from'))
    return () => { cancelled = true }
    // handleLabelsChange is recreated each render; the source id is what
    // decides whether this should run at all.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fromProjectId, resumedId])

  // Every user edit schedules a save: the field handlers, the metadata state
  // setters and the settings callbacks are all wrapped below. Prefills and the
  // default workflow seeding go around the wrappers on purpose — a fresh,
  // untouched wizard must not create a draft. (Not a `watch` subscription:
  // that also fires when the Controller fields register on mount.)
  const saving = useCallback(<A extends unknown[]>(fn: (...args: A) => unknown) => (...args: A) => {
    fn(...args)
    scheduleSave()
  }, [scheduleSave])
  // Async pickers fetch before they apply; save once the settings are in.
  const savingAfter = useCallback(<A extends unknown[]>(fn: (...args: A) => Promise<unknown>) => async (...args: A) => {
    await fn(...args)
    scheduleSave()
  }, [scheduleSave])
  // Same, for the prefill choices. They rewrite the form wholesale before the
  // user has filled anything in, so they keep an open draft in step without
  // creating one — as "From scratch" always has. Discard uses neither
  // wrapper: it deletes the draft.
  const startingAfter = useCallback(<A extends unknown[]>(fn: (...args: A) => Promise<unknown>) => async (...args: A) => {
    await fn(...args)
    scheduleSaveExisting()
  }, [scheduleSaveExisting])

  // "Start from" choice. The two buttons show until the start is settled: From
  // scratch settles at once; From template settles when a template is picked
  // in the dialog. Then the buttons give way to a line naming the source (if
  // any), the header gains Discard, and the form renders. A ?fromTemplate= /
  // ?fromProject= deep link is a settled template start; a resumed draft is
  // already started, whatever it was started from.
  const [startMode, setStartMode] = useState<StartMode | null>(
    resumedId != null ? 'scratch' : hasStartFromDeepLink ? 'template' : null,
  )
  const [templateDialogOpen, setTemplateDialogOpen] = useState(false)
  // Where the choice falls back when the picker closes without a pick.
  const modeBeforeDialog = useRef<StartMode | null>(null)
  const hasSource = !!templateId || !!fromProjectId
  const started = startMode === 'scratch' || (startMode === 'template' && hasSource)

  const handleStartModeChange = (mode: StartMode) => {
    if (mode === 'template') {
      modeBeforeDialog.current = startMode
      setStartMode('template')
      setTemplateDialogOpen(true)
      return
    }
    setStartMode('scratch')
    if (hasSource) void startingAfter(resetToDefaults)()
  }
  const handleTemplatePicked = (id: string) => {
    setTemplateDialogOpen(false)
    setStartMode('template')
    void startingAfter(handleTemplateChange)(id)
  }
  const handleTemplateDialogChange = (open: boolean) => {
    setTemplateDialogOpen(open)
    if (!open && !hasSource) setStartMode(modeBeforeDialog.current)
  }
  // Discard throws the draft away — its files and their segments go with it,
  // and the delete is not reversible — so it always asks first. It is only
  // rendered while a draft exists, so there is always something to ask about.
  const [discardOpen, setDiscardOpen] = useState(false)
  const requestDiscard = () => setDiscardOpen(true)
  // The draft's own files, for the warning's wording. Same query key as the
  // Files card, so this shares its cache entry rather than fetching again.
  const { data: draftFilesPage } = useQuery({
    queryKey: queryKeys.files.byProject(draft.draftId ?? 0),
    queryFn: () => listFiles(draft.draftId!),
    enabled: draft.draftId != null,
  })
  const draftFileCount = draftFilesPage?.data?.length ?? 0

  // Back to the untouched page: no start chosen, empty form, default settings
  // — and the draft is deleted, not emptied, so an abandoned start leaves no
  // "Untitled draft" in the project list. Typing again creates a new one.
  const discard = async () => {
    setStartMode(null)
    reset()
    setCustomerID(null)
    setDeadline(null)
    setDomain('')
    setLabelPicks([])
    setAnalyzeUploaded(true)
    await draft.discardDraft()
    await resetToDefaults()
    // Nothing in the query string survives a discard: the draft is gone and
    // a prefill deep link would re-apply the source the user just dropped.
    if (window.location.search) navigate('/projects/new', { replace: true })
  }
  const sourceCaption = fromProjectId
    ? <>Settings copied from project <span className="font-medium text-gray-700 dark:text-gray-300">{fromProjectName || '…'}</span></>
    : templateId
      ? <>Using template <span className="font-medium text-gray-700 dark:text-gray-300">{templateName || '…'}</span></>
      : null

  // Save as template: the whole form (name, languages, customer, domain and
  // the setup) becomes a template; the page stays as it is.
  const [saveAsOpen, setSaveAsOpen] = useState(false)
  const openSaveAsTemplate = () => {
    const stepsError = workflowStepsError(currentSettings().workflowSteps, workflowId)
    if (stepsError) {
      toast.error(stepsError)
      return
    }
    setSaveAsOpen(true)
  }
  const saveAsTemplate = async (name: string) => {
    try {
      const values = watch()
      const tmpl = await createTemplate({
        name,
        project_name: values.name.trim(),
        source_language: values.source_language,
        target_languages: values.target_languages,
        ...buildProjectSetupPayload(workflowId, currentSettings()),
      })
      // Labels have their own endpoint, so the form's picks are saved onto the
      // new template rather than riding along in its create payload.
      const picks = labelPicks
        .filter(p => p.value_id != null)
        .map(p => ({ label_definition_id: p.definition_id, label_value_id: p.value_id! }))
      if (picks.length > 0) await setTemplateLabels(tmpl.id, picks)
      queryClient.invalidateQueries({ queryKey: queryKeys.templates.all })
      setSaveAsOpen(false)
      toast.success(`Template “${tmpl.name}” saved`, {
        action: { label: 'Open template', onClick: () => navigate(`/templates/${tmpl.id}`) },
      })
    } catch (err) {
      toast.error(`Save as template failed: ${errorText(err, String(err))}`)
      throw err
    }
  }

  // The zod pass has already run when this is reached; the values themselves
  // travel through buildPayload, so the argument isn't needed here.
  const onSubmit = async () => {
    try {
      const stepsError = workflowStepsError(currentSettings().workflowSteps, workflowId)
      if (stepsError) {
        toast.error(stepsError)
        return
      }
      const routerError = pretranslateRouterError(currentSettings())
      if (routerError) {
        toast.error(routerError)
        focusMTRouter()
        return
      }
      // The server publishes what it has, so the form is saved once more
      // first — this also creates the draft for a form that was filled in
      // under the debounce.
      const id = await draft.saveNow()
      const project = await publishProject(id, analyzeUploaded)

      // Invalidate BOTH project list caches so the new project shows up when
      // the user navigates back to /projects:
      //   - projects.all       : full list (ProjectDetail / CATEditor read this)
      //   - projects.paginated : useInfiniteQuery on /projects
      queryClient.invalidateQueries({ queryKey: queryKeys.projects.all })
      queryClient.invalidateQueries({ queryKey: queryKeys.projects.paginated })
      // Creating a project can introduce a brand-new creator, so the
      // "Created by" chip's option set changes.
      queryClient.invalidateQueries({ queryKey: queryKeys.projects.creators })
      // The draft's files now have jobs.
      queryClient.invalidateQueries({ queryKey: queryKeys.jobs.byProject(project.id) })

      toast.success('Project created')
      navigate(`/projects/${project.id}`)
    } catch (err) {
      toast.error(`Create failed: ${(err as Error).message}`)
    }
  }

  return (
    <PageContainer>
      {/* Sticky: the form is long, and the Draft chip, the save state and
          Discard are what tells the user their work is being kept. `-mx-6 px-6`
          cancels PageContainer's padding so the bar spans the container.
          A sticky child sticks below its scroll container's top padding, and
          <main> has `pt-6` — so the bar is pulled up by that padding (`-mt-6`,
          `-top-6`) and takes it back inside (`pt-7` = the padding plus the
          bar's own pt-1). Without that, form rows scroll through the 24px
          strip above the bar.
          The background is `bg-background`, the colour the shell paints the
          page with, so the bar is not a panel over the page and needs no
          bottom rule — rows just disappear under it. A gray step is wrong
          here: a palette maps the gray scale onto card shades, not onto the
          page background. */}
      <div className="sticky -top-6 z-20 -mx-4 md:-mx-6 -mt-6 mb-6 bg-background px-4 md:px-6 pt-7 pb-3" data-testid="project-create-header">
        <TextLink to="/projects" className="text-sm mb-4 inline-block">
          &larr; Projects
        </TextLink>

        <PageHeader
          className="mb-0"
          documentTitle="New project"
          title={
            <span className="flex items-center gap-2">
              New project
              {/* The list row's chip, so a saved draft reads the same here as
                  there. Keyed on the row existing, not on the save state: it
                  shows on the first save, survives a resume, leaves with
                  Discard. */}
              {draft.draftId != null && (
                <Chip color={STATUS_COLORS.draft} data-testid="project-status-badge-draft">
                  {STATUS_LABELS.draft}
                </Chip>
              )}
              {/* Beside the chip, because it is about the same thing: the row
                  on the server and whether it is up to date. */}
              <span
                className={draft.state === 'error'
                  ? 'text-xs text-red-600 dark:text-red-400'
                  : 'text-xs text-gray-400 dark:text-gray-500'}
                data-testid="draft-save-state"
                data-state={draft.state}
              >
                {SAVE_STATE_LABEL[draft.state]}
              </span>
            </span>
          }
          actions={
            <>
              {/* Red because it deletes: the draft, its files, their segments.
                  Gated like the Draft chip — no draft, nothing to discard, no
                  button. An untouched form is abandoned by leaving the page. */}
              {draft.draftId != null && (
                <Button type="button" variant="destructive" size="sm" onClick={requestDiscard} data-testid="discard">
                  <Trash2 />
                  Discard
                </Button>
              )}
            </>
          }
        />
      </div>

      {/* Start from — first on purpose: a template pre-fills languages and
          settings, so choosing it after filling the form would overwrite what
          the user typed. The form itself waits for this choice. */}
      {!started ? (
        <div role="group" aria-label="Start from" className="mb-6 flex flex-wrap items-center gap-2" data-testid="start-from" data-guide="project_create.start">
          {/* Template is the primary action: it is the faster path and the
              one a returning user takes. Scratch is the fallback. */}
          <Button type="button" onClick={() => handleStartModeChange('template')} data-testid="start-from-template">
            <LayoutTemplate />
            From template
          </Button>
          <Button type="button" variant="outline" onClick={() => handleStartModeChange('scratch')} data-testid="start-from-scratch">
            <Plus />
            From scratch
          </Button>
        </div>
      ) : sourceCaption ? (
        <p className="mb-6 text-sm text-gray-500 dark:text-gray-400" data-testid="start-from-source">
          {sourceCaption}
        </p>
      ) : null}

      <TemplatePickerDialog
        open={templateDialogOpen}
        onOpenChange={handleTemplateDialogChange}
        selectedId={templateId}
        onPick={handleTemplatePicked}
      />

      {/* Outside the <form>: a dialog nested in it bubbles Enter through the
          React tree to the outer onSubmit. */}
      <ManageLabelsDialog
        open={labelsDialogOpen}
        onClose={() => setLabelsDialogOpen(false)}
        canEdit={isManager === true}
        canDelete={isAdminRole(me?.role)}
      />

      {started && (
      <form onSubmit={handleSubmit(onSubmit)} onBlur={draft.flushPending} className="space-y-6" data-testid="project-create-form">
        {/* Settings Editor. The project's own fields open its Project settings
            card; the Workflow card follows it, before TM / MT / terminology. */}
        <SettingsEditor
          mode="create"
          projectFields={
            <>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <FormField label="Project name" error={errors.name?.message} required>
              <Input
                {...register('name', { onChange: scheduleSave })}
                placeholder="Project name"
              />
            </FormField>
            <FormField label="Deadline (optional)">
              <DeadlinePicker
                variant="field"
                value={deadline}
                onChange={saving(setDeadline)}
                aria-label="Project deadline"
                data-testid="project-deadline-input"
                className="w-full"
              />
            </FormField>
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <FormField label="Source language" error={errors.source_language?.message} required>
              <Controller
                name="source_language"
                control={control}
                render={({ field }) => (
                  <div data-field="source_language">
                    <Combobox
                      options={toLangOptions(langOptions)}
                      value={field.value}
                      onChange={saving(field.onChange)}
                      placeholder="Source language"
                      className="w-full"
                      // A language pick IS its code, so the chip is the whole
                      // answer and the name would only repeat it.
                      triggerDisplay="chip"
                      {...langPickerProps}
                    />
                  </div>
                )}
              />
            </FormField>
            <FormField label="Target languages" error={errors.target_languages?.message} required>
              <Controller
                name="target_languages"
                control={control}
                render={({ field }) => (
                  <div data-field="target_languages">
                    <MultiCombobox
                      options={toLangOptions(langOptions)}
                      value={field.value}
                      onChange={saving(field.onChange)}
                      placeholder="Add target languages..."
                      className="w-full"
                      {...langPickerProps}
                    />
                  </div>
                )}
              />
            </FormField>
          </div>
          <div className="pt-4 border-t border-gray-200 dark:border-gray-800">
            <div className="mb-3 flex items-center justify-between gap-2">
              <h4 className="text-sm font-medium text-gray-700 dark:text-gray-300">Labels</h4>
              {isManager === true && (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setLabelsDialogOpen(true)}
                  data-testid="manage-labels-button"
                >
                  Manage labels
                </Button>
              )}
            </div>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                            {/* Customer and Domain are built-in labels, so they come
                  from here like every other one. */}
              <ProjectLabelFields
                labels={labels ?? []}
                value={labelPicks}
                onChange={handleLabelsChange}
                disabled={isManager === undefined}
              />
            </div>
          </div>
            </>
          }
          afterProjectSettings={({ mtRouterName, mtOnUpload }) => (
        <ProjectCreateWorkflowCard
          workflows={workflows ?? []}
          workflowId={workflowId}
          steps={settings.workflowSteps}
          onPickWorkflow={savingAfter(handleWorkflowChange)}
          targetLanguages={watch('target_languages')}
          onStepsChange={saving(setWorkflowSteps)}
          mtRouterName={mtRouterName}
          mtOnUpload={mtOnUpload}
        />
          )}
          values={settings}
          tmCollections={tmCollections?.map(t => ({ id: t.id, name: t.name, source_language: t.source_language, target_languages: t.target_languages })) ?? []}
          mtRouters={mtRouters ?? []}
          termBases={termBases?.map(t => ({ id: t.id, name: t.name, source_language: t.source_language, target_languages: t.target_languages ?? [] })) ?? []}
          qaProfiles={qaProfiles?.map(p => ({ id: p.id, name: p.name })) ?? []}
          tmProfiles={tmProfiles?.map(p => ({ id: p.id, name: p.name })) ?? []}
          languages={languages}
          sourceLanguage={watch('source_language')}
          targetLanguages={watch('target_languages')}
          onChange={saving(handleSettingsChange)}
        />

        {/* Files go into the draft as they are picked; jobs come with publish. */}
        <ProjectCreateFilesCard
          draftId={draft.draftId}
          ensureDraft={draft.ensureDraft}
          analyze={analyzeUploaded}
          onAnalyzeChange={setAnalyzeUploaded}
        />

        {/* Actions. Save as template keeps the page; Create project leaves it. */}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={openSaveAsTemplate} disabled={isSubmitting} data-testid="save-as-template">
            Save as template
          </Button>
          <Button type="submit" disabled={isSubmitting} data-guide="project_create.submit">
            {isSubmitting ? 'Creating...' : 'Create project'}
          </Button>
        </div>
      </form>
      )}

      <ConfirmDialog
        open={discardOpen}
        onOpenChange={setDiscardOpen}
        title="Discard draft?"
        description={draftFileCount > 0
          ? `This deletes the draft and the ${draftFileCount === 1 ? 'file' : `${draftFileCount} files`} you have uploaded into it. You cannot undo this.`
          : 'This deletes the draft you have been filling in. You cannot undo this.'}
        actionLabel="Discard"
        onConfirm={() => { void discard() }}
      />

      <SaveAsTemplateDialog
        open={saveAsOpen}
        onOpenChange={setSaveAsOpen}
        defaultName={watch('name').trim()}
        onSave={saveAsTemplate}
      />
    </PageContainer>
  )
}
