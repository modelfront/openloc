// Workflow tab shared by ProjectDetail and TemplateDetail:
// one card holding the picker and, under it, the steps it produced, read-only.
// A template also gets the mode toggle; a created project does not — the
// server refuses the switch after setup, so a Raw MT project stays locked.
// The same shape the New project wizard uses — the steps of a project or
// template come from the Workflows library and are changed by picking a
// different workflow, never edited in place.
//
// A project's deadlines are the exception, because they are scheduling rather
// than part of the workflow's identity: each step's date
// sits at the right of its own row and is editable here. A template has no
// deadlines, so it gets the plain chips.
import { useEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { toast } from 'sonner'
import { listWorkflows } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { useDebouncedCallback } from '../../hooks/useDebouncedCallback'
import { DRAFT_SAVE_DELAY_MS } from '../../hooks/useProjectDraft'
import { sameWorkflowSteps, workflowMode } from '../../lib/workflow-steps'
import type { WorkflowStep } from '../WorkflowStepsEditor'
import { WorkflowStepsWithDeadlines } from './WorkflowDeadlines'
import { WorkflowPicker } from './WorkflowPicker'
import { WorkflowStepChips } from './WorkflowStepChips'
import { PretranslateWorkflowIcon, WorkflowModeToggle } from './WorkflowModeToggle'
import { MTRouterLink, RawMTPane } from './RawMTPane'
import { errorText } from '@/lib/error-text'

interface Props {
  steps: WorkflowStep[]
  workflowId: number | null | undefined
  workflowName: string | null | undefined
  /** Project context: only decides how the picker words its help text. */
  sourceLang?: string
  /** A project's target languages — enables the per-language deadline rows. */
  targetLangs?: string[]
  /** The MT router and whether MT runs on upload, for the Pre-translation row. */
  mtRouterName?: string | null
  mtOnUpload?: boolean
  /** Takes the user to the MT router picker (it is on the Settings tab). */
  onPickRouter?: () => void
  onApplyWorkflow: (workflowId: number) => Promise<void>
  /** Persists a deadline edit. Absent for a template, which has no deadlines. */
  onSaveSteps?: (steps: WorkflowStep[]) => Promise<void>
  /** Extra sentence in the "use this workflow" confirmation — projects mention in-flight jobs. */
  applyWarning?: string
}

export function WorkflowTab({
  steps, workflowId, workflowName, sourceLang, targetLangs, onApplyWorkflow, onSaveSteps, applyWarning, mtRouterName, mtOnUpload, onPickRouter,
}: Props) {
  const { data: workflows } = useQuery({ queryKey: queryKeys.workflows.all, queryFn: listWorkflows })
  const isProject = !!sourceLang
  const schedulable = isProject && !!onSaveSteps

  // Deadlines autosave. The draft holds what the fields show until the save
  // lands and the refetched steps take over; a debounce keeps a typed date
  // from re-dating the project's live jobs once per keystroke.
  const [deadlineDraft, setDeadlineDraft] = useState<WorkflowStep[] | null>(null)
  const [savingDeadlines, setSavingDeadlines] = useState(false)
  // True from the edit until its save resolves. The steps-changed effect below
  // reads it so an unrelated refetch mid-edit cannot throw the draft away.
  const unsavedRef = useRef(false)
  // The save is a full-replace PUT, so two in flight at once can land out of
  // order and resurrect an older list. Chain them instead.
  const inFlightRef = useRef<Promise<void>>(Promise.resolve())

  const saveDeadlines = useDebouncedCallback((next: WorkflowStep[]) => {
    inFlightRef.current = inFlightRef.current.then(async () => {
      setSavingDeadlines(true)
      try {
        await onSaveSteps?.(next)
        unsavedRef.current = false
        // One id for every autosave, so editing several steps in a row replaces
        // the same toast instead of stacking one per save.
        toast.success('Deadlines saved', { id: 'workflow-deadlines-saved' })
      } catch (err) {
        // Drop the draft so the fields snap back to what is actually stored,
        // rather than showing a date the server rejected as if it were saved.
        unsavedRef.current = false
        setDeadlineDraft(null)
        toast.error(errorText(err, 'Deadlines not saved'))
      } finally {
        setSavingDeadlines(false)
      }
    })
  }, DRAFT_SAVE_DELAY_MS)

  const changeDeadlines = (next: WorkflowStep[]) => {
    setDeadlineDraft(next)
    // The list reports itself on mount and after every sync, and adding a
    // blank override row changes nothing that gets sent. Only a real
    // difference is worth a request.
    if (sameWorkflowSteps(next, steps)) {
      unsavedRef.current = false
      saveDeadlines.cancel()
      return
    }
    unsavedRef.current = true
    saveDeadlines(next)
  }

  // Leaving the tab mid-debounce must not lose the last edit.
  useEffect(() => () => saveDeadlines.flush(), [saveDeadlines])

  // A pending draft is a whole step list, so keeping it after the steps
  // themselves changed underneath (a workflow applied) would send back the list
  // as it was and undo that change. Drop the draft instead — unless it is an
  // edit still on its way to the server.
  const prevStepsRef = useRef(steps)
  useEffect(() => {
    if (steps !== prevStepsRef.current) {
      prevStepsRef.current = steps
      if (!unsavedRef.current) setDeadlineDraft(null)
    }
  }, [steps])

  const applyWorkflow = async (id: number) => {
    try {
      await onApplyWorkflow(id)
      toast.success('Workflow applied')
    } catch (err) {
      toast.error(errorText(err, 'Workflow not applied'))
    }
  }
  const rawMT = workflowMode(steps) === 'raw_mt'
  const library = workflows ?? []
  const previousWorkflowRef = useRef<number | null>(null)
  const heading = !isProject && (
    <WorkflowModeToggle
      steps={steps}
      workflowId={workflowId ?? null}
      workflows={library}
      previousWorkflowRef={previousWorkflowRef}
      confirmApply
      applyWarning={applyWarning}
      onApply={applyWorkflow}
    />
  )
  // Without a router nothing runs on upload, so there is no row to show. A
  // created Raw MT project lists the built-in step itself, so no row either.
  const pretranslation = mtOnUpload && mtRouterName && !rawMT
    ? <MTRouterLink mtRouterName={mtRouterName} onPickRouter={onPickRouter} />
    : undefined
  const stepRows = schedulable ? (
    <WorkflowStepsWithDeadlines
      steps={deadlineDraft ?? steps}
      targetLanguages={targetLangs ?? []}
      onChange={changeDeadlines}
      pretranslation={pretranslation}
    >
      {savingDeadlines && (
        <p className="pt-3 text-xs text-gray-500 dark:text-gray-400" data-testid="workflow-deadlines-saving">
          Saving…
        </p>
      )}
    </WorkflowStepsWithDeadlines>
  ) : (
    <WorkflowStepChips steps={steps} pretranslation={pretranslation} />
  )

  return (
    <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-4 space-y-4" data-guide="project_workflow.picker">
      {/* Unlike the wizard, these steps may already be in flight, so a swap is
          confirmed first. */}
      {!isProject && rawMT ? (
        <RawMTPane mtRouterName={mtRouterName} onPickRouter={onPickRouter} heading={heading} />
      ) : (
        <WorkflowPicker
          workflows={library}
          value={workflowId ?? null}
          valueName={workflowName}
          valueIcon={rawMT ? <PretranslateWorkflowIcon /> : undefined}
          locked={rawMT}
          heading={heading}
          entity={isProject ? 'project' : 'template'}
          confirmApply
          applyWarning={applyWarning}
          onApply={applyWorkflow}
        >
          {stepRows}
        </WorkflowPicker>
      )}
    </div>
  )
}
