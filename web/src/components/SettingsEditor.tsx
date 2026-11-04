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
import { LINGUIST_TERMS_LABELS, toTMLookupOrder, type LinguistTerms, type TMLookupOrder } from '../lib/settings-values'
import { LinguistTermsField } from './settings/LinguistTermsField'
import { modeHasWrite } from '../lib/tm-mode'
import { TMProfilePicker } from './settings/TMProfilePicker'
import type { WorkflowStep } from './WorkflowStepsEditor'
import { isPretranslateOnly } from '../lib/workflow-steps'
import { MISSING_MT_ROUTER_WARNING, missingTMWarning, mtRouterCoverageWarning, type RouterCoverage } from '../lib/settings-coverage'
import { SectionInfo } from '@/components/ui/section-info'
import { FileConfigurationsSection } from './settings/FileConfigurationsSection'
import { useSegmentationProfiles } from '../hooks/useSegmentationProfiles'
import { useFilterProfiles } from '../hooks/useFilterProfiles'
import { FILTER_FORMAT_LABELS, isFilterFormat, type FilterFormat } from '../lib/filter-formats'
import { OpenLinkButton } from '@/components/ui/open-link-button'
import { mtRouterPath, qaProfilePath, termBasePath } from '@/lib/asset-links'
import { conceptsLabel, tmEntriesLabel } from '@/lib/tm-display'
import { describeSwitchSave, mergeSwitches, pickSwitches } from '@/lib/settings-switches'

