import { useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { toast } from 'sonner'
import { getProjectSettings, getTemplate, getWorkflow, type Project, type WorkflowDetail } from '../api'
import { projectSettingsToValues } from '../lib/settings-values'
import { isPretranslateOnly, workflowDefinitionStepsToDrafts, workflowStepsFromApi } from '../lib/workflow-steps'
import type { SettingsValues } from '../components/SettingsEditor'
import { DEFAULT_AUTO_COMPLETE, DEFAULT_INLINE_HTML, DEFAULT_PENALTY_PCT, toTMLookupOrder } from '@/lib/settings-values'
import { errorText } from '@/lib/error-text'

interface PrefillCallbacks {
  defaultSettings: SettingsValues
  // Deep-link prefill waits for this (resource queries loaded) — template
  // language derivation and term-base names need tmCollections/termBases.
  ready: boolean
  tmCollections?: { id: number; name: string; source_language?: string | null; target_languages?: string[] | null }[]
  termBases?: { id: number; name: string }[]
  applySettings: (values: SettingsValues) => void
  /** The form's live settings draft — the workflow prefill replaces only its
   *  step list and must not clobber the rest. */
  currentSettings: () => SettingsValues
  // source is undefined when it can't be derived unambiguously (template path).
  applyLanguages: (source: string | undefined, targets: string[]) => void
  /** The source's project name, domain and customer. */
  applyMetadata: (meta: { name: string; domain: string | null; customerId: number | null }) => void
  /** A resumed draft (?draft=) also brings its metadata back — name, customer,
   *  deadline, domain — which no other prefill touches. Called after the
   *  languages and settings have been applied. */
  applyDraft?: (project: Project) => void
}

// "Start from" state for the New Project page: an optional template OR an
// existing project whose settings seed the form. Mutually exclusive — picking
// one clears the other. Deep-linkable via ?fromTemplate=ID / ?fromProject=ID
// (the project list's Duplicate action and the project page's "Use as
// template" button use the latter). Everything is an ephemeral prefill except
// the template, whose id is sent at create time so the backend applies +
// stamps it.
//
// ?draft=ID is the third entry point: it resumes a saved
// draft — languages, settings, the template/workflow provenance and, through
// applyDraft, the metadata. That one is not a prefill; it is the project.
export function usePrefillFromSource(cb: PrefillCallbacks) {
  const [templateId, setTemplateId] = useState('')
  const [fromProjectId, setFromProjectId] = useState('')
  // Names of the source project / template, for the "Settings copied from …"
  // and "Using template …" captions.
  const [fromProjectName, setFromProjectName] = useState('')
  const [templateName, setTemplateName] = useState('')
  const [searchParams] = useSearchParams()

  // The callbacks close over fresh state each render; the mount effect below
  // must see the latest without re-running.
  const cbRef = useRef(cb)
  cbRef.current = cb

  // Guards against a stale fetch response applying after the user switched
  // selections (pick template A → immediately pick "None"/a project → A's
  // late response must not overwrite). Bumped on every selection change;
  // handlers bail after each await if theirs is no longer current.
  const reqSeq = useRef(0)

  // Draft link to a library workflow ('' = Custom). Set by the Workflow card,
  // and by a template / project prefill from that source's own provenance.
  const [workflowId, setWorkflowId] = useState('')

  const handleTemplateChange = async (id: string) => {
    const seq = ++reqSeq.current
    setTemplateId(id)
    setTemplateName('')
    setFromProjectId('')
    setFromProjectName('')
    const c = cbRef.current
    if (!id) {
      c.applySettings(c.defaultSettings)
      setWorkflowId('')
      return
    }
    try {
      const detail = await getTemplate(Number(id))
      if (seq !== reqSeq.current) return
      const t = detail.template
      setTemplateName(t.name)

      // The template's own languages when it has them. Otherwise derive them
      // from its assignments: TM assignments carry no languages, so
      // each assigned TM's declared source/targets stand in; MT assignments
      // still carry a language pair.
      const sources = new Set<string>()
      const targets = new Set<string>(t.target_languages ?? [])
      if (t.source_language) sources.add(t.source_language)
      if (sources.size === 0 && targets.size === 0) {
        for (const a of detail.tm_assignments) {
          const tm = c.tmCollections?.find(t => t.id === a.tm_id)
          if (tm?.source_language) sources.add(tm.source_language)
          for (const t of tm?.target_languages ?? []) targets.add(t)
        }
      }
      c.applyLanguages(sources.size === 1 ? [...sources][0] : undefined, [...targets])

      c.applySettings({
        fuzzyThreshold: t.fuzzy_threshold,
        sublanguagePenaltyPct: t.sublanguage_penalty_pct ?? DEFAULT_PENALTY_PCT,
        nearExactPenaltyPct: t.near_exact_penalty_pct ?? DEFAULT_PENALTY_PCT,
        tmLookupOrder: toTMLookupOrder(t.tm_lookup_order),
        mtOnUpload: t.mt_on_upload,
        tmOnUpload: t.tm_on_upload,
        autoComplete: t.auto_complete ?? DEFAULT_AUTO_COMPLETE,
        inlineHTML: t.inline_html ?? DEFAULT_INLINE_HTML,
        qaProfileId: t.qa_profile_id ?? null,
        // A template that follows a TM profile hands the link to the new
        // project; its tm_assignments are the profile's and are shown read-only.
        tmProfileId: detail.tm_profile?.id ?? null,
        tmProfileName: detail.tm_profile?.name,
        tmProfileUsageCount: detail.tm_profile?.usage_count,
        tmAssignments: (detail.tm_assignments ?? []).map(a => ({
          id: a.id,
          tmId: a.tm_id,
          mode: a.mode,
          priority: a.priority,
          penaltyPct: a.penalty_pct ?? 0,
        })),
        mtRouterId: t.mt_router_id ?? null,
        mtRouterName: t.mt_router_name ?? undefined,
        termAssignments: (detail.term_assignments ?? []).map(a => ({
          id: a.id,
          resourceId: a.term_base_id,
          resourceName: c.termBases?.find(tb => tb.id === a.term_base_id)?.name ?? `Term base #${a.term_base_id}`,
          sourceLang: a.source_language,
          targetLang: a.target_language,
          mode: a.mode,
          priority: a.priority,
        })),
        workflowSteps: workflowStepsFromApi(detail.workflow_steps ?? []),
      })
      c.applyMetadata({ name: t.project_name ?? '', domain: t.domain ?? null, customerId: t.customer_id ?? null })
      setWorkflowId(t.workflow_id ? String(t.workflow_id) : '')
    } catch (err) {
      if (seq !== reqSeq.current) return
      // Leaving the template selected with settings unapplied would be worse
      // than a miss: submit would send template_id (server applies it) and
      // then overwrite with the DEFAULT editor state — a project labeled
      // "Based on X" containing none of X. Reset the picker instead.
      setTemplateId('')
      setTemplateName('')
      toast.error(`Couldn't load template settings: ${errorText(err, String(err))}`)
    }
  }

  const handleFromProjectChange = async (id: string) => {
    const seq = ++reqSeq.current
    setFromProjectId(id)
    setFromProjectName('')
    setTemplateId('')
    setTemplateName('')
    const c = cbRef.current
    if (!id) {
      c.applySettings(c.defaultSettings)
      setWorkflowId('')
      return
    }
    try {
      // Copy what a template would (languages + the settings profile + the
      // four assignment lists) plus the project's name, domain and customer.
      // Status, deadline and files stay per-project.
      const settings = await getProjectSettings(Number(id))
      if (seq !== reqSeq.current) return
      const project = settings.project
      setFromProjectName(project.name)
      // Unlike templates, a project has exact languages — no derivation.
      c.applyLanguages(project.source_language, project.target_languages)
      c.applyMetadata({ name: project.name, domain: project.domain ?? null, customerId: project.customer_id ?? null })
      c.applySettings(projectSettingsToValues(settings, c.termBases))
      setWorkflowId(project.workflow_id ? String(project.workflow_id) : '')
    } catch (err) {
      if (seq !== reqSeq.current) return
      setFromProjectId('')
      toast.error(`Couldn't load project settings: ${errorText(err, String(err))}`)
    }
  }

  // A library workflow only seeds the step list; it is
  // sent as workflow_id at create so the server stamps provenance, and the
  // (possibly tweaked) steps ride along on top. Independent of the template /
  // project pickers — it overrides whatever steps they brought in.
  const handleWorkflowChange = async (id: string, preloaded?: WorkflowDetail) => {
    const seq = ++reqSeq.current
    setWorkflowId(id)
    const c = cbRef.current
    if (!id) {
      c.applySettings({ ...c.currentSettings(), workflowSteps: c.defaultSettings.workflowSteps })
      return
    }
    try {
      const wf = preloaded ?? await getWorkflow(Number(id))
      if (seq !== reqSeq.current) return
      const workflowSteps = workflowDefinitionStepsToDrafts(wf.steps)
      // Pre-translation fills files in with MT, so it switches MT on.
      const mt = isPretranslateOnly(workflowSteps) ? { mtOnUpload: true } : {}
      c.applySettings({ ...c.currentSettings(), workflowSteps, ...mt })
    } catch (err) {
      if (seq !== reqSeq.current) return
      setWorkflowId('')
      toast.error(`Couldn't load workflow: ${errorText(err, String(err))}`)
    }
  }

  // Resuming a saved draft: everything the wizard saved
  // comes back — languages, settings, the four lists, the step draft, the
  // template / workflow provenance — plus the metadata via applyDraft.
  const resumeDraft = async (id: string) => {
    const seq = ++reqSeq.current
    const c = cbRef.current
    try {
      const settings = await getProjectSettings(Number(id))
      if (seq !== reqSeq.current) return
      const project = settings.project
      c.applyLanguages(project.source_language, project.target_languages)
      c.applySettings(projectSettingsToValues(settings, c.termBases))
      setWorkflowId(project.workflow_id ? String(project.workflow_id) : '')
      setTemplateId(project.template_id ? String(project.template_id) : '')
      c.applyDraft?.(project)
    } catch (err) {
      if (seq !== reqSeq.current) return
      toast.error(`Couldn't load the draft: ${errorText(err, String(err))}`)
    }
  }

  // Deep links from the Duplicate / "Use as template" actions and the draft
  // resume: apply once, as soon as the resource queries the prefill depends
  // on have loaded.
  const appliedDeepLink = useRef(false)
  useEffect(() => {
    if (!cb.ready || appliedDeepLink.current) return
    const draft = searchParams.get('draft')
    const fromProject = searchParams.get('fromProject')
    const fromTemplate = searchParams.get('fromTemplate')
    if (!draft && !fromProject && !fromTemplate) return
    appliedDeepLink.current = true
    if (draft) {
      resumeDraft(draft)
    } else if (fromProject) {
      handleFromProjectChange(fromProject)
    } else if (fromTemplate) {
      handleTemplateChange(fromTemplate)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cb.ready])

  // True when ?draft= / ?fromTemplate= / ?fromProject= will prefill the form
  // (now or once `ready`), so nothing else should seed it meanwhile.
  const hasStartFromDeepLink = !!(searchParams.get('draft') || searchParams.get('fromProject') || searchParams.get('fromTemplate'))

  return { templateId, templateName, fromProjectId, fromProjectName, workflowId, hasStartFromDeepLink, handleTemplateChange, handleFromProjectChange, handleWorkflowChange }
}
