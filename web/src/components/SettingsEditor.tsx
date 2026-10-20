import { type ReactNode, useState, useCallback, useEffect, useId, useMemo, useRef } from 'react'
import { DisclosureToggle } from '@/components/ui/disclosure-toggle'
import { toast } from 'sonner'
import { errorText } from '@/lib/error-text'
import { TooltipProvider } from '@/components/ui/tooltip'
import { HelpTip } from '@/components/ui/help-tip'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { RouterEngineChip } from './ai/ai-engine-chips'
import type { UnusableReason } from '../api'
import { Combobox } from '@/components/ui/combobox'
import { useEngineCreateActions } from '../hooks/useEngineCreateActions'
import { AssignmentSection, type AssignmentItem } from './AssignmentSection'
import { TMAssignmentSection, type TMAssignmentItem, type TMResource } from './project/TMAssignmentSection'
import { useInlineCreateResources } from './settings/useInlineCreateResources'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { getTMProfile, createTMProfile, updateTMProfile, setTMProfileTMs, ApiError } from '../api'
import { queryKeys } from '../lib/query-keys'
import { TMScoringFields, describeScoringSave, SCORING_SAVED_TOAST_ID, type TMScoringUpdate } from './settings/TMScoringFields'
import { TMLookupOrderField } from './settings/TMLookupOrderField'
import { toTMLookupOrder, type TMLookupOrder } from '../lib/settings-values'
import { TMProfilePicker } from './settings/TMProfilePicker'
import type { WorkflowStep } from './WorkflowStepsEditor'
import { isPretranslateOnly } from '../lib/workflow-steps'
import { MISSING_MT_ROUTER_WARNING, missingTMWarning, mtRouterCoverageWarning, type RouterCoverage } from '../lib/settings-coverage'
import { SectionInfo } from '@/components/ui/section-info'

// One partial settings update. Keys match the PUT body the API takes.
export type ScalarUpdate = {
  fuzzy_threshold?: number
  sublanguage_penalty_pct?: number
  near_exact_penalty_pct?: number
  tm_lookup_order?: TMLookupOrder
  mt_on_upload?: boolean
  tm_on_upload?: boolean
  auto_complete?: boolean
  inline_html?: boolean
  qa_profile_id?: number | null
  // null = no machine translation for this project.
  mt_router_id?: number | null
}

export interface SettingsValues {
  fuzzyThreshold: number
  sublanguagePenaltyPct: number
  nearExactPenaltyPct: number
  tmLookupOrder: TMLookupOrder
  mtOnUpload: boolean
  tmOnUpload: boolean
  autoComplete: boolean
  inlineHTML: boolean
  qaProfileId: number | null
  // The TM profile the row follows (null/absent = its own TM list + scoring).
  // While set, tmAssignments and the scoring trio are the profile's values.
  tmProfileId?: number | null
  tmProfileName?: string
  // Followers of that profile, this row included. Undefined = unknown.
  tmProfileUsageCount?: number
  tmAssignments: TMAssignmentItem[]
  // The MT router the profile points at; null = no machine translation.
  mtRouterId: number | null
  mtRouterName?: string
  termAssignments: AssignmentItem[]
  workflowSteps: WorkflowStep[]
}

interface SettingsEditorProps {
  values: SettingsValues
  tmCollections: TMResource[]
  // The MT routers the project can pick from (the MT page owns them).
  /** `unusable` warns that a route inside the router points at a broken engine;
   *  the routes and default engine show which target languages it covers. */
  mtRouters: ({ id: number; name: string; unusable?: UnusableReason } & RouterCoverage)[]
  termBases: { id: number; name: string; source_language: string; target_languages: string[] }[]
  qaProfiles: { id: number; name: string }[]
  tmProfiles?: { id: number; name: string }[]
  // Only changes wording in the TM profile picker ("this project" / "this template").
  entity?: 'project' | 'template'
  languages?: { code: string; name: string }[]
  sourceLanguage?: string
  targetLanguages?: string[]
  /** Target languages the chosen MT router is checked against, when there
   *  are no targetLanguages: the template page passes its default targets
   *  here, without the project-language behaviour targetLanguages brings. */
  routerCoverageLanguages?: string[]
  mode: 'edit' | 'create'
  onUpdateScalars?: (data: ScalarUpdate) => Promise<void>
  onSaveTMAssignments?: (items: TMAssignmentItem[]) => Promise<void>
  // TM profile link management (edit mode). Linking in create mode is a draft.
  onLinkTMProfile?: (id: number) => Promise<void>
  onDetachTMProfile?: () => Promise<void>
  onSaveTMProfile?: (name: string, description: string, link: boolean) => Promise<void>
  onSaveTermAssignments?: (items: AssignmentItem[]) => Promise<void>
  onChange?: (values: SettingsValues) => void
  /** The entity's own fields (name, languages, customer…), rendered at the
   *  top of the Project settings card: the New project form's inputs, or the
   *  template page's Save form. */
  projectFields?: ReactNode
  /** Rendered between the Project settings card and the resource cards (the
   *  New project page puts its Workflow card here). A function gets the live
   *  values the editor holds before they are saved. */
  afterProjectSettings?: ReactNode | ((live: { mtRouterName?: string; mtOnUpload: boolean }) => ReactNode)
}


