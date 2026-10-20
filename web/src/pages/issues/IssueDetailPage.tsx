import { useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { addIssueNote, commentOnIssue, getIssue, getMe, type IssueDetail, type IssueScope } from '@/api'
import { issuesKey, queryKeys } from '@/lib/query-keys'
import { issueListPath } from '@/lib/issue-labels'
import { drawAttention } from '@/lib/attention'
import { PageContainer } from '@/components/layout/PageContainer'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { IssueCommentBox, IssueSummary } from '@/components/issues/IssueParts'
import { IssueConversation } from '@/components/issues/IssueConversation'
import { IssueFieldsForm, IssueStatusForm } from '@/components/issues/IssueSupportPanel'
import { IssueConfirmFix } from '@/components/issues/IssueConfirmFix'
import { IssueKindForm } from '@/components/issues/IssueKindForm'
import { IssueTrail } from '@/components/issues/IssueTrail'
import { useDeleteIssue } from '@/components/issues/useDeleteIssue'
import { DeleteButton } from '@/components/ui/delete-button'

/**
 * One issue with its conversation. `mine` is the reporter's org's view, which
 * confirms a fix; `platform` is the support team's, which also assigns,
 * prioritizes, tags, changes the status and writes internal notes.
 */
export default function IssueDetailPage({ scope }: { scope: IssueScope }) {
  const id = Number(useParams().issueId)
  const queryClient = useQueryClient()
  const [stillBroken, setStillBroken] = useState(false)
  const detailKey = scope === 'mine' ? queryKeys.issues.detail(id) : queryKeys.platform.issue(id)
  const listKey = issuesKey(scope)
  const { data, error, isLoading, refetch } = useQuery({ queryKey: detailKey, queryFn: () => getIssue(scope, id) })
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const listPath = issueListPath(scope)
  const navigate = useNavigate()
  const { ask, dialog } = useDeleteIssue(scope, () => navigate(listPath, { replace: true }))

  // A reply or any change returns the whole issue: show it, and let the
  // lists re-read their markers.
  const applyUpdate = (detail: IssueDetail) => {
    queryClient.setQueryData(detailKey, detail)
    queryClient.invalidateQueries({ queryKey: listKey })
  }
  const commentMut = useMutationWithToast({
    mutationFn: ({ body, files }: { body: string; files: File[] }) => commentOnIssue(scope, id, body, files),
    successMessage: () => null,
    onSuccess: d => { applyUpdate(d); setStillBroken(false) },
  })
  const noteMut = useMutationWithToast({
    mutationFn: ({ body, files }: { body: string; files: File[] }) => addIssueNote(id, body, files),
    successMessage: () => null,
    onSuccess: applyUpdate,
  })

  if (isLoading) return <LoadingPlaceholder />
  if (!data) return <PageContainer><LoadFailed what="the issue" error={error} onRetry={() => void refetch()} /></PageContainer>

  const { issue, comments } = data
  const platform = scope === 'platform'
  const reply = (
    <IssueCommentBox
      sending={commentMut.isPending}
      onSend={(body, files) => commentMut.mutateAsync({ body, files })}
      placeholder={platform ? 'Reply to the reporter'
        : stillBroken ? 'What is still broken? Your reply reopens the issue.' : 'Add details or answer the team'}
    />
  )
  return (
    <PageContainer>
      <Breadcrumbs
        items={[
          { label: 'Reported issues', to: listPath },
          { label: <span className="truncate">{issue.title}</span> },
        ]}
        actions={issue.can_delete && (
          <DeleteButton size="icon-sm" title="Delete issue" aria-label="Delete issue" data-testid="issue-delete" onClick={() => ask(issue)} />
        )}
      />
      {dialog}
      <div className="max-w-3xl space-y-8">
        <IssueSummary issue={issue} scope={scope} />
        {!platform && <IssueKindForm issueId={id} kind={issue.kind} onUpdated={applyUpdate} />}
        {platform && data.trail && (
          <IssueTrail events={data.trail} label="What happened before the report" testId="issue-trail" />
        )}
        {platform && data.recording && (
          <IssueTrail events={data.recording.events} label="The steps the reporter recorded" testId="issue-recording" />
        )}
        {platform && (
          <>
            <IssueFieldsForm issue={issue} onUpdated={applyUpdate} />
            <IssueStatusForm key={issue.status} issueId={id} current={issue.status} onUpdated={applyUpdate} />
          </>
        )}
        {!platform && issue.status === 'resolved' && !issue.confirmed_at && (
          <IssueConfirmFix issueId={id} onConfirmed={applyUpdate} onStillBroken={() => {
            setStillBroken(true)
            drawAttention({ target: '[data-testid="issue-comment-box"]', focus: 'textarea' })
          }} />
        )}
        <section className="space-y-3">
          <h2 className="text-sm font-medium">Conversation</h2>
          <IssueConversation comments={comments} scope={scope} issueId={id} meId={me?.id} />
          {platform ? (
            <Tabs defaultValue="reply">
              <TabsList>
                <TabsTrigger value="reply" data-testid="issue-composer-reply">Reply to reporter</TabsTrigger>
                <TabsTrigger value="note" data-testid="issue-composer-note">Internal note</TabsTrigger>
              </TabsList>
              <TabsContent value="reply" className="pt-3">{reply}</TabsContent>
              <TabsContent value="note" className="pt-3">
                <IssueCommentBox
                  sending={noteMut.isPending}
                  onSend={(body, files) => noteMut.mutateAsync({ body, files })}
                  placeholder="Only the support team sees this note"
                />
              </TabsContent>
            </Tabs>
          ) : reply}
        </section>
      </div>
    </PageContainer>
  )
}
