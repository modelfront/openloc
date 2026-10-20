import { Button } from '@/components/ui/button'
import { Alert } from '@/components/ui/alert'

/**
 * The "this section could not load" state for a tab or card that owns a
 * query: what failed, the server's message, and a Retry. A failed query must
 * never fall through to the loading placeholder — that reads as "still
 * loading" forever.
 */
export function LoadFailed({ what, error, onRetry }: { what: string; error: unknown; onRetry?: () => void }) {
  const message = error instanceof Error ? error.message : String(error)
  return (
    <Alert tone="error">
      <div className="flex flex-wrap items-center gap-3">
        <span>
          Couldn't load the {what}: {message}
        </span>
        {onRetry && (
          <Button size="sm" variant="outline" onClick={onRetry} className="ml-auto">Retry</Button>
        )}
      </div>
    </Alert>
  )
}
