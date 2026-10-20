import { useId, useState } from 'react'
import { DisclosureToggle } from '@/components/ui/disclosure-toggle'
import type { RecordingEvent } from '@/api'
import { trailText } from '@/lib/action-trail'

/**
 * The action trail or the recording of a report, collapsed to its count: in
 * the report form, where the user sees what goes with the report, and on the
 * support team's issue page. A failed call's response, in a recording, shows
 * under its line. It is left out of every screenshot: a list of the user's
 * actions in time order never goes to a model.
 */
export function IssueTrail({ events, label, testId }: { events: RecordingEvent[]; label: string; testId: string }) {
  const [open, setOpen] = useState(false)
  const id = useId()
  if (events.length === 0) return null
  return (
    <div data-testid={testId} data-screenshot="exclude">
      <DisclosureToggle open={open} onToggle={() => setOpen(o => !o)} controls={id} data-testid={`${testId}-toggle`}
        summary={`${events.length} ${events.length === 1 ? 'event' : 'events'}`}>
        {label}
      </DisclosureToggle>
      {open && (
        <ol id={id} className="mt-1 space-y-0.5 text-xs" data-testid={`${testId}-events`}>
          {events.map((e, i) => (
            <li key={i} className="flex gap-2">
              <time dateTime={e.at} className="shrink-0 font-mono text-muted-foreground">{new Date(e.at).toLocaleTimeString()}</time>
              <span className="min-w-0 break-words">
                {trailText(e)}
                {e.kind === 'api' && e.body && (
                  <pre className="mt-0.5 max-h-24 overflow-auto whitespace-pre-wrap break-all rounded bg-muted px-1 font-mono">{e.body}</pre>
                )}
              </span>
            </li>
          ))}
        </ol>
      )}
    </div>
  )
}
