import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listWorkflows, type Project } from '../api'
import { queryKeys } from '../lib/query-keys'
import { isPretranslateOnly } from '../lib/workflow-steps'
import { unpickedRows, type WorkflowStep } from '../components/WorkflowStepsEditor'
import type { SettingsValues } from '../components/SettingsEditor'
import { DEFAULT_AUTO_COMPLETE, DEFAULT_LINGUIST_TERMS, DEFAULT_PENALTY_PCT, DEFAULT_TM_LOOKUP_ORDER } from '../lib/settings-values'
import { usePrefillFromSource } from './usePrefillFromSource'
import { orgDefaultsMap, useFilterProfiles } from './useFilterProfiles'
import { toast } from 'sonner'
import { focusWorkflowPicker } from '../components/project/ProjectCreateWorkflowCard'

export const DEFAULT_SETTINGS: SettingsValues = {
  fuzzyThreshold: 0.75,
  sublanguagePenaltyPct: DEFAULT_PENALTY_PCT,
  nearExactPenaltyPct: DEFAULT_PENALTY_PCT,
  tmLookupOrder: DEFAULT_TM_LOOKUP_ORDER,
  mtOnUpload: true,
  tmOnUpload: true,
  autoComplete: DEFAULT_AUTO_COMPLETE,
  // None picked: every format uses its Default. The form replaces this with
  // the org defaults once they load (useProjectCreateSettings).
  filterProfileIds: {},
  linguistTerms: DEFAULT_LINGUIST_TERMS,
  qaProfileId: null,
  // Null until picked: the form shows and sends the profile the server
  // suggests for the source language.
  segmentationProfileId: null,
  tmAssignments: [],
  mtRouterId: null,
  termAssignments: [],
  // No steps until a workflow is picked: nothing fills in a default.
  workflowSteps: [],
}

/** Submit-time check of the step draft. A linked workflow stands in for an
 *  empty list; every remaining row must reference a library step. Returns the
 *  message to show, or null when the draft is sendable. */
export function workflowStepsError(steps: WorkflowStep[], workflowId: string): string | null {
  if (!workflowId && steps.length === 0) return 'Pick a workflow, or create one'
  const unpicked = unpickedRows(steps)
  if (unpicked.length > 0) return `Pick a step for workflow row ${unpicked.join(', ')} or remove it`
  return null
}

/** Message of the server's rule that a live Pre-translation project has an
 *  MT router to translate with. */
const PRETRANSLATE_NEEDS_ROUTER = 'Choose an MT router: the Pre-translation workflow fills files in with machine translation.'

/** Submit-time check: Pre-translation needs an MT router. Returns the
 *  message to show, or null. A draft may lack one; publishing may not. */
export function pretranslateRouterError(settings: Pick<SettingsValues, 'workflowSteps' | 'mtRouterId'>): string | null {
  return isPretranslateOnly(settings.workflowSteps) && settings.mtRouterId == null ? PRETRANSLATE_NEEDS_ROUTER : null
}

interface Options {
  /** Deep-link prefill waits for the resource queries it derives from. */
  ready: boolean
  tmCollections?: { id: number; name: string; source_language?: string | null; target_languages?: string[] | null }[]
  termBases?: { id: number; name: string }[]
  applyLanguages: (source: string | undefined, targets: string[]) => void
  applyMetadata: (meta: { name: string; domain: string | null; customerId: number | null }) => void
  /** Metadata of a resumed draft (?draft=); see usePrefillFromSource. */
  applyDraft?: (project: Project) => void
}

