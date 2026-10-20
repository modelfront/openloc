import { describe, it, expect, vi } from 'vitest'
import { dispatchFrame, errorMessageFromBody, type ChatStreamHandlers } from './chat'

describe('errorMessageFromBody', () => {
  it('extracts the message from the house envelope {error:{code,message}}', () => {
    // Pins that the object envelope yields its message,
    // never "[object Object]".
    expect(
      errorMessageFromBody({ error: { code: 'x', message: 'failed to load AI provider key' } }, 'fallback'),
    ).toBe('failed to load AI provider key')
  })

  it('accepts a flat string error', () => {
    expect(errorMessageFromBody({ error: 'plain message' }, 'fallback')).toBe('plain message')
  })

  it('falls back for garbage without ever stringifying an object', () => {
    expect(errorMessageFromBody({ error: { weird: true } }, 'fallback')).toBe('fallback')
    expect(errorMessageFromBody(null, 'fallback')).toBe('fallback')
    expect(errorMessageFromBody('not even an object', 'fallback')).toBe('fallback')
    expect(errorMessageFromBody({ error: 42 }, 'fallback')).toBe('fallback')
  })
})

function handlers() {
  return {
    onText: vi.fn(),
    onToolStart: vi.fn(),
    onToolEnd: vi.fn(),
    onDone: vi.fn(),
    onError: vi.fn(),
  } satisfies ChatStreamHandlers
}

describe('sendChatMessage stream handling', () => {
  function streamResponse(chunks: string[]): Response {
    const encoder = new TextEncoder()
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        for (const c of chunks) controller.enqueue(encoder.encode(c))
        controller.close()
      },
    })
    return new Response(body, { status: 200 })
  }

  it('fires onAlive for keepalive comments and surfaces a cut stream as an error', async () => {
    const { sendChatMessage } = await import('./chat')
    // Ends WITHOUT done/error — the shape of a stream cut by the load balancer.
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      streamResponse([': ping\n\n', 'event: text\ndata: {"text":"partial"}\n\n']),
    ))
    const h = {
      onText: vi.fn(), onToolStart: vi.fn(), onToolEnd: vi.fn(),
      onDone: vi.fn(), onError: vi.fn(), onAlive: vi.fn(),
    }
    try {
      await sendChatMessage({ message: 'q' }, h)
    } finally {
      vi.unstubAllGlobals()
    }
    expect(h.onAlive).toHaveBeenCalledTimes(2) // one call per received chunk, the ping included
    expect(h.onText).toHaveBeenCalledWith('partial')
    expect(h.onError).toHaveBeenCalledWith(expect.stringContaining('interrupted'))
  })

  it('does NOT report an interruption when the stream ends with done', async () => {
    const { sendChatMessage } = await import('./chat')
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      streamResponse(['event: text\ndata: {"text":"hi"}\n\n', 'event: done\ndata: {"conversation_id":1}\n\n']),
    ))
    const h = {
      onText: vi.fn(), onToolStart: vi.fn(), onToolEnd: vi.fn(),
      onDone: vi.fn(), onError: vi.fn(),
    }
    try {
      await sendChatMessage({ message: 'q' }, h)
    } finally {
      vi.unstubAllGlobals()
    }
    expect(h.onDone).toHaveBeenCalledWith({ conversation_id: 1 })
    expect(h.onError).not.toHaveBeenCalled()
  })
})

describe('dispatchFrame', () => {
  it('routes text deltas', () => {
    const h = handlers()
    dispatchFrame('event: text\ndata: {"type":"text","text":"Hel"}', h)
    expect(h.onText).toHaveBeenCalledWith('Hel')
  })

  it('routes confirm_required with the full preview payload', () => {
    const onConfirmRequired = vi.fn()
    const confirm = {
      tool: 'openloc_delete_custom_palette',
      args: { id: 'ocean' },
      summary: 'Delete your custom palette "Ocean".',
      token: '123.abc',
      expires_at: '2026-09-24T12:10:00Z',
    }
    dispatchFrame(`event: confirm_required\ndata: ${JSON.stringify({ type: 'confirm_required', confirm })}`, {
      ...handlers(),
      onConfirmRequired,
    })
    expect(onConfirmRequired).toHaveBeenCalledWith(confirm)
  })

  it('routes tool lifecycle with ok flag', () => {
    const h = handlers()
    dispatchFrame('event: tool_start\ndata: {"tool":"openloc_list_jobs"}', h)
    dispatchFrame('event: tool_end\ndata: {"tool":"openloc_list_jobs","ok":true}', h)
    expect(h.onToolStart).toHaveBeenCalledWith('openloc_list_jobs')
    expect(h.onToolEnd).toHaveBeenCalledWith('openloc_list_jobs', true)
  })

  it('treats a missing ok as failure, not success', () => {
    const h = handlers()
    // The backend omits ok:false via omitempty — absence must read as failed.
    dispatchFrame('event: tool_end\ndata: {"tool":"openloc_get_job"}', h)
    expect(h.onToolEnd).toHaveBeenCalledWith('openloc_get_job', false)
  })

  it('routes done with the conversation id as a number', () => {
    const h = handlers()
    dispatchFrame('event: done\ndata: {"conversation_id":42,"input_tokens":100}', h)
    expect(h.onDone).toHaveBeenCalledWith({ conversation_id: 42 })
  })

  it('routes error frames', () => {
    const h = handlers()
    dispatchFrame('event: error\ndata: {"error":"your Claude API key was rejected"}', h)
    expect(h.onError).toHaveBeenCalledWith('your Claude API key was rejected')
  })

  it('routes a pause tool request with its reason', () => {
    const h = { ...handlers(), onClientAction: vi.fn() }
    dispatchFrame('event: client_action\ndata: {"type":"client_action","action":"ask_screenshot","reason":"To see the grid."}', h)
    expect(h.onClientAction).toHaveBeenCalledWith('ask_screenshot', expect.objectContaining({ reason: 'To see the grid.' }))
  })

  it('ignores malformed frames without throwing', () => {
    const h = handlers()
    dispatchFrame('event: text\ndata: {not json', h)
    dispatchFrame('data: {"text":"orphan data, no event"}', h)
    dispatchFrame('', h)
    expect(h.onText).not.toHaveBeenCalled()
    expect(h.onError).not.toHaveBeenCalled()
  })

  it('handles multi-line frame parsing order-independently', () => {
    const h = handlers()
    // data before event — SSE allows any field order within a frame.
    dispatchFrame('data: {"text":"hi"}\nevent: text', h)
    expect(h.onText).toHaveBeenCalledWith('hi')
  })
})
