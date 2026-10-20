// Built-in AI chat. The send path is NOT `request()`: the response
// is an SSE-formatted stream on a POST body (EventSource is GET-only), so we
// read it with fetch + ReadableStream and surface parsed events via callbacks.
import { request, getToken, ApiError, BASE } from './request';
import { fetchBlob } from './download';

export interface ChatConversation {
  id: number;
  title: string;
  created_at: string;
  updated_at: string;
}

export interface ChatToolCall {
  name: string;
  ok: boolean;
}

export interface ChatMessage {
  id: number;
  conversation_id: number;
  role: 'user' | 'assistant';
  content: string;
  tool_calls: ChatToolCall[] | null;
  created_at: string;
}

export const listChatConversations = () =>
  request<{ data: ChatConversation[] }>('/chat/conversations').then(r => r.data);

export const listChatMessages = (conversationId: number) =>
  request<{ data: ChatMessage[] }>(`/chat/conversations/${conversationId}/messages`).then(r => r.data);

export const deleteChatConversation = (conversationId: number) =>
  request<void>(`/chat/conversations/${conversationId}`, { method: 'DELETE' });

/** Payload of a client_action: navigate, render_chart, highlight or tour. */
export interface ClientActionPayload {
  path?: string;
  chart?: ChartSpec;
  /** highlight: the data-guide id and the caption next to it. */
  anchor?: string;
  caption?: string;
  /** tour: the steps, each with an optional route to open first. */
  steps?: TourStep[];
  /** suggest_replies: one-click answers shown under the message. */
  replies?: string[];
  /** playbook_tour: run this playbook's saved tour (steps from listPlaybooks). */
  playbook?: string;
  /** ask_screenshot, ask_repeat: why the assistant asks, shown by the button. */
  reason?: string;
}

export interface TourStep {
  path?: string;
  anchor: string;
  title: string;
  body: string;
  /** What the voice reads instead of body, when tours are read aloud. */
  say?: string;
}

/**
 * A playbook for the caller's role. `steps` is its saved
 * tour, validated and routed server-side, which the browser runs without the
 * assistant; empty steps mean the assistant runs it.
 */
export interface PlaybookInfo {
  name: string;
  title: string;
  description: string;
  steps: TourStep[];
  /** A saved tour exists, even if this org has nothing for its steps. */
  has_tour: boolean;
}

export const listPlaybooks = () =>
  request<{ data: PlaybookInfo[] }>('/chat/playbooks').then(r => r.data);

/**
 * A guarded tool's preview: nothing has changed yet. The
 * user confirms it with confirmChatAction; the model never sees the token.
 */
export interface ChatConfirmRequest {
  tool: string;
  args: Record<string, unknown>;
  summary: string;
  effects?: string[];
  token: string;
  expires_at: string;
  /** An issue report: proves the assistant's check when the form sends the report. */
  verification_token?: string;
}

/** A file attached in the assistant panel; import tools take its id. */
export interface ChatUpload {
  id: string;
  filename: string;
  size_bytes: number;
  expires_at: string;
}

export async function uploadChatFile(file: File): Promise<ChatUpload> {
  const form = new FormData();
  form.append('file', file);
  const headers: Record<string, string> = {};
  const token = getToken();
  if (token && token !== '__cookie_auth__') headers['Authorization'] = `Bearer ${token}`;
  const res = await fetch(`${BASE}/chat/uploads`, { method: 'POST', credentials: 'same-origin', headers, body: form });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(res.status, errorMessageFromBody(body, res.statusText), body);
  return (body as { data: ChatUpload }).data;
}

/** A screenshot the user sent the assistant, read back as a file for the report form. */
export async function fetchChatScreenshot(uploadId: string): Promise<File> {
  const blob = await fetchBlob(`/chat/uploads/${encodeURIComponent(uploadId)}`)
  return new File([blob], `screenshot-${uploadId.slice(0, 8)}.${blob.type === 'image/png' ? 'png' : 'jpg'}`, { type: blob.type })
}

/** Whether tour cards can be read aloud (the org has an OpenAI key). */
export const getTourSpeechStatus = () =>
  request<{ data: { available: boolean } }>('/chat/speech').then(r => r.data.available);

