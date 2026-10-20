import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listStepDefinitions, listWorkflows, type Project } from '../api'
import { queryKeys } from '../lib/query-keys'
import { defaultStepsFrom, isPretranslateOnly } from '../lib/workflow-steps'
import { unpickedRows, type WorkflowStep } from '../components/WorkflowStepsEditor'
import type { SettingsValues } from '../components/SettingsEditor'
import { DEFAULT_AUTO_COMPLETE, DEFAULT_INLINE_HTML, DEFAULT_PENALTY_PCT, DEFAULT_TM_LOOKUP_ORDER } from '../lib/settings-values'
import { usePrefillFromSource } from './usePrefillFromSource'

export const DEFAULT_SETTINGS: SettingsValues = {
  fuzzyThreshold: 0.75,
  sublanguagePenaltyPct: DEFAULT_PENALTY_PCT,
  nearExactPenaltyPct: DEFAULT_PENALTY_PCT,
  tmLookupOrder: DEFAULT_TM_LOOKUP_ORDER,
  mtOnUpload: true,
  tmOnUpload: true,
  autoComplete: DEFAULT_AUTO_COMPLETE,
  inlineHTML: DEFAULT_INLINE_HTML,
  qaProfileId: null,
  tmAssignments: [],
  mtRouterId: null,
  termAssignments: [],
  // Filled from the step library once it loads (defaultStepsFrom): the
  // tenant's Translation → Review definitions, matching what the server
  // creates when the form sends nothing.
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

  // Step library + workflow library. The defaults carry the tenant's
  // Translation → Review definitions once loaded.
  const { data: stepDefinitions } = useQuery({ queryKey: queryKeys.stepDefinitions.all, queryFn: listStepDefinitions })
  const { data: workflows } = useQuery({ queryKey: queryKeys.workflows.all, queryFn: listWorkflows })
  const defaultSettings = useMemo<SettingsValues>(
    () => ({ ...DEFAULT_SETTINGS, workflowSteps: defaultStepsFrom(stepDefinitions) }),
    [stepDefinitions],
  )

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

  // Seed the untouched form once, when both libraries have arrived: the first
  // library workflow when the org has any (its steps pre-filled, the project
  // based on it), else the default Translation → Review as custom steps. Once
  // only: an explicit "Custom" pick empties the list on purpose and a later
  // refetch must not refill it. A Duplicate deep link (?fromTemplate= /
  // ?fromProject=) prefills the form itself — with its own steps and
  // provenance — so it is left alone; selecting a workflow here would also
  // cancel that in-flight prefill (the hook keeps one request sequence).
  const seedDefaultWorkflow = async () => {
    if (workflows && workflows.length > 0) {
      await handleWorkflowChange(String(workflows[0].id))
      return
    }
    if (defaultSettings.workflowSteps.length > 0) {
      applySettings({ ...settingsRef.current, workflowSteps: defaultSettings.workflowSteps })
    }
  }
  const defaultsSeededRef = useRef(false)
  useEffect(() => {
    if (defaultsSeededRef.current || stepDefinitions === undefined || workflows === undefined) return
    defaultsSeededRef.current = true
    if (hasStartFromDeepLink || templateId || fromProjectId || settingsRef.current.workflowSteps.length > 0) return
    void seedDefaultWorkflow()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [stepDefinitions, workflows, defaultSettings])

  // "From scratch" after a template / project prefill: the untouched form
  // again — default settings, no source, and the same seeded workflow a
  // fresh page gets.
  const resetToDefaults = async () => {
    await handleTemplateChange('')
    await seedDefaultWorkflow()
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
