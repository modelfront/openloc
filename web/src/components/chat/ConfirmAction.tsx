// A guarded tool's preview inside a chat answer. Nothing
// has changed yet: the user reviews the preview in ConfirmDialog, and only
// their click posts the confirm token. An issue report is the exception: it
// opens the report form, filled in, and the form sends it. Composed from
// gallery parts only.
import { useState } from 'react'
import { ShieldAlert } from 'lucide-react'
import { toast } from 'sonner'
import { useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Chip } from '@/components/ui/chip'
import ConfirmDialog from '@/components/ConfirmDialog'
import { confirmChatAction, recordChatIssueReported, type ChatConfirmRequest, type Issue } from '../../api'
import { ReportIssueDialog } from '@/components/issues/ReportIssueDialog'
import { issueDefaultsFromToolArgs } from '@/lib/issue-labels'
import { ChatCard, ChatCardText } from './ChatCard'

export type ConfirmState = 'pending' | 'running' | 'done' | 'failed' | 'cancelled'

export interface PendingConfirm {
  request: ChatConfirmRequest
  state: ConfirmState
}

const STATE_LABEL: Record<ConfirmState, string> = {
  pending: 'Needs your confirmation',
  running: 'Running',
  done: 'Done',
  failed: 'Failed',
  cancelled: 'Cancelled',
}

const STATE_COLOR = {
  pending: 'yellow',
  running: 'blue',
  done: 'green',
  failed: 'red',
  cancelled: 'grey',
} as const

// The tool whose preview opens the report form instead of a confirm dialog.
const REPORT_ISSUE_TOOL = 'openloc_report_issue'

// Destructive tools are the deletes; edits to shared settings confirm with a
// neutral button.
const isDelete = (tool: string) => /^openloc_(delete|remove|revoke|clear)_/.test(tool)

export function ConfirmAction({
  item,
  getConversationId,
  onStateChange,
}: {
  item: PendingConfirm
  /** Read at click time: the id arrives with the stream's done event. */
  getConversationId: () => number | undefined
  onStateChange: (state: ConfirmState) => void
}) {
  const [open, setOpen] = useState(false)
  const queryClient = useQueryClient()
  const { request, state } = item
  const description = [request.summary, ...(request.effects ?? [])].join(' ')
  // An issue report is reviewed in the real report form, which adds the
  // browser details and files; the form sends it.
  const isReport = request.tool === REPORT_ISSUE_TOOL
  const [reviewing, setReviewing] = useState(false)
  const onReported = (issue: Issue) => {
    onStateChange('done')
    const conversationId = getConversationId()
    if (conversationId !== undefined) {
      recordChatIssueReported(conversationId, issue.id).catch(() => {
        // The report is saved; only the assistant's note of it is lost.
      })
    }
  }

  const confirm = async () => {
    const conversationId = getConversationId()
    if (conversationId === undefined) {
      toast.error('The answer is still streaming. Try again in a moment.')
      return
    }
    onStateChange('running')
    try {
      const res = await confirmChatAction({
        conversation_id: conversationId,
        tool: request.tool,
        args: request.args,
        confirm_token: request.token,
      })
      onStateChange(res.ok ? 'done' : 'failed')
      if (res.ok) {
        toast.success('Done.')
        // A guarded tool can change any entity; refetch what is on screen.
        void queryClient.invalidateQueries()
      } else {
        toast.error(res.message)
      }
    } catch (err) {
      onStateChange('failed')
      toast.error((err as Error).message)
    }
  }

  return (
    <ChatCard className="flex flex-wrap items-center gap-2" data-testid="chat-confirm" data-state={state}>
      <Chip color={STATE_COLOR[state]} icon={<ShieldAlert />}>{STATE_LABEL[state]}</Chip>
      <ChatCardText className="min-w-0 flex-1">{request.summary}</ChatCardText>
      {state === 'pending' && (
        <>
          <Button size="xs" variant="ghost" onClick={() => onStateChange('cancelled')}>
            Cancel
          </Button>
          <Button
            size="xs"
            variant={isDelete(request.tool) ? 'destructive' : 'default'}
            onClick={() => (isReport ? setReviewing(true) : setOpen(true))}
            data-testid="chat-confirm-review"
          >
            {isReport ? 'Review and send' : 'Review'}
          </Button>
        </>
      )}
      {isReport && (
        <ReportIssueDialog
          open={reviewing}
          onClose={() => setReviewing(false)}
          defaults={{ ...issueDefaultsFromToolArgs(request.args, request.verification_token), fromAssistant: true }}
          onCreated={onReported}
        />
      )}
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title="Confirm this change"
        description={description}
        actionLabel={isDelete(request.tool) ? 'Delete' : 'Confirm'}
        variant={isDelete(request.tool) ? 'destructive' : 'default'}
        onConfirm={() => void confirm()}
      />
    </ChatCard>
  )
}
