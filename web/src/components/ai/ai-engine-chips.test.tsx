import { describe, it, expect } from 'vitest'
import { aiEngineChips } from './ai-engine-chips'
import type { AiPrompt, AIVendorStatus } from '../../api'

const prompt = (id: number, flags: Partial<Pick<AiPrompt, 'out_status' | 'out_text' | 'out_note' | 'out_comments'>>): AiPrompt =>
  ({ id, out_status: false, out_text: false, out_note: false, out_comments: false, ...flags }) as AiPrompt
const vendor = (provider: string, tokenIds: number[]): AIVendorStatus =>
  ({ provider, connected: true, tokens: tokenIds.map(id => ({ id, name: 't', key_last4: '1234', connection_id: 1, use_for_chat: false })), connections: [], models: [] }) as unknown as AIVendorStatus

describe('aiEngineChips', () => {
  const promptById = new Map([[5, prompt(5, { out_status: true, out_text: true })], [6, prompt(6, { out_text: true })]])
  const vendorByToken = new Map<number, AIVendorStatus>([[1, vendor('openai', [1])], [2, vendor('anthropic', [2])]])

  // An extension row has no prompt and no model of its own: the chip names the
  // extension by its display name, with the mode its capabilities give it.
  it('an extension row is the extension by name, with no model', () => {
    const ext = {
      ...vendor('ext:mockqe', [3]), display_name: 'Mock QE',
      extension: { id: 'mockqe', version: '1.0.0', capabilities: ['qe'], settings: [], allowed_hosts: [] },
    } as AIVendorStatus
    const byToken = new Map([...vendorByToken, [3, ext]])
    expect(aiEngineChips({ rows: [{ source_lang: '*', target_langs: ['*'], system_prompt_id: null, token_id: 3, model: 'mockqe' }] }, promptById, byToken))
      .toEqual([{ key: 'ext:mockqe||QE', provider: 'ext:mockqe', name: 'Mock QE', model: null, mode: 'QE' }])
  })

  // The key was deleted or the extension removed: the row still says which
  // extension it runs, from its model (the extension id).
  it('an extension row with no known key falls back to its extension id', () => {
    expect(aiEngineChips({ rows: [{ source_lang: '*', target_langs: ['*'], system_prompt_id: null, token_id: 99, model: 'mockqe' }] }, promptById, vendorByToken))
      .toEqual([{ key: 'ext:mockqe||QE+APE', provider: 'ext:mockqe', name: 'mockqe', model: null, mode: 'QE+APE' }])
  })

  it('an LLM row shows its token provider, model and the prompt mode; duplicates collapse', () => {
    const rows = [
      { source_lang: 'en', target_langs: ['fr'], system_prompt_id: 5, token_id: 1, model: 'gpt-4o' },
      { source_lang: 'en', target_langs: ['de'], system_prompt_id: 5, token_id: 1, model: 'gpt-4o' },
      { source_lang: 'en', target_langs: ['es'], system_prompt_id: 6, token_id: 2, model: 'claude-opus-5' },
    ]
    expect(aiEngineChips({ rows }, promptById, vendorByToken).map(c => `${c.provider} ${c.model} ${c.mode}`))
      .toEqual(['openai gpt-4o QE+APE', 'anthropic claude-opus-5 MT'])
  })

  it('skips a row whose prompt is unknown rather than inventing a mode', () => {
    expect(aiEngineChips({ rows: [{ source_lang: 'en', target_langs: ['fr'], system_prompt_id: 99, token_id: 1, model: 'x' }] }, promptById, vendorByToken)).toEqual([])
  })
})
