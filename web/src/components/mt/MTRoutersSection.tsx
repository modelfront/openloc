// MT routers: what a project points at for machine translation. A router
// maps target languages to MT engines — provider engines and LLM translation
// engines side by side; the shared RoutersSection renders it, this file only
// says which API and engines it uses. Manager surface end to end.
import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  listMTRouters, createMTRouter, updateMTRouter, deleteMTRouter, listMTEngines, listAIEngines,
  type MTRouter, type MTRouterInput,
} from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { combineMTEngines } from '../../lib/mt-engines'
import type { UnusableReason } from '../../api'
import { mtRouterToShape, mtShapeToInput } from '../../lib/mt-router-rows'
import { resKey } from '../AssignmentSection'
import { EngineUnusableChip, useAIEngineChips } from '../ai/ai-engine-chips'
import { RoutersSection, type RouterAdapter } from '../routers/RoutersSection'

export function MTRoutersSection({ canWrite }: { canWrite: boolean }) {
  const { data: mtEngines } = useQuery({ queryKey: queryKeys.mtEngines.all, queryFn: listMTEngines })
  const { data: aiEngines } = useQuery({ queryKey: queryKeys.aiEngines.all, queryFn: listAIEngines })
  const { metaFor } = useAIEngineChips()
  const adapter = useMemo<RouterAdapter<MTRouter, MTRouterInput>>(() => {
    const byId = new Map((aiEngines ?? []).map(e => [e.id, e]))
    return {
      noun: 'MT router',
      // No title: the Routers tab already names the list.
      testIdPrefix: 'mt-router',
      deleteDescription: 'Projects and templates that use this router must pick another one first.',
      queryKey: queryKeys.mtRouters.all,
      list: listMTRouters,
      create: createMTRouter,
      update: updateMTRouter,
      remove: deleteMTRouter,
      // Project and template settings show the router's name.
      invalidateKeys: [queryKeys.mtRouters.all, queryKeys.projects.all, queryKeys.templates.all],
      // Provider engines plus LLM translation engines.
      // Unusable ones come through carrying their reason: the picker hides them
      // from new picks, and a rule already pointing at one shows the warning
      // instead of rendering blank.
      engineOptions: combineMTEngines(mtEngines, aiEngines).map(e => {
        const sys = e.kind === 'ai' ? byId.get(e.id) : undefined
        return {
          value: resKey(e.kind, e.id),
          label: e.name,
          unusable: e.unusable,
          chip: e.unusable
            ? <EngineUnusableChip reason={e.unusable as UnusableReason} />
            : sys ? metaFor(sys) : undefined,
        }
      }),
      emptyEngineText: 'No MT engines yet — connect one in AI settings or create an LLM engine on the Engines tab.',
      toShape: mtRouterToShape,
      fromShape: mtShapeToInput,
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mtEngines, aiEngines])
  return <RoutersSection canWrite={canWrite} adapter={adapter} />
}
