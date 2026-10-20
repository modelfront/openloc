// Playbooks for the current role. A playbook with a saved
// tour runs in the browser with no assistant involved. Every way to start one —
// the Learn page, the "Take the tour" buttons, the assistant and ?tour= — goes
// through runSavedTour, which runs it in the demo organization, where every
// step has sample data and the demo-only elements exist.
import { useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { enterDemo, getMe, listPlaybooks, type PlaybookInfo } from '../api'
import { announcePlaygroundTourEnd, goToDemo, tourPath } from './demo'
import { queryKeys } from './query-keys'
import { runGuide } from './guide'

// The list is cached; starting a tour refetches it, because picked
// steps resolve to the organization's current items (the busiest job, the
// largest memory).
const playbooksQuery = {
  queryKey: queryKeys.chat.playbooks,
  queryFn: listPlaybooks,
  staleTime: Infinity,
  retry: false,
} as const

export function usePlaybooks() {
  return useQuery(playbooksQuery)
}

/**
 * Starts a saved tour, at `start` (a 0-based step) when given. Outside the
 * demo organization it enters the demo and the tour starts after the reload;
 * inside, it starts at once. False when the demo has no saved tour of that name.
 */
export async function runSavedTour(
  queryClient: QueryClient,
  name: string,
  navigate: (path: string) => void,
  start = 0,
): Promise<boolean> {
  const me = await queryClient.ensureQueryData({ queryKey: queryKeys.auth.me, queryFn: getMe })
  if (!me.demo) {
    await goToDemo(name, start)
    return true
  }
  return startPlaybookTourByName(queryClient, name, navigate, start)
}

/**
 * Opens a saved tour from its entry address (/tour/<name>/<n>). Outside the
 * demo it enters the demo and reloads the same address; inside, it opens the
 * step's page and starts the tour there. False when there is no such tour.
 */
export async function openTour(
  queryClient: QueryClient,
  name: string,
  start: number,
  navigate: (path: string, opts?: { replace?: boolean }) => void,
): Promise<boolean> {
  const me = await queryClient.ensureQueryData({ queryKey: queryKeys.auth.me, queryFn: getMe })
  if (!me.demo) {
    await enterDemo()
    window.location.replace(tourPath(name, start))
    return true
  }
  const list = await queryClient.fetchQuery({ ...playbooksQuery, staleTime: 0 })
  const pb = list.find(p => p.name === name)
  if (!pb || !hasSavedTour(pb)) return false
  const at = Math.min(Math.max(start, 0), pb.steps.length - 1)
  // A step on every page (the assistant launcher) has no page of its own: it
  // shows on the page of the step before it, or on the project list.
  const landing = pb.steps.slice(0, at + 1).reverse().find(s => s.path)?.path ?? '/projects'
  navigate(landing, { replace: true })
  runGuide(pb.steps, navigate, undefined, { name: pb.name, start: at, onEnd: announcePlaygroundTourEnd })
  return true
}

/** Runs a playbook's saved tour by name in this org; false when it has none. */
async function startPlaybookTourByName(
  queryClient: QueryClient,
  name: string,
  navigate: (path: string) => void,
  start: number,
): Promise<boolean> {
  const list = await queryClient.fetchQuery({ ...playbooksQuery, staleTime: 0 })
  const pb = list.find(p => p.name === name)
  if (!pb || !hasSavedTour(pb)) return false
  runGuide(pb.steps, navigate, undefined, { name: pb.name, start, onEnd: announcePlaygroundTourEnd })
  return true
}

const hasSavedTour = (pb: PlaybookInfo) => pb.steps.length > 0

/** A tour the demo organization can show, whatever this org holds. */
const hasDemoTour = (pb: PlaybookInfo) => pb.has_tour

/** The saved tours a user can pick, and how to start one (runSavedTour). */
export function useStartSavedTour() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const playbooks = usePlaybooks().data ?? []
  const inDemo = !!me?.demo
  const tours = playbooks.filter(inDemo ? hasSavedTour : hasDemoTour)
  const start = (name: string) => {
    runSavedTour(queryClient, name, navigate)
      .catch((err: Error) => toast.error(`Could not start the tour: ${err.message}`))
  }
  const openDemo = () => {
    goToDemo().catch((err: Error) => toast.error(`Could not open the demo: ${err.message}`))
  }
  return { tours, inDemo, start, openDemo }
}
