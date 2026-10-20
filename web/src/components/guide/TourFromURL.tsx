import { useEffect, useRef } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { TOUR_PARAM, TOUR_STEP_PARAM, tourFromHash, tourPath } from '@/lib/demo'
import { runSavedTour } from '@/lib/playbooks'

/**
 * Tours named by the address of an ordinary page. `?tour=<name>&step=<n>`
 * (older links) moves to the tour's entry address, /tour/<name>/<n>. A page
 * loaded with a running tour after "#" (a reload mid-tour) continues that
 * tour at its step; outside the demo, App sends such an address to the entry
 * address before any page renders.
 */
export function TourFromURL() {
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const started = useRef(false)
  // The hash is read once, as the page loads: later changes are the running
  // tour's own.
  const loadedHash = useRef(tourFromHash(window.location.hash))
  const legacy = params.get(TOUR_PARAM)

  useEffect(() => {
    if (started.current) return
    if (legacy) {
      started.current = true
      navigate(tourPath(legacy, (Number(params.get(TOUR_STEP_PARAM)) || 1) - 1), { replace: true })
      return
    }
    const fromHash = loadedHash.current
    if (!fromHash) return
    started.current = true
    runSavedTour(queryClient, fromHash.tour, navigate, fromHash.start)
      .then(ok => { if (!ok) toast.info('This tour has nothing to show here yet.') })
      .catch((err: Error) => toast.error(`Could not start the tour: ${err.message}`))
  }, [legacy, params, navigate, queryClient])

  return null
}
