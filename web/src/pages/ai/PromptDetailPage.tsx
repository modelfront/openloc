import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import {
  listAIPrompts, createAIPrompt, updateAIPrompt,
  type AIPromptWithAdditions, type AIPromptInput, type EnginePurpose,
} from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import { useCanWriteAI } from '../../hooks/useCanWriteAI'
import { PageHeader } from '@/components/layout/PageHeader'
import { PageContainer } from '@/components/layout/PageContainer'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { EditableTitle } from '@/components/list/EditableTitle'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { EmptyState } from '@/components/layout/EmptyState'
import { PromptForm } from '../../components/ai/PromptForm'
import { promptsBasePath } from '../../components/ai/prompt-routes'
import { usePageTitle } from '../../hooks/usePageTitle'
import { TextLink } from '@/components/ui/text-link'

// One system prompt, on its own page: /ai/prompts/:promptId, /mt/prompts/new,
// and /…/new?from=<id> for a duplicate.
//
// A page, not a dialog: the form is a dozen fields deep and the URL is
// shareable.

export default function PromptDetailPage({ purpose }: { purpose: EnginePurpose }) {
  const { promptId } = useParams<{ promptId: string }>()
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const canWrite = useCanWriteAI()
  const base = promptsBasePath(purpose)

  // The list is already cached by the page that linked here, so reusing its
  // query key opens the editor without a second round trip — and a rename
  // shows up in both places at once.
  const { data: prompts, isLoading } = useQuery({ queryKey: queryKeys.aiPrompts.all, queryFn: listAIPrompts })

  const creating = promptId === 'new'
  // On a duplicate the source only seeds the form; what gets saved is new.
  const sourceID = Number(creating ? params.get('from') : promptId)
  const source = (prompts ?? []).find(p => p.id === sourceID)
  usePageTitle(creating ? undefined : source?.name)

  const backToList = () => navigate(base)

  const createMut = useMutationWithToast({
    mutationFn: (input: AIPromptInput) => createAIPrompt(input),
    successMessage: 'Prompt created',
    errorMessage: 'Create failed',
    invalidateKeys: [queryKeys.aiPrompts.all],
    onSuccess: backToList,
  })

  const updateMut = useMutationWithToast({
    mutationFn: (input: AIPromptInput) => updateAIPrompt(sourceID, input),
    successMessage: 'Prompt updated',
    errorMessage: 'Update failed',
    invalidateKeys: [queryKeys.aiPrompts.all],
    onSuccess: backToList,
  })

  const renameMut = useMutationWithToast({
    mutationFn: (name: string) => {
      if (!source) throw new Error('Prompt not found')
      const { prompt_text, out_status, out_text, out_note, out_comments, segment_context, language_additions } = source
      return updateAIPrompt(sourceID, {
        name, prompt_text, out_status, out_text, out_note, out_comments, segment_context, language_additions,
      })
    },
    successMessage: 'Prompt renamed',
    errorMessage: 'Rename failed',
    invalidateKeys: [queryKeys.aiPrompts.all],
  })

  if (isLoading) {
    return <PageContainer><LoadingPlaceholder /></PageContainer>
  }
  // A stale link, or someone else's prompt. Editing is the only case that
  // needs the row — a plain create has no source at all.
  if (!creating && !source) {
    return (
      <PageContainer>
        <Breadcrumbs items={[{ label: 'Prompts', to: base }, { label: 'Not found' }]} />
        <EmptyState message="This prompt no longer exists." />
      </PageContainer>
    )
  }

  const asCopy = creating && !!source
  const readOnly = !creating && (!canWrite || !!source?.is_default)
  const title = creating ? 'New prompt' : (source as AIPromptWithAdditions).name

  return (
    <PageContainer>
      {creating ? (
        <>
          <TextLink to={base} className="mb-4 inline-block text-sm">
            &larr; Prompts
          </TextLink>
          <PageHeader title="New prompt" className="border-b border-border pb-3" />
        </>
      ) : (
        <Breadcrumbs
          items={[
            { label: 'Prompts', to: base },
            { label: <span className="flex min-w-0 items-center gap-2">
              <EditableTitle
                name={title}
                onRename={name => renameMut.mutate(name)}
                canEdit={!readOnly && !renameMut.isPending && !updateMut.isPending}
                data-testid="rename-prompt"
              />

            </span> },
          ]}
        />
      )}

      <PromptForm
        // Remount when the route changes so the form re-seeds: react-hook-form
        // reads defaultValues once, and without this, going from one prompt to
        // another would keep the first one's text.
        key={`${promptId}:${params.get('from') ?? ''}`}
        initial={creating ? source : (source as AIPromptWithAdditions)}
        asCopy={asCopy}
        readOnly={readOnly}
        purpose={purpose}
        takenNames={(prompts ?? []).map(p => p.name)}
        isPending={createMut.isPending || updateMut.isPending || renameMut.isPending}
        onSubmit={input => (creating ? createMut.mutate(input) : updateMut.mutate({ ...input, name: source?.name ?? input.name }))}
        onCancel={backToList}
        onDuplicate={readOnly && canWrite ? () => navigate(`${base}/new?from=${sourceID}`) : undefined}
      />

    </PageContainer>
  )
}