/** One tour step read aloud, as MP3: its say line, else its body. */
export async function fetchTourSpeech(body: string, say: string | undefined, signal?: AbortSignal): Promise<Blob> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  const token = getToken();
  if (token && token !== '__cookie_auth__') headers['Authorization'] = `Bearer ${token}`;
  const res = await fetch(`${BASE}/chat/speech`, {
    method: 'POST', credentials: 'same-origin', headers, body: JSON.stringify({ body, say: say ?? '' }), signal,
  });
  if (!res.ok) {
    const err = await res.json().catch(() => ({}));
    throw new ApiError(res.status, errorMessageFromBody(err, res.statusText), err);
  }
  return res.blob();
}

/** The line a message carries per attachment, so the model knows the id. */
export const attachmentNote = (u: Pick<ChatUpload, 'id' | 'filename'>) =>
  `[Attached file: ${u.filename} (upload_id: ${u.id})]`;

/** The user sent the assistant's issue report from the form: the conversation records it. */
export const recordChatIssueReported = (conversation_id: number, issue_id: number) =>
  request<{ data: { ok: boolean; message: string } }>('/chat/issue-reported', {
    method: 'POST',
    body: JSON.stringify({ conversation_id, issue_id }),
  }).then(r => r.data)

export const confirmChatAction = (body: {
  conversation_id: number;
  tool: string;
  args: Record<string, unknown>;
  confirm_token: string;
}) =>
  request<{ data: { ok: boolean; message: string } }>('/chat/confirm', {
    method: 'POST',
    body: JSON.stringify(body),
  }).then(r => r.data);

export interface ChatStreamHandlers {
  onText: (delta: string) => void;
  onToolStart: (tool: string) => void;
  onToolEnd: (tool: string, ok: boolean) => void;
  onDone: (info: { conversation_id: number }) => void;
  onError: (message: string) => void;
  /**
   * The model asked the browser to act — {action: "navigate",
   * path} or {action: "render_chart", chart}. Paths are
   * composed server-side against a route whitelist; chart specs are validated
   * server-side. Fire-and-forget.
   */
  onClientAction?: (action: string, payload: ClientActionPayload) => void;
  /** A guarded tool needs the user's confirmation. */
  onConfirmRequired?: (req: ChatConfirmRequest) => void;
  /**
   * Fired on EVERY received chunk, including `: ping` keepalive comments that
   * carry no event. Liveness signal for the caller's stalled-stream watchdog
   * — "bytes are flowing" is the fact that matters, not
   * "an event parsed".
   */
  onAlive?: () => void;
}

/**
 * Pulls a human-readable message out of an error response body without ever
 * coercing an object into a string. The house envelope is
 * `{"error": {"code", "message"}}` (httputil.Error/ErrorCode); a flat
 * `{"error": "..."}` string is accepted too. Anything else → fallback. Never
 * stringifies an object, so "[object Object]" cannot reach users
 */
export function errorMessageFromBody(body: unknown, fallback: string): string {
  if (body && typeof body === 'object' && 'error' in body) {
    const e = (body as { error: unknown }).error;
    if (typeof e === 'string' && e) return e;
    if (e && typeof e === 'object' && 'message' in e) {
      const m = (e as { message: unknown }).message;
      if (typeof m === 'string' && m) return m;
    }
  }
  return fallback;
}

/** Thrown on HTTP 412: no API key is connected for the chat model's provider. */
export class NoClaudeKeyError extends Error {
  constructor() {
    super('no Claude API key configured');
    this.name = 'NoClaudeKeyError';
  }
}

/**
 * Sends one message and consumes the SSE stream. Resolves when the stream
 * ends (after done/error). The AbortSignal cancels generation server-side —
 * the backend's context dies with the connection.
 */
