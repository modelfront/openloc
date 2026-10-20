// /learn: the saved tours the user can take, and the way into the Playground.
// Every role: the server lists only the tours a role may take. A tour runs in
// the Playground (useStartSavedTour).
import { Compass } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { PageContainer } from '@/components/layout/PageContainer'
import { PageHeader } from '@/components/layout/PageHeader'
import { EmptyState } from '@/components/layout/EmptyState'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { listCardClass } from '@/components/list/list-row'
import { cn } from '@/lib/utils'
import { usePlaybooks, useStartSavedTour } from '@/lib/playbooks'

function LearnCard({ title, description, action, testId }: {
  title: string
  description: string
  action: React.ReactNode
  testId: string
}) {
  return (
    <div data-testid={testId} className={cn(listCardClass, 'flex items-center justify-between gap-4')}>
      <div className="min-w-0">
        <div className="font-medium text-gray-900 dark:text-gray-100">{title}</div>
        <p className="text-sm text-gray-500 dark:text-gray-400 mt-1">{description}</p>
      </div>
      {action}
    </div>
  )
}

export default function LearnPage() {
  const { isLoading, isError, error, refetch } = usePlaybooks()
  const { tours, inDemo, start, openDemo } = useStartSavedTour()

  return (
    <PageContainer>
      <PageHeader title="Learn" subtitle="Tours run in the Playground, on sample data. Nothing you do there is saved." />
      {isLoading ? (
        <LoadingPlaceholder />
      ) : isError ? (
        <LoadFailed what="tours" error={error} onRetry={() => refetch()} />
      ) : tours.length === 0 ? (
        <EmptyState message="No tours are available." />
      ) : (
        <div className="space-y-2" data-guide="learn.tours">
          {tours.map(pb => (
            <LearnCard
              key={pb.name}
              testId={`learn-tour-${pb.name}`}
              title={pb.title}
              description={pb.description}
              action={
                <Button variant="outline" size="sm" onClick={() => start(pb.name)} data-testid={`learn-start-${pb.name}`}>
                  <Compass className="h-4 w-4" />
                  Start tour
                </Button>
              }
            />
          ))}
          {!inDemo && (
            <LearnCard
              testId="learn-playground"
              title="Explore the Playground"
              description="Try every feature on sample data. Nothing is saved."
              action={
                <Button variant="outline" size="sm" onClick={openDemo} data-testid="learn-open-playground">
                  Open the Playground
                </Button>
              }
            />
          )}
        </div>
      )}
    </PageContainer>
  )
}
