import { useCallback, useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { createProject, deleteProject, saveProjectDraft, type ProjectCreatePayload } from '../api'
import { queryKeys } from '../lib/query-keys'
import { useDebouncedCallback } from './useDebouncedCallback'

export type DraftSaveState = 'idle' | 'saving' | 'saved' | 'error'

/** Delay between the last edit and the autosave request. */
export const DRAFT_SAVE_DELAY_MS = 800

interface Options {
  /** The draft being resumed (from ?draft=), or null for a fresh wizard. */
  initialId: number | null
  /** The whole form as the server wants it; read at save time, not schedule time. */
  buildPayload: () => ProjectCreatePayload
}

/**
 * The New Project wizard's autosave. The first save
 * creates a status='draft' project and pins its id into the URL (?draft=) so
 * a reload resumes it; every later save replays the form with PUT /draft.
 *
 * Saves are serialized: a save waits for the one before it, so the create can
 * never run twice and two replays can never overtake each other. While a
 * draft is being resumed (`hydrating`) the prefill's own change events are
 * ignored — they describe what the server already has.
 */
export function useProjectDraft({ initialId, buildPayload }: Options) {
  const [draftId, setDraftId] = useState<number | null>(initialId)
  const idRef = useRef<number | null>(initialId)
  const [state, setState] = useState<DraftSaveState>('idle')
  const hydratingRef = useRef(initialId != null)
  const chainRef = useRef<Promise<unknown>>(Promise.resolve())
  const buildRef = useRef(buildPayload)
  useEffect(() => {
    buildRef.current = buildPayload
  }, [buildPayload])
  const queryClient = useQueryClient()

  /** Save the form now (after any save already running). Resolves to the draft id. */
  const saveNow = useCallback((): Promise<number> => {
    const run = chainRef.current.then(async () => {
      setState('saving')
      const payload = buildRef.current()
      try {
        if (idRef.current == null) {
          const project = await createProject({ ...payload, status: 'draft' })
          idRef.current = project.id
          setDraftId(project.id)
          // Outside the router on purpose: a re-render here would re-run the
          // deep-link prefill. Back/forward and reload see the draft.
          const url = new URL(window.location.href)
          url.searchParams.set('draft', String(project.id))
          window.history.replaceState(window.history.state, '', url)
        } else {
          await saveProjectDraft(idRef.current, payload)
        }
        // The draft is a row in the project list, and creating one can
        // introduce a new entry in the "Created by" filter's option set.
        queryClient.invalidateQueries({ queryKey: queryKeys.projects.paginated })
        queryClient.invalidateQueries({ queryKey: queryKeys.projects.creators })
        setState('saved')
        return idRef.current
      } catch (err) {
        setState('error')
        throw err
      }
    })
    chainRef.current = run.catch(() => undefined)
    return run as Promise<number>
  }, [queryClient])

  const debounced = useDebouncedCallback(() => {
    saveNow().catch((err: Error) => toast.error(`Draft not saved: ${err.message}`))
  }, DRAFT_SAVE_DELAY_MS)

  /** Call on every user change. Ignored while a resume is applying. */
  const scheduleSave = useCallback(() => {
    if (hydratingRef.current) return
    debounced()
  }, [debounced])

  /** Save a draft that already exists, and only then. A prefill choice (a
   *  template, From scratch over a source) rewrites the form without the user
   *  having filled anything in, so it keeps an open draft in step but must
   *  not bring one into being. Discard removes it instead — see
   *  discardDraft. */
  const scheduleSaveExisting = useCallback(() => {
    if (idRef.current == null) return
    scheduleSave()
  }, [scheduleSave])

  /** Fire the pending save now, if there is one (blur, leaving the page). */
  const flushPending = useCallback(() => debounced.flush(), [debounced])

  // Leaving the page must not lose the last edit.
  useEffect(() => () => debounced.flush(), [debounced])

  /** The draft id, creating the draft first when the form was never saved. */
  const ensureDraft = useCallback((): Promise<number> => {
    debounced.cancel()
    return idRef.current != null ? Promise.resolve(idRef.current) : saveNow()
  }, [debounced, saveNow])

  /** The resume prefill has been applied; user changes count from here. */
  const endHydration = useCallback(() => {
    hydratingRef.current = false
  }, [])

  /**
   * Throw the draft away — "Discard" abandons a start, and an abandoned
   * one must not stay in the project list as an "Untitled draft". The hook is
   * left as it was on a fresh wizard, so the next edit creates a new draft.
   *
   * Queued behind any save already running, so a create that is still in
   * flight is deleted rather than outliving the discard. A failed delete
   * keeps the id: the draft exists either way, and keeping it means the next
   * save overwrites it instead of orphaning it.
   */
  const discardDraft = useCallback(async () => {
    debounced.cancel()
    await chainRef.current
    const id = idRef.current
    if (id == null) return
    try {
      await deleteProject(id)
    } catch (err) {
      toast.error(`Draft not deleted: ${(err as Error).message}`)
      return
    }
    idRef.current = null
    setDraftId(null)
    setState('idle')
    // Same reason as the create's replaceState: staying outside the router
    // keeps the deep-link prefill from re-running.
    const url = new URL(window.location.href)
    url.searchParams.delete('draft')
    window.history.replaceState(window.history.state, '', url)
    queryClient.invalidateQueries({ queryKey: queryKeys.projects.paginated })
  }, [debounced, queryClient])

  return { draftId, state, scheduleSave, scheduleSaveExisting, flushPending, saveNow, ensureDraft, discardDraft, endHydration }
}
