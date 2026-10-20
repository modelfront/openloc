import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { listComments, createComment, deleteComment, type SegmentComment } from '../api'
import { queryKeys } from '../lib/query-keys'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import { Button } from '@/components/ui/button'
import { DeleteButton } from '@/components/ui/delete-button'
import { Textarea } from '@/components/ui/textarea'

interface Props {
  translationId: number | null
}

export default function CommentPanel({ translationId }: Props) {
  const queryClient = useQueryClient()
  const [draft, setDraft] = useState('')

  const { data: comments } = useQuery({
    queryKey: queryKeys.comments.byTranslation(translationId!),
    queryFn: () => listComments(translationId!),
    enabled: !!translationId,
  })

  // Both mutations change the per-file count that drives the segment-grid
  // comment indicator + "Commented" filter, so they invalidate the counts
  // prefix alongside this translation's thread.
  const invalidateComments = () => {
    queryClient.invalidateQueries({ queryKey: queryKeys.comments.byTranslation(translationId!) })
    queryClient.invalidateQueries({ queryKey: queryKeys.comments.allCounts })
  }

  // The thread itself shows the change, so neither mutation has a success toast.
  const createMut = useMutationWithToast({
    mutationFn: () => createComment(translationId!, draft.trim()),
    successMessage: () => null,
    errorMessage: 'Comment failed',
    onSuccess: () => {
      setDraft('')
      invalidateComments()
    },
  })

  const deleteMut = useMutationWithToast({
    mutationFn: (commentId: number) => deleteComment(translationId!, commentId),
    successMessage: () => null,
    errorMessage: 'Delete failed',
    onSuccess: invalidateComments,
  })

  if (!translationId) {
    return <p className="text-xs text-gray-400">Select a segment to view comments.</p>
  }

  return (
    <div className="space-y-2">
      {/* Comment list */}
      {(!comments || comments.length === 0) ? (
        <p className="text-xs text-gray-400 dark:text-gray-500">No comments yet</p>
      ) : (
        <div className="space-y-2 max-h-[200px] overflow-y-auto">
          {comments.map((c: SegmentComment) => (
            <div key={c.id} className="text-xs border border-border rounded p-2 bg-muted">
              <div className="flex justify-between items-start">
                {/* Null author = AI/system comment — the
                    QE+APE step writes the LLM's `comment` output with no user. */}
                <span className="font-medium text-gray-700 dark:text-gray-300">{c.author_name || c.author_email || 'AI'}</span>
                <DeleteButton
                  size="icon-xs"
                  onClick={() => deleteMut.mutate(c.id)}
                  data-testid="comment-delete"
                  aria-label="Delete comment"
                  title="Delete comment"
                />
              </div>
              <p className="text-gray-600 dark:text-gray-400 mt-0.5 whitespace-pre-wrap">{c.content}</p>
              <span className="text-[9px] text-gray-400 dark:text-gray-600 mt-1 block">
                {new Date(c.created_at).toLocaleString()}
              </span>
            </div>
          ))}
        </div>
      )}

      {/* Add comment */}
      <form onSubmit={(e) => { e.preventDefault(); if (draft.trim()) createMut.mutate() }} className="flex gap-1">
        <Textarea
          value={draft}
          onChange={e => setDraft(e.target.value)}
          placeholder="Add a comment..."
          data-testid="comment-input"
          className="flex-1 py-1"
          inputClassName="text-xs min-h-6 resize-none"
          rows={1}
        />
        <Button
          type="submit"
          size="sm"
          data-testid="comment-add"
          disabled={!draft.trim() || createMut.isPending}
          className="h-8 text-xs px-2"
        >
          {createMut.isPending ? '...' : 'Add'}
        </Button>
      </form>
    </div>
  )
}
