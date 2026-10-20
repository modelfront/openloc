// MT router ↔ the shared router shape. An MT route points at one of two engine
// tables, so every case here is really "does the kind survive the trip".
import { describe, it, expect } from 'vitest'
import { mtEngineKey, mtEngineRef, mtRouterToShape, mtShapeToInput } from './mt-router-rows'
import { shapeToRows, rowsToShape, ALL_LANGUAGES } from './router-rows'

describe('mtEngineKey / mtEngineRef', () => {
  it('keys a classic engine "mt:<id>" and an LLM engine "ai:<id>"', () => {
    expect(mtEngineKey({ mt_engine_id: 7 })).toBe('mt:7')
    expect(mtEngineKey({ ai_engine_id: 7 })).toBe('ai:7')
    // The same numeric id in the two tables must not collide.
    expect(mtEngineKey({ mt_engine_id: 7 })).not.toBe(mtEngineKey({ ai_engine_id: 7 }))
  })
  it('has no key for a ref that points at neither table', () => {
    expect(mtEngineKey({})).toBeNull()
    expect(mtEngineKey({ mt_engine_id: null, ai_engine_id: null })).toBeNull()
  })
  it('turns a key back into a ref that names one table only', () => {
    expect(mtEngineRef('mt:7')).toEqual({ mt_engine_id: 7 })
    expect(mtEngineRef('ai:7')).toEqual({ ai_engine_id: 7 })
  })
})

describe('mtRouterToShape', () => {
  it('carries both engine kinds through, default and rules alike', () => {
    expect(mtRouterToShape({
      default_mt_engine_id: undefined, default_ai_engine_id: 3,
      rules: [{ target_lang: 'de', mt_engine_id: 1 }, { target_lang: 'fr', ai_engine_id: 1 }],
    })).toEqual({
      defaultEngine: 'ai:3',
      rules: [{ target_lang: 'de', engine: 'mt:1' }, { target_lang: 'fr', engine: 'ai:1' }],
    })
  })
  it('has a null default when the router names none', () => {
    expect(mtRouterToShape({
      default_mt_engine_id: undefined, default_ai_engine_id: undefined,
      rules: [{ target_lang: 'de', mt_engine_id: 2 }],
    })).toEqual({ defaultEngine: null, rules: [{ target_lang: 'de', engine: 'mt:2' }] })
  })
  it('gives a rules-only router no "all languages" row', () => {
    const rows = shapeToRows(mtRouterToShape({
      default_mt_engine_id: undefined, default_ai_engine_id: undefined,
      rules: [{ target_lang: 'de', mt_engine_id: 2 }, { target_lang: 'it', mt_engine_id: 2 }],
    }))
    expect(rows).toEqual([{ langs: ['de', 'it'], engine: 'mt:2' }])
  })
})

describe('mtShapeToInput', () => {
  it('sends an empty default when no row covers all languages, and trims the name', () => {
    expect(mtShapeToInput(' R ', { defaultEngine: null, rules: [{ target_lang: 'de', engine: 'ai:4' }] })).toEqual({
      name: 'R', default: {}, rules: [{ target_lang: 'de', ai_engine_id: 4 }],
    })
  })
})

describe('round trip', () => {
  it('router → rows → input keeps every route on its own engine table', () => {
    const router = {
      default_mt_engine_id: 5, default_ai_engine_id: undefined,
      rules: [{ target_lang: 'de', mt_engine_id: 5 }, { target_lang: 'fr', ai_engine_id: 5 }],
    }
    const rows = shapeToRows(mtRouterToShape(router))
    // The default folds into its own engine's row; the AI engine with the same
    // id stays a separate row.
    expect(rows).toEqual([
      { langs: ['de', ALL_LANGUAGES], engine: 'mt:5' },
      { langs: ['fr'], engine: 'ai:5' },
    ])
    expect(mtShapeToInput('R', rowsToShape(rows))).toEqual({
      name: 'R',
      default: { mt_engine_id: 5 },
      rules: [{ target_lang: 'de', mt_engine_id: 5 }, { target_lang: 'fr', ai_engine_id: 5 }],
    })
  })
})
