import type { UnusableReason } from './ai-engines'
import { request } from './request'
import type { AiRouter } from '../types/generated'

// ─── AI routers ───
//
// A router is what a hybrid / ai workflow step points at: it maps target
// languages to QE+APE AI engines and may name a default engine for every
// other language. Source languages play no part. Manager-read, owner/admin-
// write, like engines.

export interface AIRouterRule {
  id?: number
  target_lang: string
  engine_id: number
}

export interface AIRouter extends AiRouter {
  /**
   * Why this router cannot fully translate, or '' when every engine it routes
   * to can. Computed server-side so a picker can warn without loading the
   * engine lists to work it out.
   */
  unusable: UnusableReason
  rules: AIRouterRule[]
}

export interface AIRouterInput {
  name: string
  default_engine_id: number | null
  rules: { target_lang: string; engine_id: number }[]
}

export const listAIRouters = () =>
  request<{ data: AIRouter[] }>('/ai/routers').then(r => r.data)

export const createAIRouter = (input: AIRouterInput) =>
  request<{ data: AIRouter }>('/ai/routers', { method: 'POST', body: JSON.stringify(input) }).then(r => r.data)

export const updateAIRouter = (id: number, input: AIRouterInput) =>
  request<{ data: AIRouter }>(`/ai/routers/${id}`, { method: 'PUT', body: JSON.stringify(input) }).then(r => r.data)

export const deleteAIRouter = (id: number) =>
  request<void>(`/ai/routers/${id}`, { method: 'DELETE' })

/** Every engine a router can send a job to: its default plus each rule's. */
export function routerEngineIDs(router: Pick<AIRouter, 'default_engine_id' | 'rules'>): number[] {
  const ids = new Set<number>()
  if (router.default_engine_id != null) ids.add(router.default_engine_id)
  for (const r of router.rules) ids.add(r.engine_id)
  return [...ids]
}
