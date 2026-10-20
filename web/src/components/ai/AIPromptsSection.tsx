import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Copy } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useQuery } from '@tanstack/react-query'
import { listAIPrompts, deleteAIPrompt, type AIPromptWithAdditions, type EnginePurpose } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import ConfirmDialog from '../ConfirmDialog'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { EmptyState } from '@/components/layout/EmptyState'
import { Button } from '@/components/ui/button'
import { ListSection } from '@/components/layout/SectionHeader'
import { listRowClass } from '@/components/list/list-row'
import { rowButtonProps } from '@/components/list/row-button'
import { EditableTitle } from '@/components/list/EditableTitle'
import { DeleteButton } from '@/components/ui/delete-button'
import { Chip } from '@/components/ui/chip'
import { MODE_LABELS, MODE_DESCRIPTIONS, flagsToMode, orderPrompts } from './output-mode'
import { promptsBasePath } from './prompt-routes'

// System prompt templates: the list. Opening one is a navigation —
// the editor is its own page (PromptDetailPage), because the form is a dozen
// fields deep and ends in a preview as long as the prompt itself.
//
// Manager-visible; write actions gated to owner/admin (backend also enforces).
export function AIPromptsSection({ canWrite, purpose, guide }: {
  canWrite: boolean
  purpose: EnginePurpose
  /** Tour anchor on the first prompt: a card beside one row, not over the list. */
  guide?: string
}) {
  const navigate = useNavigate()
  const base = promptsBasePath(purpose)
  const [deleteTarget, setDeleteTarget] = useState<{ id: number; name: string } | null>(null)

  const { data: allPrompts, isLoading, isError, error, refetch } = useQuery({ queryKey: queryKeys.aiPrompts.all, queryFn: listAIPrompts })
  // A system prompt's purpose is implicit in its output flags rather than
  // stored: only MT emits target text with no status and no comment, so
  // flagsToMode pins it exactly (the promptMode ↔ purpose rule).
  const prompts = useMemo(
    () => (allPrompts ?? []).filter(p => (flagsToMode(p) === 'mt') === (purpose === 'mt')),
    [allPrompts, purpose],
  )

  const deleteMut = useMutationWithToast({
    mutationFn: (id: number) => deleteAIPrompt(id),
    successMessage: 'Prompt deleted',
    errorMessage: 'Delete failed',
    invalidateKeys: [queryKeys.aiPrompts.all],
    onSuccess: () => setDeleteTarget(null),
  })

  // orderPrompts owns the rule — see output-mode.ts. Rendered as one flat list
  // rather than two groups: the split is ordering, not a visual section.
  const ordered = orderPrompts(prompts ?? [])

  const renderRow = (p: AIPromptWithAdditions, i: number) => (
    <PromptCard
      key={p.id}
      guide={i === 0 ? guide : undefined}
      prompt={p}
      canWrite={canWrite}
      onOpen={() => navigate(`${base}/${p.id}`)}
      onDelete={() => setDeleteTarget({ id: p.id, name: p.name })}
      // Duplicating opens the create page pre-filled from the source
      // prompt — the customize path for read-only defaults (works on any).
      onDuplicate={() => navigate(`${base}/new?from=${p.id}`)}
    />
  )

  return (
    <ListSection
      action={canWrite && (
        <Button size="sm" onClick={() => navigate(`${base}/new`)} data-testid="ai-prompt-add">New prompt</Button>
      )}
      data-testid="ai-prompts-section"
    >
      {isLoading ? (
        <LoadingPlaceholder />
      ) : isError && !allPrompts ? (
        <LoadFailed what="prompts" error={error} onRetry={() => refetch()} />
      ) : (
        <div className="space-y-2">
          {ordered.map(renderRow)}
          {ordered.length === 0 && <EmptyState message="No prompts yet." />}
        </div>
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={open => !open && setDeleteTarget(null)}
        title={`Delete prompt "${deleteTarget?.name}"?`}
        description="AI engines that use this prompt must be removed first."
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget.id)}
      />
    </ListSection>
  )
}

function PromptCard({
  prompt, canWrite, onOpen, onDelete, onDuplicate, guide,
}: {
  guide?: string
  prompt: AIPromptWithAdditions
  canWrite: boolean
  onOpen: () => void
  onDelete: () => void
  onDuplicate: () => void
}) {
  const mode = flagsToMode(prompt)
  // Built-in templates open read-only (view the stock text) and
  // can't be deleted. A dashed border marks them (no separate "Default" label).
  const clickable = canWrite || prompt.is_default
  return (
    <div
      data-guide={guide}
      className={cn(
        listRowClass,
        prompt.is_default && 'border border-dashed',
        clickable && 'cursor-pointer transition-colors hover:bg-muted',
      )}
      {...rowButtonProps(clickable ? onOpen : undefined)}
      data-testid={`ai-prompt-${prompt.id}`}
    >
      <div className="min-w-0 flex items-center gap-2 flex-wrap">
        <EditableTitle name={prompt.name} canEdit={false} className="font-medium text-gray-900 dark:text-gray-100" />
        <Chip title={MODE_DESCRIPTIONS[mode]} className="shrink-0 cursor-help">{MODE_LABELS[mode]}</Chip>
      </div>
      {canWrite && (
        <div className="flex shrink-0 items-center self-center">
          <Button variant="ghost" size="icon"
            className="text-muted-foreground"
            aria-label="Duplicate"
            title="Duplicate — create an editable copy"
            data-testid="ai-prompt-duplicate"
            onClick={e => { e.stopPropagation(); onDuplicate() }}
          >
            <Copy />
          </Button>
          {!prompt.is_default && (
            <DeleteButton aria-label="Delete" title="Delete" onClick={e => { e.stopPropagation(); onDelete() }} />
          )}
        </div>
      )}
    </div>
  )
}
