// /tour/<name>/<step>: the entry address of a saved tour, the link the tour
// pill copies. It shows no organization's data: outside the demo it enters
// the demo first, then it opens the step's own page and starts the tour.
import { useEffect, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { PageContainer } from '@/components/layout/PageContainer'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { EmptyState } from '@/components/layout/EmptyState'
import { openTour } from '@/lib/playbooks'

export default function TourEntryPage() {
  const { name = '', step } = useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const opened = useRef(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (opened.current) return
    opened.current = true
    openTour(queryClient, name, (Number(step) || 1) - 1, navigate)
      .then(ok => { if (!ok) setError('This tour does not exist.') })
      .catch((err: Error) => setError(`Could not open the tour: ${err.message}`))
  }, [name, step, navigate, queryClient])

  return (
    <PageContainer>
      {error ? <EmptyState message={error} /> : <LoadingPlaceholder message="Opening the tour…" delayMs={0} />}
    </PageContainer>
  )
}
