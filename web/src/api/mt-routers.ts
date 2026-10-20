import type { UnusableReason } from './ai-engines'
import { request } from './request'
import type { MtRouter } from '../types/generated'

// ─── MT routers ───
//
// A router is what a project's (or template's) settings profile points at
// for machine translation: it maps target languages to MT engines — a classic
// engine (mt_engine_id) or an LLM translation engine (ai_engine_id), never
// both — and may name a default engine for every other language. Manager
// surface end to end, like MT engines.

export interface MTEngineRef {
  mt_engine_id?: number | null
  ai_engine_id?: number | null
}

export interface MTRouterRule extends MTEngineRef {
  id?: number
  target_lang: string
}

export interface MTRouter extends MtRouter {
  /**
   * Why this router cannot fully translate, or '' when every engine it routes
   * to can. Computed server-side so a picker can warn without loading the
   * engine lists to work it out.
   */
  unusable: UnusableReason
  rules: MTRouterRule[]
}

export interface MTRouterInput {
  name: string
  default: MTEngineRef
  rules: MTRouterRule[]
}

export const listMTRouters = () =>
  request<{ data: MTRouter[] }>('/mt-routers').then(r => r.data)

export const createMTRouter = (input: MTRouterInput) =>
  request<{ data: MTRouter }>('/mt-routers', { method: 'POST', body: JSON.stringify(input) }).then(r => r.data)

export const updateMTRouter = (id: number, input: MTRouterInput) =>
  request<{ data: MTRouter }>(`/mt-routers/${id}`, { method: 'PUT', body: JSON.stringify(input) }).then(r => r.data)

export const deleteMTRouter = (id: number) =>
  request<void>(`/mt-routers/${id}`, { method: 'DELETE' })
