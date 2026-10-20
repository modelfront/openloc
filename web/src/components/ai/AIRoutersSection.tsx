// AI routers: what a hybrid / ai workflow step points at.
// A router maps target languages to QE+APE engines; the shared RoutersSection
// renders it, this file only says which API and engines it uses. Manager-
// visible; write actions gated to owner/admin via `canWrite`.
import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  listAIRouters, createAIRouter, updateAIRouter, deleteAIRouter, listAIEngines,
  type AIRouter, type AIRouterInput,
} from '../../api'
import { Plus } from 'lucide-react'
import { queryKeys } from '../../lib/query-keys'
import { aiEngineKey, aiRouterToShape, aiShapeToInput } from '../../lib/ai-router-rows'
import { EngineUnusableChip, useAIEngineChips } from './ai-engine-chips'
import { RoutersSection, type RouterAdapter } from '../routers/RoutersSection'
import { useEngineCreateActions } from '../../hooks/useEngineCreateActions'

export function AIRoutersSection({ canWrite, createOnMount, onCreateClosed }: {
  canWrite: boolean
  createOnMount?: boolean
  onCreateClosed?: () => void
}) {
  const { data: allEngines } = useQuery({ queryKey: queryKeys.aiEngines.all, queryFn: listAIEngines })
  const { metaFor } = useAIEngineChips()

  // Building a router is when you notice the engine you need does not exist
  // yet, so creating one is pinned in the picker — same machinery as every
  // other "New …" entry: a new tab onto the create form, lists refreshed on
  // the way back.
  const { aiEngine } = useEngineCreateActions()

  const adapter = useMemo<RouterAdapter<AIRouter, AIRouterInput>>(() => ({
    noun: 'AI router',
    // No title: the Routers tab already names the list.
    testIdPrefix: 'ai-router',
    deleteDescription: 'Workflow steps that use this router must be removed first.',
    queryKey: queryKeys.aiRouters.all,
    list: listAIRouters,
    create: createAIRouter,
    update: updateAIRouter,
    remove: deleteAIRouter,
    // Step lists (library, workflows) show the router's name, so a rename or
    // delete stales them too.
    invalidateKeys: [queryKeys.aiRouters.all, queryKeys.stepDefinitions.all, queryKeys.workflows.all],
    // Only qe_ape-purpose engines review, so only they can be routed to.
    // Unusable engines come through carrying their reason, so the picker can
    // hide them from new picks while a rule already pointing at one keeps
    // showing it, flagged.
    engineOptions: (allEngines ?? []).filter(e => e.purpose === 'qe_ape').map(e => ({
      value: aiEngineKey(e.id),
      label: e.name,
      unusable: e.unusable,
      chip: e.unusable ? <EngineUnusableChip reason={e.unusable} /> : metaFor(e),
    })),
    engineAction: { ...aiEngine(), icon: <Plus /> },
    emptyEngineText: 'No QE+APE engines yet — create one on the Engines tab.',
    toShape: aiRouterToShape,
    fromShape: aiShapeToInput,
  }), [allEngines, aiEngine, metaFor])
  return (
    <RoutersSection
      canWrite={canWrite}
      adapter={adapter}
      createOnMount={createOnMount}
      onCreateClosed={onCreateClosed}
    />
  )
}