export function SettingsEditor({
  values,
  tmCollections,
  mtRouters,
  termBases,
  qaProfiles,
  tmProfiles = [],
  entity = 'project',
  languages,
  sourceLanguage,
  targetLanguages,
  routerCoverageLanguages,
  mode,
  onUpdateScalars,
  onSaveTMAssignments,
  onLinkTMProfile,
  onDetachTMProfile,
  onSaveTMProfile,
  onSaveTermAssignments,
  onChange,
  projectFields,
  afterProjectSettings,
}: SettingsEditorProps) {
  const isCreate = mode === 'create'
  const [saving, setSaving] = useState(false)
  const queryClient = useQueryClient()

  // The MT router picker's way out of an empty list: routers are created on
  // the MT page, in a new tab.
  const { mtRouter } = useEngineCreateActions()
  const mtRouterActions = useMemo(() => [mtRouter()], [mtRouter])

  const [localPreTranslate, setLocalPreTranslate] = useState(values.mtOnUpload)
  const [localTmOnUpload, setLocalTmOnUpload] = useState(values.tmOnUpload)
  const [localAutoComplete, setLocalAutoComplete] = useState(values.autoComplete)
  const [localInlineHTML, setLocalInlineHTML] = useState(values.inlineHTML)
  const [localTmLookupOrder, setLocalTmLookupOrder] = useState(values.tmLookupOrder)
  // The wizard's own TM rows live in TMAssignmentSection and reach us only
  // through onDraftChange; `values` is not re-fed, so count them here.
  const [localTMRowCount, setLocalTMRowCount] = useState(values.tmAssignments.length)
  const [localQaProfileId, setLocalQaProfileId] = useState(values.qaProfileId)
  const [localMTRouterId, setLocalMTRouterId] = useState<number | null>(values.mtRouterId)
  // Create mode keeps the TM profile choice as a draft too; edit mode reads it
  // from the server payload.
  const [localTMProfileId, setLocalTMProfileId] = useState<number | null>(values.tmProfileId ?? null)
  const tmProfileId = isCreate ? localTMProfileId : (values.tmProfileId ?? null)
  const tmLinked = tmProfileId != null
  // In create mode nothing is persisted yet, so the chosen profile's rows and
  // scoring are fetched for the read-only preview. Edit mode gets them from
  // the settings payload (the server returns the effective values).
  const { data: linkedProfile } = useQuery({
    queryKey: queryKeys.tmProfile.detail(tmProfileId ?? 0),
    queryFn: () => getTMProfile(tmProfileId!),
    enabled: isCreate && tmLinked,
  })
  const tmRows: TMAssignmentItem[] = isCreate && tmLinked
    ? (linkedProfile?.tm_assignments ?? []).map(a => ({ id: a.id, tmId: a.tm_id, mode: a.mode, priority: a.priority, penaltyPct: a.penalty_pct ?? 0 }))
    : values.tmAssignments
  // A new project starts with the TM source unanswered: no profile, no
  // memories of its own, no pick yet. Until it is answered the list + scoring
  // stay hidden and the on-upload switch has nothing to act on. A template
  // prefill answers it through the rows it brings. A saved project or template
  // always shows what it has: there, no profile IS the answer.
  const [ownPicked, setOwnPicked] = useState(false)
  const tmChosen = !isCreate || tmLinked || ownPicked || tmRows.length > 0
  const scoringValues = isCreate && tmLinked && linkedProfile
    ? { fuzzyThreshold: linkedProfile.fuzzy_threshold, sublanguagePenaltyPct: linkedProfile.sublanguage_penalty_pct, nearExactPenaltyPct: linkedProfile.near_exact_penalty_pct }
    : { fuzzyThreshold: values.fuzzyThreshold, sublanguagePenaltyPct: values.sublanguagePenaltyPct, nearExactPenaltyPct: values.nearExactPenaltyPct }
  const lookupOrder = isCreate && tmLinked && linkedProfile
    ? toTMLookupOrder(linkedProfile.tm_lookup_order)
    : isCreate ? localTmLookupOrder : values.tmLookupOrder
  const lookupOrderTMCount = isCreate && !tmLinked ? localTMRowCount : tmRows.length
  // A followed profile can be edited right here while nobody else follows it:
  // the edit reaches only this row, so nothing surprising happens elsewhere.
  // Followers other than this row: in create mode the project doesn't exist
  // yet, so the profile's count is all "others"; in edit mode subtract this
  // row. Unknown counts read as shared (read-only) — the safe side.
  const tmProfileOthers = !tmLinked ? 0
    : isCreate ? (linkedProfile?.usage_count ?? Infinity)
    : values.tmProfileUsageCount != null ? Math.max(0, values.tmProfileUsageCount - 1) : Infinity
  const tmProfileEditable = tmLinked && tmProfileOthers === 0
  // A followed profile's settings are collapsed by default — the picker row
  // already says which profile applies — and expand on demand. Re-collapse
  // when the followed profile changes; saving the draft AS a profile opens it,
  // since that is the setup the user was just editing.
  const [profileSettingsOpen, setProfileSettingsOpen] = useState(false)
  // Set before a save-as links the new profile: the profile-change effect then
  // opens instead of collapsing (it runs after the link lands, in both modes).
  const openOnNextProfileRef = useRef(false)
  useEffect(() => {
    if (openOnNextProfileRef.current && tmProfileId != null) {
      openOnNextProfileRef.current = false
      setProfileSettingsOpen(true)
    } else {
      setProfileSettingsOpen(false)
    }
  }, [tmProfileId])
  const afterProfileEdit = () => {
    if (tmProfileId != null) queryClient.invalidateQueries({ queryKey: queryKeys.tmProfile.detail(tmProfileId) })
    queryClient.invalidateQueries({ queryKey: queryKeys.tmProfiles.all })
    queryClient.invalidateQueries({ queryKey: queryKeys.project.allSettings })
    queryClient.invalidateQueries({ queryKey: queryKeys.template.all })
    queryClient.invalidateQueries({ queryKey: queryKeys.tmMatches.all })
  }
  // The edit is conditional on nobody else following (0 in the wizard, where
  // this project does not exist yet; 1 = this row alone on a saved page). If
  // someone started following since the page loaded, the server refuses with
  // 409 tm_profile_shared: refetch (the block turns read-only) and say why.
  const profileEditOpts = { maxFollowers: isCreate ? 0 : 1 }
  const explainSharedProfile = (err: unknown): boolean => {
    if (err instanceof ApiError && err.status === 409) {
      afterProfileEdit()
      toast.error('Another project or template now follows this TM profile, so it is read-only here. Edit it on its own page, or detach.')
      return true
    }
    return false
  }
  const saveProfileScoring = async (u: TMScoringUpdate) => {
    if (tmProfileId == null) return false
    setSaving(true)
    try {
      await updateTMProfile(tmProfileId, u, profileEditOpts)
      afterProfileEdit()
      toast.success(`${describeScalarSave(u)} on the TM profile`, { id: SCORING_SAVED_TOAST_ID })
      return true
    } catch (err) {
      if (!explainSharedProfile(err)) toast.error(`Failed: ${errorText(err, String(err))}`)
      return false
    } finally {
      setSaving(false)
    }
  }
  const saveProfileTMs = async (items: TMAssignmentItem[]) => {
    if (tmProfileId == null) return
    try {
      await setTMProfileTMs(tmProfileId, items.map(i => ({ tm_id: i.tmId, mode: i.mode, priority: i.priority, penalty_pct: i.penaltyPct ?? 0 })), profileEditOpts)
    } catch (err) {
      if (explainSharedProfile(err)) return
      throw err
    }
    afterProfileEdit()
  }

  // Sync other locals when values change externally (template pre-fill) — create mode only
  const prevValuesRef = useRef(values)
  useEffect(() => {
    if (isCreate && values !== prevValuesRef.current) {
      prevValuesRef.current = values
      setLocalPreTranslate(values.mtOnUpload)
      setLocalTmOnUpload(values.tmOnUpload)
      setLocalAutoComplete(values.autoComplete)
      setLocalInlineHTML(values.inlineHTML)
      setLocalTmLookupOrder(values.tmLookupOrder)
      setLocalTMRowCount(values.tmAssignments.length)
      setLocalQaProfileId(values.qaProfileId)
      setLocalMTRouterId(values.mtRouterId)
      setLocalTMProfileId(values.tmProfileId ?? null)
    }
  }, [values, isCreate])

  // Track the latest draft so each onChange call doesn't clobber changes
  // from other sections (e.g., adding TM then toggling pre-translate).
  // Without this, each callback would spread the stale `values` prop.
  const draftRef = useRef(values)
  useEffect(() => { draftRef.current = values }, [values])

  // "+ Create new…" inside the TM / term-base pickers: promise-based dialogs
  // owned by useInlineCreateResources; `inlineCreateDialogs` renders them.
  const { handleCreateNewTM, handleCreateNewTermBase, dialogs: inlineCreateDialogs } = useInlineCreateResources({ sourceLanguage, targetLanguages })

  const describeScalarSave = (data: ScalarUpdate): string => {
    const scoring = describeScoringSave(data)
    if (scoring) return scoring
    if (data.mt_on_upload !== undefined) {
      return data.mt_on_upload ? 'MT on upload enabled' : 'MT on upload disabled'
    }
    if (data.tm_on_upload !== undefined) {
      return data.tm_on_upload ? 'Apply TM on upload enabled' : 'Apply TM on upload disabled'
    }
    if (data.auto_complete !== undefined) {
      return data.auto_complete ? 'Auto-complete enabled' : 'Auto-complete disabled'
    }
    if (data.inline_html !== undefined) {
      return data.inline_html ? 'HTML strings enabled' : 'HTML strings disabled'
    }
    if ('qa_profile_id' in data) {
      if (data.qa_profile_id === null) return 'QA profile cleared'
      return `QA profile set to ${qaProfiles.find(p => p.id === data.qa_profile_id)!.name}`
    }
    if ('mt_router_id' in data) {
      if (data.mt_router_id === null) return 'MT router cleared'
      return `MT router set to ${mtRouters.find(r => r.id === data.mt_router_id)?.name ?? data.mt_router_id}`
    }
    return 'Settings saved'
  }

  const handleScalarChange = async (data: ScalarUpdate) => {
    if (isCreate && onChange) {
      // Use local state (not stale values prop) to preserve current settings
      const newFuzzy = data.fuzzy_threshold ?? draftRef.current.fuzzyThreshold
      const newSublanguage = data.sublanguage_penalty_pct ?? draftRef.current.sublanguagePenaltyPct
      const newNearExact = data.near_exact_penalty_pct ?? draftRef.current.nearExactPenaltyPct
      const newLookupOrder = data.tm_lookup_order ?? draftRef.current.tmLookupOrder
      const newPreTranslate = data.mt_on_upload ?? localPreTranslate
      const newTmOnUpload = data.tm_on_upload ?? localTmOnUpload
      const newAutoComplete = data.auto_complete ?? localAutoComplete
      const newInlineHTML = data.inline_html ?? localInlineHTML
      const newQa = data.qa_profile_id !== undefined ? data.qa_profile_id : localQaProfileId
      const newMTRouter = data.mt_router_id !== undefined ? data.mt_router_id : localMTRouterId
      setLocalPreTranslate(newPreTranslate)
      setLocalTmOnUpload(newTmOnUpload)
      setLocalAutoComplete(newAutoComplete)
      setLocalInlineHTML(newInlineHTML)
      setLocalTmLookupOrder(newLookupOrder)
      setLocalQaProfileId(newQa)
      setLocalMTRouterId(newMTRouter)
      // Use draftRef to preserve assignments added via other callbacks
      const next = {
        ...draftRef.current,
        fuzzyThreshold: newFuzzy,
        sublanguagePenaltyPct: newSublanguage,
        nearExactPenaltyPct: newNearExact,
        tmLookupOrder: newLookupOrder,
        mtOnUpload: newPreTranslate,
        tmOnUpload: newTmOnUpload,
        autoComplete: newAutoComplete,
        inlineHTML: newInlineHTML,
        qaProfileId: newQa,
        mtRouterId: newMTRouter,
      }
      draftRef.current = next
      onChange(next)
    } else if (onUpdateScalars) {
      setSaving(true)
      try {
        await onUpdateScalars(data)
        // Scoring fields save while typing: one toast that each edit replaces.
        toast.success(describeScalarSave(data), describeScoringSave(data) ? { id: SCORING_SAVED_TOAST_ID } : undefined)
        return true
      } catch (err) {
        toast.error(`Failed: ${errorText(err, String(err))}`)
        return false
      } finally {
        setSaving(false)
      }
    }
  }

  const displayPreTranslate = isCreate ? localPreTranslate : values.mtOnUpload
  const displayTmOnUpload = isCreate ? localTmOnUpload : values.tmOnUpload
  const displayAutoComplete = isCreate ? localAutoComplete : values.autoComplete
  const displayInlineHTML = isCreate ? localInlineHTML : values.inlineHTML
  const displayQaProfileId = isCreate ? localQaProfileId : values.qaProfileId
  const displayMTRouterId = isCreate ? localMTRouterId : values.mtRouterId
  // Same gate as tmChosen, for the MT switch: on a new project there is
  // nothing to translate with until a router is picked.
  const mtChosen = !isCreate || displayMTRouterId != null
  // Raw MT's whole job is machine translation on upload, so the switch stays
  // on (the server holds the same rule).
  const mtLocked = isPretranslateOnly(values.workflowSteps)

  // Setup gaps, flagged beside the card they belong to. Not on the New project
  // form, where the TM and MT sources start unanswered. The locked MT switch
  // already asks for a router in red, so the MT title stays quiet then.
  const showGaps = !isCreate && !!sourceLanguage && !!targetLanguages?.length
  const tmWarning = showGaps
    ? missingTMWarning({
        sourceLanguage: sourceLanguage!,
        targetLanguages: targetLanguages!,
        tmTargetLanguages: tmRows.flatMap(a => tmCollections.find(t => t.id === a.tmId)?.target_languages ?? []),
      })
    : null
  const mtWarning = showGaps && !mtLocked && values.mtRouterId == null ? MISSING_MT_ROUTER_WARNING : null
  // Unlike the gaps above, this one shows on the New project form too: the
  // router is picked there, before any file is machine translated.
  const selectedRouter = mtRouters.find(r => r.id === displayMTRouterId)
  const routerCoverageWarning = selectedRouter
    ? mtRouterCoverageWarning(selectedRouter, targetLanguages ?? routerCoverageLanguages ?? [])
    : null

  const handleLinkTMProfile = async (id: number) => {
    if (isCreate) {
      setLocalTMProfileId(id)
      // A linked project takes its TMs from the profile; the draft rows would
      // be rejected alongside tm_profile_id at create time.
      const next = { ...draftRef.current, tmProfileId: id, tmAssignments: [] }
      draftRef.current = next
      onChange?.(next)
      return
    }
    if (!onLinkTMProfile) return
    setSaving(true)
    try {
      await onLinkTMProfile(id)
      toast.success('Following TM profile')
    } catch (err) {
      toast.error(`Failed: ${errorText(err, String(err))}`)
    } finally {
      setSaving(false)
    }
  }
  const handleClearDraftTMProfile = () => {
    setLocalTMProfileId(null)
    setOwnPicked(true)
    const next = { ...draftRef.current, tmProfileId: null }
    draftRef.current = next
    onChange?.(next)
  }
  const handleDetachTMProfile = async () => {
    if (!onDetachTMProfile) return
    setSaving(true)
    try {
      await onDetachTMProfile()
      setOwnPicked(true)
      toast.success('Detached — the profile\'s TMs and match scoring were copied into this project')
    } catch (err) {
      toast.error(`Failed: ${errorText(err, String(err))}`)
    } finally {
      setSaving(false)
    }
  }
  // The dialog reports failures itself; success is a toast here. In create mode
  // nothing is on the server yet, so the profile is built straight from the
  // draft rows + scoring (the API takes them on create) and the draft then
  // follows it — the same "create it now" shape as inline "Create new TM".
  const handleSaveTMProfile = async (name: string, description: string, link: boolean) => {
    if (isCreate) {
      const d = draftRef.current
      const created = await createTMProfile({
        name, description,
        fuzzy_threshold: d.fuzzyThreshold,
        sublanguage_penalty_pct: d.sublanguagePenaltyPct,
        near_exact_penalty_pct: d.nearExactPenaltyPct,
        tm_lookup_order: d.tmLookupOrder,
        tms: d.tmAssignments.map(i => ({ tm_id: i.tmId, mode: i.mode, priority: i.priority, penalty_pct: i.penaltyPct ?? 0 })),
      })
      queryClient.invalidateQueries({ queryKey: queryKeys.tmProfiles.all })
      if (link) {
        openOnNextProfileRef.current = true
        await handleLinkTMProfile(created.id)
      }
      toast.success(link ? `TM profile "${name}" created — this project will follow it` : `TM profile "${name}" created`)
      return
    }
    if (!onSaveTMProfile) return
    openOnNextProfileRef.current = link
    await onSaveTMProfile(name, description, link)
    toast.success(link ? `TM profile "${name}" created — this project now follows it` : `TM profile "${name}" created`)
  }

  const handleTMDraftChange = useCallback((items: TMAssignmentItem[]) => {
    if (!onChange) return
    setLocalTMRowCount(items.length)
    const next = { ...draftRef.current, tmAssignments: items }
    draftRef.current = next
    onChange(next)
  }, [onChange])

  const handleTermDraftChange = useCallback((items: AssignmentItem[]) => {
    if (!onChange) return
    const next = { ...draftRef.current, termAssignments: items }
    draftRef.current = next
    onChange(next)
  }, [onChange])

  // Only shown while a profile is followed; summarises the collapsed block.
  const tmSettingsId = useId()
  const profileSettingsToggle = (
    <DisclosureToggle
      open={profileSettingsOpen}
      onToggle={() => setProfileSettingsOpen(o => !o)}
      controls={tmSettingsId}
      summary={<>{' '}— {tmRows.length} {tmRows.length === 1 ? 'TM' : 'TMs'} · {Math.round(scoringValues.fuzzyThreshold * 100)}% threshold</>}
      data-testid="tm-profile-settings-toggle"
    >
      {profileSettingsOpen ? 'Hide' : 'Show'} profile settings
    </DisclosureToggle>
  )
  // Scoring + TM rows. Rendered under the TM profile dropdown (behind the
  // toggle, collapsed by default) while a profile is followed, and straight
  // below the dropdown otherwise. While the profile is editable in place, edits
  // go to the PROFILE (its endpoints), not the row — and they commit on blur
  // even in the wizard, because the profile already exists on the server.
  const tmSettingsBlock = (
  <div id={tmSettingsId} className="space-y-4 rounded-lg border border-gray-200 dark:border-gray-800 p-3" data-testid="tm-profile-settings">
  {/* Not locked while saving: the fields save while they keep focus, and a
      disabled input drops it. */}
  <TMScoringFields
    values={scoringValues}
    isCreate={isCreate && !tmProfileEditable}
    readOnly={tmLinked && !tmProfileEditable}
    onCommit={tmProfileEditable ? saveProfileScoring : handleScalarChange}
  />
  <TMAssignmentSection
    variant="embedded"
    readOnly={tmLinked && !tmProfileEditable}
    items={tmRows}
    tmCollections={tmCollections}
    projectSourceLanguage={sourceLanguage}
    projectTargetLanguages={targetLanguages}
    initialEditing={isCreate && !tmProfileEditable}
    onDraftChange={isCreate && !tmProfileEditable ? handleTMDraftChange : undefined}
    onSave={tmProfileEditable ? saveProfileTMs : (!isCreate ? onSaveTMAssignments : undefined)}
    onCreateNewResource={handleCreateNewTM}
    createNewLabel="Create new TM..."
    footer={
      <TMLookupOrderField
        value={lookupOrder}
        tmCount={lookupOrderTMCount}
        disabled={saving}
        readOnly={tmLinked && !tmProfileEditable}
        onCommit={tmProfileEditable ? saveProfileScoring : handleScalarChange}
      />
    }
  />
  </div>
  )

  return (
    <TooltipProvider delay={100}>
    <div className="space-y-6">
      {/* Scalar Settings. The New project page drops the heading: the page
          title already says what the form is, and the card opens with the
          project's own fields. */}
      <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-4">
        {!isCreate && (
          <h3 className="text-sm font-medium text-gray-700 dark:text-gray-300 mb-4">Project settings</h3>
        )}

        {projectFields && (
          <div className="space-y-4 mb-4 pb-4 border-b border-gray-200 dark:border-gray-800">
            {projectFields}
          </div>
        )}

        <CheckboxField
          className="mb-4"
          checked={displayAutoComplete}
          onChange={checked => handleScalarChange({ auto_complete: checked })}
          disabled={saving}
          data-testid="settings-auto-complete"
          label={<>
            <strong>Complete</strong> the project when every task is done
            <HelpTip text="When enabled, the project's status becomes Completed once every one of its tasks is completed, and returns to In progress if that stops being true." />
          </>}
        />

        <CheckboxField
          className="mb-4"
          checked={displayInlineHTML}
          onChange={checked => handleScalarChange({ inline_html: checked })}
          disabled={saving}
          data-testid="settings-inline-html"
          label="Strings contain HTML"
          description="Tags in JSON, PO, TXT and Excel strings become protected tags. Turn off for plain text. Applies to new uploads."
        />

        {/* The "apply TM / fill in MT on start or upload" switches and the
            match scoring live in the Translation memory and Machine
            translation cards below, next to what they control, and the QA
            profile has its own card at the end. QP+APE is not an
            upload phase — it runs as a QE+APE workflow step. */}
      </div>

      {typeof afterProjectSettings === 'function'
        ? afterProjectSettings({ mtRouterName: selectedRouter?.name, mtOnUpload: displayPreTranslate })
        : afterProjectSettings}


      {/* Translation memory, top to bottom: where the TM list + match scoring
          come from (a shared profile, or this row's own), the scoring, then
          the TM rows (per-TM; languages come from the TM itself).
          One card so the three read as one subject; while a profile is
          followed the scoring and rows are its values, shown read-only. */}
      <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-4 space-y-4" data-testid="tm-section">
        <h3 className="flex items-center gap-1.5 text-sm font-medium text-gray-700 dark:text-gray-300">
          Translation memory
          {tmWarning && <SectionInfo tone="amber" label="Missing translation memory" data-testid="tm-coverage-warning">{tmWarning}</SectionInfo>}
        </h3>
        {/* Independent of the MT switch; all four combinations are valid. */}
        {/* Locked until the TM source is chosen: with no memories behind it the
            switch would promise a lookup that cannot run. */}
        <CheckboxField
          checked={displayTmOnUpload}
          onChange={checked => handleScalarChange({ tm_on_upload: checked })}
          disabled={saving || !tmChosen}
          data-testid="settings-tm-on-upload"
          label={<>
            Apply <strong>translation memory</strong> on start or upload
            <HelpTip text="When enabled, translation memory lookup runs automatically when a project starts or files are uploaded. Matches are filled before machine translation. Choose where the translation memories come from to turn this off." />
          </>}
        />
        <TMProfilePicker
          profiles={tmProfiles}
          value={tmChosen ? tmProfileId : undefined}
          entity={entity}
          disabled={saving}
          isCreate={isCreate}
          onLink={handleLinkTMProfile}
          onDetach={!isCreate && onDetachTMProfile ? handleDetachTMProfile : undefined}
          onClearDraft={isCreate ? handleClearDraftTMProfile : undefined}
          onChooseOwn={() => setOwnPicked(true)}
          onSaveAsProfile={isCreate || onSaveTMProfile ? handleSaveTMProfile : undefined}
          editableInPlace={tmProfileEditable}
          profileSettingsToggle={tmLinked ? profileSettingsToggle : undefined}
          profileSettingsBody={tmLinked && profileSettingsOpen ? tmSettingsBlock : undefined}
        />
        {!tmLinked && tmChosen && tmSettingsBlock}
      </div>

      {/* Machine translation: the on-upload switch, then which MT router this
          project follows. The router (MT page) decides the engine per target
          language; nothing per-language is configured here. */}
      <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-4 space-y-4" data-testid="mt-section">
        <h3 className="flex items-center gap-1.5 text-sm font-medium text-gray-700 dark:text-gray-300">
          Machine translation
          {mtWarning && <SectionInfo tone="amber" label="Missing MT router" data-testid="mt-coverage-warning">{mtWarning}</SectionInfo>}
        </h3>
        {/* Same gate as the TM switch: no router, nothing to translate with. */}
        {/* The error stays outside the checkbox row, which is dimmed while
            locked; the shared wrapper keeps it right under the row. */}
        <div>
          <CheckboxField
            checked={mtLocked || displayPreTranslate}
            onChange={checked => handleScalarChange({ mt_on_upload: checked })}
            disabled={saving || !mtChosen || mtLocked}
            data-testid="settings-mt-on-upload"
            label={<>
              Fill in <strong>machine translation</strong> on start or upload
              <HelpTip text="When enabled, machine translation runs automatically when a project starts or files are uploaded to fill untranslated segments. Choose an MT router to turn this off." />
            </>}
            description={mtLocked && <span data-testid="settings-mt-locked">Always on with Raw MT.</span>}
          />
          {mtLocked && displayMTRouterId == null && (
            <p className="text-xs text-red-500 dark:text-red-400" data-testid="settings-mt-router-required">
              Choose an MT router: the project cannot start without one.
            </p>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <span className="flex items-center gap-1 text-sm font-medium text-gray-700 dark:text-gray-300">
            MT router
            <HelpTip text="The router picks the MT engine for each job by its target language. Routers are set up on the MT page." />
          </span>
          <Combobox
            value={displayMTRouterId != null ? String(displayMTRouterId) : ''}
            onChange={v => handleScalarChange({ mt_router_id: v === '' ? null : Number(v) })}
            placeholder="No machine translation"
            clearable
            options={[
              // A router this profile follows but the list no longer carries
              // (retired) stays selectable so the field never reads as empty.
              ...(displayMTRouterId != null && !mtRouters.some(r => r.id === displayMTRouterId)
                ? [{ value: String(displayMTRouterId), label: values.mtRouterName ?? `Router #${displayMTRouterId}` }]
                : []),
              // The warning travels with the router, so you can see a broken
              // one before assigning it rather than after a job fails.
              ...mtRouters.map(r => ({
                value: String(r.id),
                label: r.name,
                chip: r.unusable ? <RouterEngineChip reason={r.unusable} /> : undefined,
              })),
            ]}
            actions={mtRouterActions}
            className="w-[28rem] max-w-full"
            disabled={saving}
            data-testid="mt-router-select"
          />
          {routerCoverageWarning && (
            <SectionInfo tone="amber" label="Languages without machine translation" data-testid="mt-router-coverage-warning">
              {routerCoverageWarning}
            </SectionInfo>
          )}
        </div>
      </div>

      {/* Terminology Assignments */}
      <AssignmentSection
        title="Terminology"
        helpText="Assign terminology (term bases) to language pairs. Terms from assigned term bases are highlighted in the CAT editor and used for QA terminology checks."
        items={values.termAssignments}
        resources={termBases}
        resourceLabel="Term base"
        hasMode
        hasPriority
        languages={languages}
        autoSourceLanguage={sourceLanguage}
        restrictTargetsTo={targetLanguages}
        resourceLangs={Object.fromEntries(termBases.map(t => [t.id, { source_language: t.source_language, target_languages: t.target_languages }]))}
        initialEditing={isCreate}
        onDraftChange={isCreate ? handleTermDraftChange : undefined}
        onSave={!isCreate ? onSaveTermAssignments : undefined}
        onCreateNewResource={handleCreateNewTermBase}
        createNewLabel="Create new term base..."
      />

      {/* Quality assurance: which QA profile's checks run on this project. Its
          own card, like the other resources — terminology above feeds its
          terminology check. */}
      <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-4 space-y-4" data-testid="qa-section">
        <h3 className="text-sm font-medium text-gray-700 dark:text-gray-300">Quality assurance</h3>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <span className="flex items-center gap-1 text-sm font-medium text-gray-700 dark:text-gray-300">
            QA profile
            <HelpTip text="Select a QA profile to enable quality checks (tag verification, number consistency, terminology compliance, etc.) for this project. Create profiles in the QA profiles page." />
          </span>
          <Combobox
            value={String(displayQaProfileId ?? '')}
            onChange={v => handleScalarChange({ qa_profile_id: v ? Number(v) : null })}
            options={[
              { value: '', label: 'None' },
              ...qaProfiles.map(p => ({ value: String(p.id), label: p.name })),
            ]}
            className="w-64"
            disabled={saving}
            data-testid="qa-profile-select"
          />
        </div>
      </div>

      {inlineCreateDialogs}
    </div>
    </TooltipProvider>
  )
}
