// MT router ↔ the shared router shape (lib/router-rows.ts). An MT route points
// at one of two engine tables, so the engine key carries the kind:
// "mt:<id>" for a classic engine, "ai:<id>" for an LLM translation engine
// (the same `resKey` scheme the assignment tables use).
import type { MTEngineRef, MTRouter, MTRouterInput } from '../api/mt-routers'
import { resKey, parseResKey } from '../components/AssignmentSection'
import type { RouterShape } from './router-rows'

export function mtEngineKey(ref: MTEngineRef): string | null {
  if (ref.ai_engine_id != null) return resKey('ai', ref.ai_engine_id)
  if (ref.mt_engine_id != null) return resKey('mt', ref.mt_engine_id)
  return null
}

export function mtEngineRef(key: string): MTEngineRef {
  const { kind, id } = parseResKey(key)
  return kind === 'ai' ? { ai_engine_id: id } : { mt_engine_id: id }
}

export function mtRouterToShape(router: Pick<MTRouter, 'default_mt_engine_id' | 'default_ai_engine_id' | 'rules'>): RouterShape {
  return {
    defaultEngine: mtEngineKey({ mt_engine_id: router.default_mt_engine_id, ai_engine_id: router.default_ai_engine_id }),
    rules: router.rules.map(r => ({ target_lang: r.target_lang, engine: mtEngineKey(r) ?? '' })),
  }
}

export function mtShapeToInput(name: string, shape: RouterShape): MTRouterInput {
  return {
    name: name.trim(),
    default: shape.defaultEngine != null ? mtEngineRef(shape.defaultEngine) : {},
    rules: shape.rules.map(r => ({ target_lang: r.target_lang, ...mtEngineRef(r.engine) })),
  }
}