// One partial settings update. Keys match the PUT body the API takes.
export type ScalarUpdate = {
  fuzzy_threshold?: number
  sublanguage_penalty_pct?: number
  near_exact_penalty_pct?: number
  tm_lookup_order?: TMLookupOrder
  mt_on_upload?: boolean
  tm_on_upload?: boolean
  auto_complete?: boolean
  // Per format; null goes back to the format's Default, a format left out
  // keeps its pick.
  filter_profile_ids?: Partial<Record<FilterFormat, number | null>>
  linguist_terms?: LinguistTerms
  qa_profile_id?: number | null
  segmentation_profile_id?: number
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
  // The filter profile per file format; a format left out uses its Default.
  filterProfileIds: Partial<Record<FilterFormat, number>>
  linguistTerms: LinguistTerms
  qaProfileId: number | null
  // The segmentation profile new uploads are split with. null: on a new
  // project, the suggested one; on a saved project or template, none is
  // picked (its profile was deleted).
  segmentationProfileId: number | null
  segmentationProfileName?: string
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
  termBases: { id: number; name: string; source_language: string; target_languages: string[]; entry_count?: number }[]
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
  /** The source language the segmentation profile must suit, when there is
   *  no sourceLanguage: the template page passes its own here, without the
   *  project-language behaviour sourceLanguage brings. */
  segmentationLanguage?: string
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
  segmentationLanguage,
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
  // Term bases carry their size as the picker chip and the row's second line.
  const termBaseResources = useMemo(
    () => termBases.map(t => ({ ...t, meta: t.entry_count != null ? conceptsLabel(t.entry_count) : undefined })),
    [termBases],
  )

  const [localSwitches, setLocalSwitches] = useState(() => pickSwitches(values))
  const [localFilterProfileIds, setLocalFilterProfileIds] = useState(values.filterProfileIds)
  const filterProfiles = useFilterProfiles()
  const [localTmLookupOrder, setLocalTmLookupOrder] = useState(values.tmLookupOrder)
  const [localLinguistTerms, setLocalLinguistTerms] = useState(values.linguistTerms)
  // The wizard's own TM rows live in TMAssignmentSection and reach us only
  // through onDraftChange; `values` is not re-fed, so count them here.
  const [localTMRowCount, setLocalTMRowCount] = useState(values.tmAssignments.length)
  const [localQaProfileId, setLocalQaProfileId] = useState(values.qaProfileId)
  const [localSegmentationProfileId, setLocalSegmentationProfileId] = useState(values.segmentationProfileId)
  const segmentation = useSegmentationProfiles(sourceLanguage ?? segmentationLanguage)
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
  // Entries across the rows' TMs, for the collapsed summary; null while any count is unknown.
  const tmEntryTotal = tmRows.reduce<number | null>((sum, a) => {
    const n = tmCollections.find(t => t.id === a.tmId)?.entry_count
    return sum == null || n == null ? null : sum + n
  }, 0)
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
      setLocalSwitches(pickSwitches(values))
      setLocalFilterProfileIds(values.filterProfileIds)
      setLocalTmLookupOrder(values.tmLookupOrder)
      setLocalLinguistTerms(values.linguistTerms)
      setLocalTMRowCount(values.tmAssignments.length)
      setLocalQaProfileId(values.qaProfileId)
      setLocalSegmentationProfileId(values.segmentationProfileId)
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
    const toggled = describeSwitchSave(data)
    if (toggled) return toggled
    if (data.linguist_terms !== undefined) return `Linguists can add terms set to “${LINGUIST_TERMS_LABELS[data.linguist_terms]}”`
    if (data.filter_profile_ids !== undefined) {
      const [format, id] = Object.entries(data.filter_profile_ids)[0] ?? []
      if (format && isFilterFormat(format)) {
        const name = id == null ? 'Default' : (filterProfiles.profiles.find(p => p.id === id)?.name ?? String(id))
        return `${FILTER_FORMAT_LABELS[format]} filter profile set to ${name}`
      }
      return 'Filter profiles saved'
    }
    if ('qa_profile_id' in data) {
      if (data.qa_profile_id === null) return 'QA profile cleared'
      return `QA profile set to ${qaProfiles.find(p => p.id === data.qa_profile_id)!.name}`
    }
    if (data.segmentation_profile_id !== undefined) {
      return `Segmentation profile set to ${segmentation.profiles.find(p => p.id === data.segmentation_profile_id)?.name ?? data.segmentation_profile_id}`
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
      const newLinguistTerms = data.linguist_terms ?? localLinguistTerms
      const newSwitches = mergeSwitches(localSwitches, data)
      const newFilterProfileIds = data.filter_profile_ids ? applyFilterPicks(localFilterProfileIds, data.filter_profile_ids) : localFilterProfileIds
      const newQa = data.qa_profile_id !== undefined ? data.qa_profile_id : localQaProfileId
      const newSegmentation = data.segmentation_profile_id ?? localSegmentationProfileId
      const newMTRouter = data.mt_router_id !== undefined ? data.mt_router_id : localMTRouterId
      setLocalSwitches(newSwitches)
      setLocalFilterProfileIds(newFilterProfileIds)
      setLocalTmLookupOrder(newLookupOrder)
      setLocalLinguistTerms(newLinguistTerms)
      setLocalQaProfileId(newQa)
      setLocalSegmentationProfileId(newSegmentation)
      setLocalMTRouterId(newMTRouter)
      // Use draftRef to preserve assignments added via other callbacks
      const next = {
        ...draftRef.current,
        fuzzyThreshold: newFuzzy,
        sublanguagePenaltyPct: newSublanguage,
        nearExactPenaltyPct: newNearExact,
        tmLookupOrder: newLookupOrder,
        linguistTerms: newLinguistTerms,
        ...newSwitches,
        filterProfileIds: newFilterProfileIds,
        qaProfileId: newQa,
        segmentationProfileId: newSegmentation,
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

  const displaySwitches = isCreate ? localSwitches : pickSwitches(values)
  const displayLinguistTerms = isCreate ? localLinguistTerms : values.linguistTerms
  const displayFilterProfileIds = isCreate ? localFilterProfileIds : values.filterProfileIds
  const displayQaProfileId = isCreate ? localQaProfileId : values.qaProfileId
  // A new project shows the suggested profile until one is picked, which is
  // what the form sends. A saved project or template names none after its
  // profile is deleted: the field stays empty and asks for one.
  const storedSegmentationId = isCreate ? localSegmentationProfileId : values.segmentationProfileId
  const segmentationMissing = !isCreate && storedSegmentationId == null
  // A saved project keeps its profile when the list no longer holds it: one
  // whose language was changed since.
  const keepsStoredProfile = !isCreate && storedSegmentationId != null && !!values.segmentationProfileName &&
    !segmentation.isLoading && !segmentation.error && !segmentation.profiles.some(p => p.id === storedSegmentationId)
  const displaySegmentationProfileId = segmentationMissing ? null
    : keepsStoredProfile ? storedSegmentationId : segmentation.pick(storedSegmentationId)
  const storedSegmentationLabel = values.segmentationProfileName && `${values.segmentationProfileName} (not for this language)`
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
  const selectedQaProfile = qaProfiles.find(p => p.id === displayQaProfileId)
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
      summary={<>{' '}— {tmRows.length} {tmRows.length === 1 ? 'TM' : 'TMs'}{tmRows.length > 0 && tmEntryTotal != null && ` · ${tmEntriesLabel(tmEntryTotal)}`} · {Math.round(scoringValues.fuzzyThreshold * 100)}% threshold</>}
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
          checked={displaySwitches.autoComplete}
          onChange={checked => handleScalarChange({ auto_complete: checked })}
          disabled={saving}
          data-testid="settings-auto-complete"
          label={<>
            <strong>Complete</strong> the project when every task is done
            <HelpTip text="When enabled, the project's status becomes Completed once every one of its tasks is completed, and returns to In progress if that stops being true." />
          </>}
        />

        {/* The "apply TM / fill in MT on start or upload" switches and the
            match scoring live in the Translation memory and Machine
            translation cards below, next to what they control, and the QA
            profile has its own card at the end. QP+APE is not an
            upload phase — it runs as a QE+APE workflow step. */}
      </div>

      {typeof afterProjectSettings === 'function'
        ? afterProjectSettings({ mtRouterName: selectedRouter?.name, mtOnUpload: displaySwitches.mtOnUpload })
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
          checked={displaySwitches.tmOnUpload}
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
            checked={mtLocked || displaySwitches.mtOnUpload}
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
          {selectedRouter && <OpenLinkButton to={mtRouterPath(selectedRouter.id)} label="Open MT router" data-testid="mt-router-open" />}
        </div>
      </div>

      {/* Terminology: who adds terms from the editor, then the term bases.
          One card, like Translation memory above. */}
      <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-4 space-y-4" data-testid="terminology-settings">
        <h3 className="text-sm font-medium text-gray-700 dark:text-gray-300">Terminology</h3>
        <LinguistTermsField
          value={displayLinguistTerms}
          disabled={saving}
          disabledReason={values.termAssignments.length === 0
            ? 'Add a term base to let linguists add terms.'
            : values.termAssignments.some(a => modeHasWrite(a.mode ?? 'read_write')) ? undefined : 'Tick Write on a term base to let linguists add terms.'}
          onChange={v => handleScalarChange({ linguist_terms: v })}
        />
        <AssignmentSection
          variant="embedded"
          title="Term bases"
          helpText="Assign term bases to language pairs. Terms from assigned term bases are highlighted in the CAT editor and used for QA terminology checks."
          items={values.termAssignments}
          resources={termBaseResources}
          resourcePath={termBasePath}
          resourceLabel="term base"
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
      </div>

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
          {selectedQaProfile && <OpenLinkButton to={qaProfilePath(selectedQaProfile.id)} label="Open QA profile" data-testid="qa-profile-open" />}
        </div>
      </div>

      <FileConfigurationsSection
        profiles={segmentation.profiles}
        value={displaySegmentationProfileId}
        storedLabel={storedSegmentationLabel || undefined}
        missing={segmentationMissing ? 'Pick a segmentation profile.' : undefined}
        disabled={saving}
        error={segmentation.error}
        onRetry={() => { void segmentation.refetch() }}
        onChange={id => handleScalarChange({ segmentation_profile_id: id })}
        filters={{
          byFormat: filterProfiles.byFormat,
          values: displayFilterProfileIds,
          error: filterProfiles.error,
          onRetry: () => { void filterProfiles.refetch() },
          onChange: (format, id) => handleScalarChange({ filter_profile_ids: { [format]: id } }),
        }}
      />

      {inlineCreateDialogs}
    </div>
    </TooltipProvider>
  )
}

/** picks applied to the stored map: null removes the format (its Default). */
function applyFilterPicks(
  stored: Partial<Record<FilterFormat, number>>,
  picks: Partial<Record<FilterFormat, number | null>>,
): Partial<Record<FilterFormat, number>> {
  const out = { ...stored }
  for (const [format, id] of Object.entries(picks) as [FilterFormat, number | null][]) {
    if (id == null) delete out[format]
    else out[format] = id
  }
  return out
}
