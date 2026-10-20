import { request } from './request';
import type { Entry } from '@/types/llmdebug';

// ─── Dev-only LLM/extension debug feed ───
//
// GET /api/debug/llm/calls streams every outbound provider call (OpenAI/Claude
// MT + QE+APE, extension calls) captured by the in-memory ring on
// the backend. The route only exists when the server runs with
// DEV_AUTO_LOGIN=true — production answers 404 (ApiError.status === 404).

export type LLMDebugEntry = Entry;

export interface LLMDebugCallsResponse {
  entries: LLMDebugEntry[];
  last_seq: number;
}

/** Cursor poll: returns entries with seq > after, ascending. */
export const listLLMDebugCalls = (after: number) =>
  request<{ data: LLMDebugCallsResponse }>(`/debug/llm/calls?after=${after}`).then(r => r.data);
