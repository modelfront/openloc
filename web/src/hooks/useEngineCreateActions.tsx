import { useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { queryKeys } from '../lib/query-keys'
import { useRefreshOnReturn } from './useRefreshOnReturn'
import type { ComboboxAction } from '@/components/ui/combobox'

// Pinned "New …" entries for every picker that points at something created
// elsewhere: a project's MT router, a workflow step's AI router, a router's
// engine rows, an engine's system prompt. Each opens that thing's CREATE form
// in a NEW TAB — the user is usually mid-way through a form of their own, and
// losing that draft to a navigation would be worse than the extra tab.
//
// Most paths end in /new so the tab lands on the form rather than on a list
// with a button to find — a picker's whole point is that the author already
// knows what they want to make. The MT engine and MT router targets have no
// /new route yet, so those two land on their list page.
//
// Returning from that tab refreshes the lists (useRefreshOnReturn), so the
// engine just created shows up in the still-open form.

const ENGINES_PATH = '/mt/engines'
const MT_ROUTERS_PATH = '/mt'
const AI_ENGINES_PATH = '/ai/engines/new'
const AI_ROUTERS_PATH = '/ai/new'
const STEP_LIBRARY_PATH = '/workflows/steps/new'

export function useEngineCreateActions() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const refreshOnReturn = useRefreshOnReturn(() => {
    queryClient.invalidateQueries({ queryKey: queryKeys.mtEngines.all })
    queryClient.invalidateQueries({ queryKey: queryKeys.aiEngines.all })
    queryClient.invalidateQueries({ queryKey: queryKeys.aiRouters.all })
    queryClient.invalidateQueries({ queryKey: queryKeys.mtRouters.all })
    queryClient.invalidateQueries({ queryKey: queryKeys.aiPrompts.all })
    queryClient.invalidateQueries({ queryKey: queryKeys.stepDefinitions.all })
  })

  const open = useCallback((path: string) => {
    // No 'noopener'/'noreferrer': per spec they suppress the handle, so
    // window.open returns null even on success — which we'd misread as
    // "blocked" and navigate away, dumping the user's in-progress form.
    // The target is our own origin, so opener access is
    // ours anyway; a null return means the popup was blocked.
    const tab = window.open(path, '_blank')
    if (!tab) {
      // Popup blocked — navigating in place beats a dead menu entry, even
      // though it costs the user any unsaved form state.
      navigate(path)
      return
    }
    refreshOnReturn()
  }, [navigate, refreshOnReturn])

  const mtEngine = useCallback(
    (label = 'New MT engine'): ComboboxAction => ({
      key: 'new-mt-engine',
      label,
      onSelect: () => open(ENGINES_PATH),
    }),
    [open],
  )

  const aiEngine = useCallback(
    (label = 'New AI engine'): ComboboxAction => ({
      key: 'new-ai-engine',
      label,
      onSelect: () => open(AI_ENGINES_PATH),
    }),
    [open],
  )

  const aiRouter = useCallback(
    (label = 'New AI router'): ComboboxAction => ({
      key: 'new-ai-router',
      label,
      onSelect: () => open(AI_ROUTERS_PATH),
    }),
    [open],
  )

  const mtRouter = useCallback(
    (label = 'New MT router'): ComboboxAction => ({
      key: 'new-mt-router',
      label,
      onSelect: () => open(MT_ROUTERS_PATH),
    }),
    [open],
  )

  // A prompt belongs to the section its output flags put it in, so the caller
  // says which — the other section's list would filter it straight back out.
  const prompt = useCallback(
    (purpose: 'mt' | 'qe_ape', label = 'New prompt'): ComboboxAction => ({
      key: 'new-prompt',
      label,
      onSelect: () => open(purpose === 'mt' ? '/mt/prompts/new' : '/ai/prompts/new'),
    }),
    [open],
  )

  const stepDefinition = useCallback(
    (label = 'New step'): ComboboxAction => ({
      key: 'new-step-definition',
      label,
      onSelect: () => open(STEP_LIBRARY_PATH),
    }),
    [open],
  )

  return { mtEngine, aiEngine, aiRouter, mtRouter, prompt, stepDefinition }
}