// The New Project page's settings draft: the SettingsValues carrier (a ref
// that collects the latest draft for submit, plus a snapshot in state so a
// prefill re-renders the editors), the "Start from" prefill, and the workflow
// step draft that the Workflow card owns.
export function useProjectCreateSettings({ ready, tmCollections, termBases, applyLanguages, applyMetadata, applyDraft }: Options) {
  const settingsRef = useRef<SettingsValues>(DEFAULT_SETTINGS)
  const [settingsSnapshot, setSettingsSnapshot] = useState<SettingsValues>(DEFAULT_SETTINGS)
  const applySettings = useCallback((values: SettingsValues) => {
    settingsRef.current = values
    setSettingsSnapshot(values)
  }, [])

  // The workflow library, for the Workflow card's dropdown.
  const { data: workflows } = useQuery({ queryKey: queryKeys.workflows.all, queryFn: listWorkflows })

  // Without a template the form starts from the org's default filter
  // profiles, and "From scratch" goes back to them. The form sends every
  // format, so the server never fills them in for it.
  const { profiles: filterProfiles, isSuccess: filterProfilesLoaded } = useFilterProfiles()
  const orgFilterDefaults = useMemo(() => orgDefaultsMap(filterProfiles), [filterProfiles])
  const defaultSettings = useMemo(() => ({ ...DEFAULT_SETTINGS, filterProfileIds: orgFilterDefaults }), [orgFilterDefaults])
  const filterDefaultsSeeded = useRef(false)
  useEffect(() => {
    // Only a loaded list: after a failed load, the retry still seeds.
    if (filterDefaultsSeeded.current || !filterProfilesLoaded) return
    filterDefaultsSeeded.current = true
    // A prefill or the user got there first: their picks stay.
    if (settingsRef.current.filterProfileIds !== DEFAULT_SETTINGS.filterProfileIds) return
    if (Object.keys(orgFilterDefaults).length === 0) return
    applySettings({ ...settingsRef.current, filterProfileIds: orgFilterDefaults })
  }, [filterProfilesLoaded, orgFilterDefaults, applySettings])

  const {
    templateId, templateName, fromProjectId, fromProjectName, workflowId, hasStartFromDeepLink,
    handleTemplateChange, handleFromProjectChange, handleWorkflowChange,
  } = usePrefillFromSource({
    defaultSettings,
    currentSettings: () => settingsRef.current,
    ready,
    tmCollections,
    termBases,
    applySettings,
    applyLanguages,
    applyMetadata,
    applyDraft,
  })

  // "From scratch" after a template / project prefill: the untouched form
  // again — default settings, no source and no steps.
  const resetToDefaults = async () => {
    await handleTemplateChange('')
  }

  // Workflow steps are owned by the Workflow card; the settings editor's draft
  // still carries the `workflowSteps` snapshot it mounted with, so a scalar or
  // assignment change must never merge it back — the step draft always wins.
  const handleSettingsChange = useCallback((newSettings: SettingsValues) => {
    settingsRef.current = { ...newSettings, workflowSteps: settingsRef.current.workflowSteps }
  }, [])

  // The Workflow card's own writes into the step draft — the deadlines, which
  // are per step and per target language. Goes through applySettings so the
  // snapshot re-renders the fields the user is typing into.
  const setWorkflowSteps = useCallback((workflowSteps: WorkflowStep[]) => {
    applySettings({ ...settingsRef.current, workflowSteps })
  }, [applySettings])

  // A submit with no workflow points at the dropdown and frames it red until
  // a workflow is picked.
  const [workflowRequired, setWorkflowRequired] = useState(false)
  const workflowInvalid = workflowRequired && workflowStepsError(settingsSnapshot.workflowSteps, workflowId) != null
  // `optional`: a template may have no workflow, so no workflow and no steps
  // pass; a half-filled step list is still refused.
  const requireWorkflow = ({ optional = false }: { optional?: boolean } = {}) => {
    const steps = settingsRef.current.workflowSteps
    if (optional && !workflowId && steps.length === 0) return true
    const stepsError = workflowStepsError(steps, workflowId)
    if (!stepsError) return true
    toast.error(stepsError)
    setWorkflowRequired(true)
    focusWorkflowPicker()
    return false
  }

  return {
    /** Render-time values for the editors; re-set by prefills and seeding. */
    settings: settingsSnapshot,
    /** The live draft, read on submit. */
    currentSettings: () => settingsRef.current,
    workflows,
    templateId,
    templateName,
    fromProjectId,
    fromProjectName,
    workflowId,
    /** True after a refused submit, until a workflow is picked: frame the field red. */
    workflowInvalid,
    /** The submit-time workflow check: false, with a toast and the dropdown pointed at, when none is picked (or, with `optional`, when the steps are half-filled). */
    requireWorkflow,
    /** True when ?fromTemplate= / ?fromProject= prefills the form. */
    hasStartFromDeepLink,
    handleTemplateChange,
    handleFromProjectChange,
    handleWorkflowChange,
    setWorkflowSteps,
    resetToDefaults,
    handleSettingsChange,
  }
}