export async function sendChatMessage(
  body: {
    conversation_id?: number;
    message: string;
    model?: string;
    allow_writes?: boolean;
    page_path?: string;
    visible_anchors?: string[];
    /** The screenshot the user approved for this message. */
    screenshot_upload_id?: string;
  },
  handlers: ChatStreamHandlers,
  signal?: AbortSignal,
): Promise<void> {
  const token = getToken();
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if (token && token !== '__cookie_auth__') headers['Authorization'] = `Bearer ${token}`;

  const res = await fetch(`${BASE}/chat`, {
    method: 'POST',
    credentials: 'same-origin',
    headers,
    body: JSON.stringify(body),
    signal,
  });

  if (res.status === 412) throw new NoClaudeKeyError();
  if (!res.ok) {
    const err = await res.json().catch(() => ({}));
    throw new ApiError(res.status, errorMessageFromBody(err, res.statusText), err);
  }
  if (!res.body) throw new Error('streaming not supported');

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  let terminal = false;
  const wrapped: ChatStreamHandlers = {
    ...handlers,
    onDone: (info) => {
      terminal = true;
      handlers.onDone(info);
    },
    onError: (message) => {
      terminal = true;
      handlers.onError(message);
    },
  };
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      handlers.onAlive?.();
      buffer += decoder.decode(value, { stream: true });
      // SSE frames are blank-line separated; keep the trailing partial frame.
      const frames = buffer.split('\n\n');
      buffer = frames.pop() ?? '';
      for (const frame of frames) dispatchFrame(frame, wrapped);
    }
    if (buffer.trim()) dispatchFrame(buffer, wrapped);
    // A stream that ends without done/error was cut mid-flight (proxy idle
    // timeout, backend crash, network drop). Report it, or the panel hangs
    // with no explanation.
    if (!terminal) {
      handlers.onError('connection to the assistant was interrupted — try again');
    }
  } finally {
    reader.releaseLock();
  }
}

/** Exported for tests. Parses one "event: X\ndata: {...}" frame. */
export function dispatchFrame(frame: string, handlers: ChatStreamHandlers): void {
  let event = '';
  let data = '';
  for (const line of frame.split('\n')) {
    if (line.startsWith('event: ')) event = line.slice(7);
    else if (line.startsWith('data: ')) data = line.slice(6);
  }
  if (!event || !data) return;
  let payload: Record<string, unknown>;
  try {
    payload = JSON.parse(data);
  } catch {
    return;
  }
  switch (event) {
    case 'text':
      handlers.onText((payload.text as string) ?? '');
      break;
    case 'tool_start':
      handlers.onToolStart((payload.tool as string) ?? '');
      break;
    case 'tool_end':
      handlers.onToolEnd((payload.tool as string) ?? '', Boolean(payload.ok));
      break;
    case 'done':
      handlers.onDone({ conversation_id: Number(payload.conversation_id) });
      break;
    case 'error':
      handlers.onError((payload.error as string) ?? 'something went wrong');
      break;
    case 'confirm_required':
      if (payload.confirm && typeof payload.confirm === 'object') {
        handlers.onConfirmRequired?.(payload.confirm as ChatConfirmRequest);
      }
      break;
    case 'client_action':
      handlers.onClientAction?.((payload.action as string) ?? '', {
        path: payload.path as string | undefined,
        chart: payload.chart as ChartSpec | undefined,
        anchor: payload.anchor as string | undefined,
        caption: payload.caption as string | undefined,
        steps: payload.steps as ClientActionPayload['steps'],
        replies: payload.replies as string[] | undefined,
        playbook: payload.playbook as string | undefined,
        reason: payload.reason as string | undefined,
      });
      break;
  }
}

/**
 * Chart spec emitted by the chat's render_chart client tool.
 * Validated server-side (type enum, 1-5 series,
 * ≤200 rows); the panel renders it verbatim with recharts.
 */
export interface ChartSpec {
  title?: string;
  type: 'line' | 'bar' | 'area' | 'pie';
  x_key: string;
  series: string[];
  /**
   * Dimension whose VALUES become separate series (e.g. `kind` → one band per
   * tm/mt/human). With it set, `series` names the single value column to read
   * and area/bar charts stack, because the bands are parts of one whole.
   */
  series_key?: string;
  /** Value columns that are averages: never stacked, summed or zero-filled. */
  averages?: string[];
  /** What each value column counts ("words", "minutes"), when known. */
  units?: Record<string, string>;
  /** Value columns that are sums of other value columns (total = input + output). */
  derived?: string[];
  data: Record<string, unknown>[];
}

export interface ChatModelInfo {
  provider: string;
  id: string;
  label: string;
  connected: boolean;
  selectable: boolean;
  default: boolean;
}

export const listChatModels = () =>
  request<{ data: ChatModelInfo[] }>('/chat/models').then(r => r.data);
