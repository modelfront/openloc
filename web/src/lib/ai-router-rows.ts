// AI router ↔ the shared router shape (lib/router-rows.ts). An AI route points
// at one engine table, so the engine key is just the engine's id as a string.
import type { AIRouter, AIRouterInput } from '../api/ai-routers'
import type { RouterShape } from './router-rows'

export const aiEngineKey = (id: number) => String(id)

export function aiRouterToShape(router: Pick<AIRouter, 'default_engine_id' | 'rules'>): RouterShape {
  return {
    defaultEngine: router.default_engine_id != null ? aiEngineKey(router.default_engine_id) : null,
    rules: router.rules.map(r => ({ target_lang: r.target_lang, engine: aiEngineKey(r.engine_id) })),
  }
}

export function aiShapeToInput(name: string, shape: RouterShape): AIRouterInput {
  return {
    name: name.trim(),
    default_engine_id: shape.defaultEngine != null ? Number(shape.defaultEngine) : null,
    rules: shape.rules.map(r => ({ target_lang: r.target_lang, engine_id: Number(r.engine) })),
  }
}
