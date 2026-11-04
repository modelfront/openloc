import { describe, it, expect, vi, afterEach } from 'vitest'
import {
  dispatchFrame,
  errorMessageFromBody,
  fetchTourSpeech,
  sendChatMessage,
  uploadChatFile,
  type ChatStreamHandlers,
} from './chat'

afterEach(() => vi.unstubAllGlobals())

/** Stubs fetch with its answer: a response, or the error fetch rejects with. */
function stubFetch(answer: Response | Error) {
  vi.stubGlobal('fetch', answer instanceof Response ? vi.fn().mockResolvedValue(answer) : vi.fn().mockRejectedValue(answer))
}

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
  /** Sends one message, with fetch giving answer. */
  function send(answer: Response | Error, h: ChatStreamHandlers): Promise<void> {
    stubFetch(answer)
    return sendChatMessage({ message: 'q' }, h)
  }

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

  // A body whose read() rejects after the chunks, the way Firefox reports a
  // connection that breaks mid-response ("Error in input stream").
  function brokenStreamResponse(chunks: string[], failure: Error): Response {
    const encoder = new TextEncoder()
    let i = 0
    const body = new ReadableStream<Uint8Array>({
      pull(controller) {
        if (i < chunks.length) controller.enqueue(encoder.encode(chunks[i++]))
        else controller.error(failure)
      },
    })
    return new Response(body, { status: 200 })
  }

  it('fires onAlive for keepalive comments and surfaces a cut stream as an error', async () => {
    const h = { ...handlers(), onAlive: vi.fn() }
    // Ends WITHOUT done/error — the shape of a stream cut by the load balancer.
    await send(streamResponse([': ping\n\n', 'event: text\ndata: {"text":"partial"}\n\n']), h)
    expect(h.onAlive).toHaveBeenCalledTimes(2) // one call per received chunk, the ping included
    expect(h.onText).toHaveBeenCalledWith('partial')
    expect(h.onError).toHaveBeenCalledWith(expect.stringContaining('interrupted'), { interrupted: true })
  })

  it('reports a stream that breaks mid-read as an interruption, not the browser text', async () => {
    const h = handlers()
    await send(brokenStreamResponse(['event: text\ndata: {"text":"partial"}\n\n'], new TypeError('Error in input stream')), h)
    expect(h.onText).toHaveBeenCalledWith('partial')
    expect(h.onError).toHaveBeenCalledWith(expect.stringContaining('interrupted'), { interrupted: true })
    expect(h.onError).not.toHaveBeenCalledWith(expect.stringContaining('input stream'))
  })

  it('a stream the user stops mid-read rejects with the abort, not an interruption', async () => {
    const { sendChatMessage } = await import('./chat')
    const ctrl = new AbortController()
    let sent = false
    const body = new ReadableStream<Uint8Array>({
      pull(controller) {
        if (sent) {
          ctrl.abort()
          controller.error(new DOMException('The operation was aborted.', 'AbortError'))
          return
        }
        sent = true
        controller.enqueue(new TextEncoder().encode('event: text\ndata: {"text":"partial"}\n\n'))
      },
    })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(body, { status: 200 })))
    const h = {
      onText: vi.fn(), onToolStart: vi.fn(), onToolEnd: vi.fn(),
      onDone: vi.fn(), onError: vi.fn(),
    }
    try {
      await expect(sendChatMessage({ message: 'q' }, h, ctrl.signal)).rejects.toMatchObject({ name: 'AbortError' })
    } finally {
      vi.unstubAllGlobals()
    }
    expect(h.onError).not.toHaveBeenCalled()
  })

  it('does NOT report an interruption when the stream ends with done', async () => {
    const h = handlers()
    await send(
      streamResponse(['event: text\ndata: {"text":"hi"}\n\n', 'event: done\ndata: {"conversation_id":1}\n\n']),
      h,
    )
    expect(h.onDone).toHaveBeenCalledWith({ conversation_id: 1 })
    expect(h.onError).not.toHaveBeenCalled()
  })

  it('stays quiet when the connection breaks after done', async () => {
    const h = handlers()
    await expect(send(
      brokenStreamResponse(['event: done\ndata: {"conversation_id":1}\n\n'], new TypeError('Error in input stream')),
      h,
    )).resolves.toBeUndefined()
    expect(h.onDone).toHaveBeenCalledWith({ conversation_id: 1 })
    expect(h.onError).not.toHaveBeenCalled()
  })

  it('reports a request that gets no answer as unreachable, with no Try again, not as the browser error', async () => {
    const h = handlers()
    await expect(send(new TypeError('NetworkError when attempting to fetch resource.'), h)).resolves.toBeUndefined()
    expect(h.onError).toHaveBeenCalledTimes(1)
    // One argument only: no `interrupted`, so the panel offers no Try again.
    expect(h.onError).toHaveBeenCalledWith('The assistant could not be reached. Send the message again.')
  })

  it('a request the user stops before the answer starts rejects with the abort', async () => {
    const ctrl = new AbortController()
    ctrl.abort()
    stubFetch(new DOMException('The operation was aborted.', 'AbortError'))
    const h = handlers()
    await expect(sendChatMessage({ message: 'q' }, h, ctrl.signal)).rejects.toMatchObject({ name: 'AbortError' })
    expect(h.onError).not.toHaveBeenCalled()
  })
})

describe('chat requests answered by an error page', () => {
  const SERVER_ERROR = 'The server could not answer. Try again.'

  // A load balancer's 502: an HTML body and, over HTTP/2, no status text.
  it.each([
    ['sendChatMessage', () => sendChatMessage({ message: 'q' }, handlers())],
    ['uploadChatFile', () => uploadChatFile(new File(['x'], 'notes.txt'))],
    ['fetchTourSpeech', () => fetchTourSpeech('Step body', undefined)],
  ])('%s rejects with a full-sentence message', async (_name, call) => {
    stubFetch(new Response('<html>502 Bad Gateway</html>', { status: 502 }))
    await expect(call()).rejects.toMatchObject({ status: 502, message: SERVER_ERROR })
  })

  it('does not show the bare status text of an HTTP/1.1 error page', async () => {
    stubFetch(new Response('<html>502 Bad Gateway</html>', { status: 502, statusText: 'Bad Gateway' }))
    await expect(sendChatMessage({ message: 'q' }, handlers())).rejects.toMatchObject({ status: 502, message: SERVER_ERROR })
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

  it('routes start with the conversation id as a number', () => {
    const h = { ...handlers(), onStart: vi.fn() }
    dispatchFrame('event: start\ndata: {"conversation_id":42}', h)
    expect(h.onStart).toHaveBeenCalledWith({ conversation_id: 42 })
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
