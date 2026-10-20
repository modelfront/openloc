import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Bug } from 'lucide-react'
import { getMe, isLoggedIn } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { useAppConfig } from '@/pages/docs/DocsHost'
import { Button } from '@/components/ui/button'
import { ReportIssueDialog } from './ReportIssueDialog'

/**
 * "Report this issue" on a crash screen, with the error as the title. Only for
 * a signed-in user outside the Playground, on a deployment with operators.
 */
export function ReportCrashButton({ error }: { error: Error }) {
  const [open, setOpen] = useState(false)
  const signedIn = isLoggedIn()
  const { data: config } = useAppConfig()
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe, enabled: signedIn })
  if (!signedIn || !config?.issues || !me || me.demo) return null
  const title = `Crash: ${error.message}`.slice(0, 200)
  return (
    <>
      <Button variant="outline" onClick={() => setOpen(true)} data-testid="report-crash">
        <Bug />
        Report this issue
      </Button>
      <ReportIssueDialog open={open} onClose={() => setOpen(false)} defaults={{ kind: 'bug', title }} />
    </>
  )
}
