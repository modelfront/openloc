import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Routes, Route, useLocation } from 'react-router-dom'
import { menuItem, menuText } from '@/test/menu'

function LocationProbe() {
  const location = useLocation()
  return <span data-testid="location-probe">{location.pathname}</span>
}

// Mock the api module BEFORE importing the component. The panel only uses
// sendChatMessage + NoClaudeKeyError from it.
vi.mock('../../api', async () => {
  class NoClaudeKeyError extends Error {
    constructor() {
      super('no Claude API key configured')
      this.name = 'NoClaudeKeyError'
    }
  }
  return {
    sendChatMessage: vi.fn(),
    listChatModels: vi.fn(),
    confirmChatAction: vi.fn(),
    listChatMessages: vi.fn(),
    listChatConversations: vi.fn(),
    uploadChatFile: vi.fn(),
    listPlaybooks: vi.fn(),
    getMe: vi.fn(async () => ({ id: 1, email: 'a@example.com', name: 'A', needs_org_selection: false, role: 'owner' })),
    listProjectsFilteredPage: vi.fn(),
    enterDemo: vi.fn(async () => ({ token: 't' })),
    getAppConfig: vi.fn(async () => ({ app_url: 'http://localhost', docs_url: '', docs_host: false, developer_url: '', developer_host: false })),
    ApiError: (await vi.importActual<typeof import('../../api/request')>('../../api/request')).ApiError,
    attachmentNote: (await vi.importActual<typeof import('../../api/chat')>('../../api/chat')).attachmentNote,
    NoClaudeKeyError,
  }
})

vi.mock('../../lib/guide', () => ({
  runGuide: vi.fn(),
  attachReplies: vi.fn(() => false),
  visibleAnchors: vi.fn(() => ['projects.new', 'nav.tasks']),
}))

vi.mock('../../lib/screenshot', () => ({
  capturePage: vi.fn(async () => new File(['jpeg'], 'screenshot.jpg', { type: 'image/jpeg' })),
}))

import { runGuide, attachReplies } from '../../lib/guide'
import { actionTrail, clearActionTrail, recordTrail } from '../../lib/action-trail'
import {
  sendChatMessage,
  listChatModels,
  confirmChatAction,
  listChatMessages,
  listChatConversations,
  uploadChatFile,
  listPlaybooks,
  listProjectsFilteredPage,
  NoClaudeKeyError,
  type ChatModelInfo,
} from '../../api'
import { ApiError } from '../../api/request'
import { ChatPanel } from './ChatPanel'
import { openChatWithPrompt, openChatWithHint } from '../../lib/chat-bus'
import { heldRecording } from '../../lib/watch-recording'
import { capturePage } from '../../lib/screenshot'
import { toast } from 'sonner'

const sendMock = vi.mocked(sendChatMessage)
const modelsMock = vi.mocked(listChatModels)
const confirmMock = vi.mocked(confirmChatAction)
const guideMock = vi.mocked(runGuide)
const attachMock = vi.mocked(attachReplies)
const messagesMock = vi.mocked(listChatMessages)
const conversationsMock = vi.mocked(listChatConversations)
const uploadMock = vi.mocked(uploadChatFile)
const playbooksMock = vi.mocked(listPlaybooks)
const projectsMock = vi.mocked(listProjectsFilteredPage)

const MODELS: ChatModelInfo[] = [
  { provider: 'claude', id: 'claude-sonnet-5', label: 'Sonnet 5', connected: true, selectable: true, default: true },
  { provider: 'claude', id: 'claude-opus-5', label: 'Opus 5', connected: true, selectable: true, default: false },
  { provider: 'openai', id: 'gpt-4o', label: 'GPT-4o', connected: false, selectable: false, default: false },
]

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <ChatPanel />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

function openPanel() {
  fireEvent.click(screen.getByTestId('chat-launcher'))
}

/**
 * Opens the panel and sends a message whose answer asks for a screenshot or a
 * recording; the test scripts the answers to later sends.
 */
function askAfterSending(action: 'ask_screenshot' | 'ask_repeat', reason: string, text?: string) {
  URL.createObjectURL = vi.fn(() => 'blob:shot')
  URL.revokeObjectURL = vi.fn()
  sendMock.mockImplementationOnce(async (_body, handlers) => {
    if (text) handlers.onText(text)
    handlers.onClientAction?.(action, { reason })
    handlers.onDone({ conversation_id: 9 })
  })
  renderPanel()
  openPanel()
  fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'it is broken' } })
  fireEvent.click(screen.getByTestId('chat-send'))
}

/** An ask card's buttons wait for the answer to finish, then render afresh: click the live one. */
async function clickWhenReady(testId: string) {
  await waitFor(() => {
    const el = screen.getByTestId(testId)
    expect(el).toBeEnabled()
    expect(el).not.toHaveAttribute('aria-disabled', 'true')
  })
  fireEvent.click(screen.getByTestId(testId))
}

beforeEach(() => {
  sendMock.mockReset()
  modelsMock.mockReset()
  confirmMock.mockReset()
  guideMock.mockReset()
  attachMock.mockReset()
  attachMock.mockReturnValue(false)
  messagesMock.mockReset()
  uploadMock.mockReset()
  conversationsMock.mockReset()
  conversationsMock.mockResolvedValue([])
  playbooksMock.mockReset()
  playbooksMock.mockResolvedValue([])
  projectsMock.mockReset()
  projectsMock.mockResolvedValue({ data: [], cursor: 0, has_more: false })
  modelsMock.mockResolvedValue(MODELS)
  // The resize tests persist panel size; a leaked value would make later
  // tests' panels open at whatever the previous test dragged to.
  localStorage.clear()
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('ChatPanel', () => {
  it('renders launcher only until opened, then the panel', () => {
    renderPanel()
    expect(screen.queryByTestId('chat-panel')).not.toBeInTheDocument()
    openPanel()
    expect(screen.getByTestId('chat-panel')).toBeInTheDocument()
    expect(screen.queryByTestId('chat-launcher')).not.toBeInTheDocument()
  })

  // The panel is never in the user's screenshot.
  it('marks the panel to be left out of screenshots', () => {
    renderPanel()
    openPanel()
    expect(screen.getByTestId('chat-panel')).toHaveAttribute('data-screenshot', 'exclude')
  })

  it('streams an answer: text deltas accumulate, tool chip appears', async () => {
    sendMock.mockImplementation(async (_body, handlers) => {
      handlers.onToolStart('openloc_list_jobs')
      handlers.onToolEnd('openloc_list_jobs', true)
      handlers.onText('Two jobs ')
      handlers.onText('are overdue.')
      handlers.onDone({ conversation_id: 7 })
    })
    renderPanel()
    openPanel()

    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'overdue?' } })
    fireEvent.click(screen.getByTestId('chat-send'))

    await waitFor(() => {
      expect(screen.getByText('Two jobs are overdue.')).toBeInTheDocument()
    })
    const chip = screen.getByTestId('chat-tool-chip')
    // Chip drops the openloc_ prefix for humans.
    expect(chip.textContent).toContain('list_jobs')
    // User bubble also present.
    expect(screen.getByText('overdue?')).toBeInTheDocument()
  })

  it('threads conversation_id into the second send', async () => {
    sendMock.mockImplementation(async (_body, handlers) => {
      handlers.onText('answer')
      handlers.onDone({ conversation_id: 99 })
    })
    renderPanel()
    openPanel()

    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'first' } })
    fireEvent.click(screen.getByTestId('chat-send'))
    await waitFor(() => expect(sendMock).toHaveBeenCalledTimes(1))
    expect(sendMock.mock.calls[0][0].conversation_id).toBeUndefined()

    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'second' } })
    fireEvent.click(screen.getByTestId('chat-send'))
    await waitFor(() => expect(sendMock).toHaveBeenCalledTimes(2))
    expect(sendMock.mock.calls[1][0].conversation_id).toBe(99)
  })

  it('new-conversation button clears history and drops the thread id', async () => {
    sendMock.mockImplementation(async (_body, handlers) => {
      handlers.onText('answer')
      handlers.onDone({ conversation_id: 5 })
    })
    renderPanel()
    openPanel()
    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'q' } })
    fireEvent.click(screen.getByTestId('chat-send'))
    await waitFor(() => expect(screen.getByText('answer')).toBeInTheDocument())

    fireEvent.click(screen.getByTestId('chat-new'))
    expect(screen.queryByText('answer')).not.toBeInTheDocument()

    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'fresh' } })
    fireEvent.click(screen.getByTestId('chat-send'))
    await waitFor(() => expect(sendMock).toHaveBeenCalledTimes(2))
    expect(sendMock.mock.calls[1][0].conversation_id).toBeUndefined()
  })

  it('shows the settings link when the tenant has no key for the AI provider, without naming one', async () => {
    sendMock.mockRejectedValue(new NoClaudeKeyError())
    renderPanel()
    openPanel()
    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'hello' } })
    fireEvent.click(screen.getByTestId('chat-send'))

    await waitFor(() => {
      expect(screen.getByText('Connect one in AI settings → Providers.')).toBeInTheDocument()
    })
    // The missing key belongs to the selected model's provider, which may not be Claude.
    expect(screen.getByText('No AI provider is connected for your organization.')).toBeInTheDocument()
    expect(screen.queryByText(/Claude/)).not.toBeInTheDocument()
    // Pins that the link points at /ai-settings, not only
    // the words.
    expect(screen.getByRole('link', { name: /AI settings/ })).toHaveAttribute('href', '/ai-settings')
    // The failed exchange must not linger as a half-rendered bubble.
    expect(screen.queryByText('hello')).not.toBeInTheDocument()
    // Input is disabled — nothing to chat with.
    expect(screen.getByTestId('chat-input')).toBeDisabled()
  })

  it('renders a stream error inline without losing the user message', async () => {
    sendMock.mockImplementation(async (_body, handlers) => {
      handlers.onError('Claude is rate-limiting requests — try again in a moment.')
    })
    renderPanel()
    openPanel()
    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'q' } })
    fireEvent.click(screen.getByTestId('chat-send'))

    await waitFor(() => {
      expect(screen.getByTestId('chat-error').textContent).toContain('rate-limiting')
    })
    expect(screen.getByText('q')).toBeInTheDocument()
  })

  it('send is inert for empty input and looks neutral, then turns primary with text', () => {
    renderPanel()
    openPanel()
    const btn = screen.getByTestId('chat-send')
    expect(btn).toHaveAttribute('aria-disabled', 'true')
    fireEvent.click(btn)
    expect(sendMock).not.toHaveBeenCalled()
    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'hi' } })
    expect(screen.getByTestId('chat-send')).toHaveAttribute('aria-disabled', 'false')
  })

  it('model picker: shows default, selects Opus, persists, and sends the model', async () => {
    sendMock.mockImplementation(async (_body, handlers) => {
      handlers.onText('ok')
      handlers.onDone({ conversation_id: 1 })
    })
    renderPanel()
    openPanel()

    // Defaults to the registry default once models load.
    const picker = screen.getByTestId('chat-model-picker')
    await waitFor(() => expect(picker.textContent).toContain('Sonnet 5'))

    // Open dropdown: OpenAI entry visible but disabled with its reason.
    fireEvent.click(picker)
    const gpt = await menuItem('chat-model-gpt-4o')
    expect(gpt).toHaveAttribute('aria-disabled', 'true')
    expect(gpt.textContent).toContain('no key')

    // The header title is the model label, not a generic "Assistant".
    await waitFor(() => expect(screen.getByTestId('chat-header-model').textContent).toBe('Sonnet 5'))

    // Select Opus 5 → button label updates, selection persisted, header follows.
    fireEvent.click(screen.getByTestId('chat-model-claude-opus-5'))
    await waitFor(() => expect(picker.textContent).toContain('Opus 5'))
    expect(screen.getByTestId('chat-header-model').textContent).toBe('Opus 5')
    expect(localStorage.getItem('openloc-chat-model')).toBe('claude-opus-5')

    // The chosen model rides the send body.
    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'q' } })
    fireEvent.click(screen.getByTestId('chat-send'))
    await waitFor(() => expect(sendMock).toHaveBeenCalledTimes(1))
    expect(sendMock.mock.calls[0][0].model).toBe('claude-opus-5')
  })

  it('model picker: stale stored selection snaps back to the default', async () => {
    localStorage.setItem('openloc-chat-model', 'claude-retired-model')
    renderPanel()
    openPanel()
    // The stored id isn't in the list → picker falls back to the default.
    await waitFor(() =>
      expect(screen.getByTestId('chat-model-picker').textContent).toContain('Sonnet 5'),
    )
  })

  // Both keys hold bare strings, not JSON; a JSON read would drop them.
  it('model picker: a stored bare model id is restored on mount', async () => {
    localStorage.setItem('openloc-chat-model', 'claude-opus-5')
    renderPanel()
    openPanel()
    await waitFor(() =>
      expect(screen.getByTestId('chat-model-picker').textContent).toContain('Opus 5'),
    )
    expect(localStorage.getItem('openloc-chat-model')).toBe('claude-opus-5')
  })

  it('mode picker: a stored bare `write` opens in write mode', () => {
    localStorage.setItem('openloc-chat-mode', 'write')
    renderPanel()
    openPanel()
    expect(screen.getByTestId('chat-mode-badge').textContent).toBe('write')
    expect(localStorage.getItem('openloc-chat-mode')).toBe('write')
  })

  it('sends the current page path and navigates on client_action', async () => {
    sendMock.mockImplementation(async (_body, handlers) => {
      handlers.onClientAction?.('navigate', { path: '/projects/42' })
      handlers.onText('Opened project 42.')
      handlers.onDone({ conversation_id: 1 })
    })
    // Probe element rendered by the router so navigation is observable.
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/tasks']}>
          <Routes>
            <Route path="*" element={<LocationProbe />} />
          </Routes>
          <ChatPanel />
        </MemoryRouter>
      </QueryClientProvider>,
    )
    openPanel()
    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'open alpha' } })
    fireEvent.click(screen.getByTestId('chat-send'))

    await waitFor(() => expect(screen.getByText('Opened project 42.')).toBeInTheDocument())
    // Context rode the send…
    expect(sendMock.mock.calls[0][0].page_path).toBe('/tasks')
    // …and the client action moved the router.
    expect(screen.getByTestId('location-probe').textContent).toBe('/projects/42')
  })

  describe('guarded tool confirmation', () => {
    const request = {
      tool: 'openloc_delete_custom_palette',
      args: { id: 'ocean' },
      summary: 'Delete your custom palette "Ocean".',
      effects: ['It is your active palette; the default palette takes its place.'],
      token: '123.abc',
      expires_at: '2026-09-24T12:10:00Z',
    }
    async function streamPreview() {
      sendMock.mockImplementation(async (_body, handlers) => {
        handlers.onConfirmRequired?.(request)
        handlers.onText('Confirm in the card to delete it.')
        handlers.onDone({ conversation_id: 9 })
      })
      renderPanel()
      openPanel()
      fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'delete ocean' } })
      fireEvent.click(screen.getByTestId('chat-send'))
      await waitFor(() => expect(screen.getByTestId('chat-confirm')).toHaveAttribute('data-state', 'pending'))
    }

    it('confirming in the dialog posts the token with the exact tool and args', async () => {
      confirmMock.mockResolvedValue({ ok: true, message: 'done' })
      await streamPreview()
      expect(screen.getByText('Needs your confirmation')).toBeInTheDocument()
      expect(confirmMock).not.toHaveBeenCalled()

      fireEvent.click(screen.getByTestId('chat-confirm-review'))
      expect(await screen.findByText(/the default palette takes its place/)).toBeInTheDocument()
      fireEvent.click(screen.getByRole('button', { name: 'Delete' }))

      await waitFor(() => expect(screen.getByTestId('chat-confirm')).toHaveAttribute('data-state', 'done'))
      expect(confirmMock).toHaveBeenCalledWith({
        conversation_id: 9,
        tool: 'openloc_delete_custom_palette',
        args: { id: 'ocean' },
        confirm_token: '123.abc',
      })
    })

    it('cancel never calls the server', async () => {
      await streamPreview()
      fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
      expect(screen.getByTestId('chat-confirm')).toHaveAttribute('data-state', 'cancelled')
      expect(screen.queryByTestId('chat-confirm-review')).not.toBeInTheDocument()
      expect(confirmMock).not.toHaveBeenCalled()
    })

    it('a refused confirm shows as failed', async () => {
      confirmMock.mockResolvedValue({ ok: false, message: 'The confirmation expired.' })
      await streamPreview()
      fireEvent.click(screen.getByTestId('chat-confirm-review'))
      fireEvent.click(await screen.findByRole('button', { name: 'Delete' }))
      await waitFor(() => expect(screen.getByTestId('chat-confirm')).toHaveAttribute('data-state', 'failed'))
    })
  })

  it('an attached file rides the next message as an upload_id note, then clears', async () => {
    uploadMock.mockResolvedValue({ id: 'u-1', filename: 'terms.tbx', size_bytes: 9, expires_at: '' })
    sendMock.mockResolvedValue(undefined)
    renderPanel()
    openPanel()

    fireEvent.change(screen.getByTestId('chat-attach-input'), {
      target: { files: [new File(['<martif/>'], 'terms.tbx')] },
    })
    await waitFor(() => expect(screen.getByTestId('chat-attachments')).toHaveTextContent('terms.tbx'))

    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'import these terms' } })
    fireEvent.click(screen.getByTestId('chat-send'))
    await waitFor(() => expect(sendMock).toHaveBeenCalled())
    expect(sendMock.mock.calls[0][0].message).toBe(
      'import these terms\n\n[Attached file: terms.tbx (upload_id: u-1)]',
    )
    expect(screen.queryByTestId('chat-attachments')).not.toBeInTheDocument()
  })

  it('a screenshot the assistant asks for is taken, shown, and sent with the next message only after Send', async () => {
    uploadMock.mockResolvedValue({ id: 'shot-1', filename: 'screenshot.jpg', size_bytes: 4, expires_at: '' })
    sendMock.mockResolvedValue(undefined)
    askAfterSending('ask_screenshot', 'So the team sees the empty grid.', 'Could you add a screenshot?')

    const card = await screen.findByTestId('chat-ask-screenshot')
    expect(card).toHaveTextContent('So the team sees the empty grid.')
    await clickWhenReady('chat-screenshot-take')
    expect(await screen.findByTestId('chat-screenshot-preview')).toHaveAttribute('src', 'blob:shot')
    expect(uploadMock).not.toHaveBeenCalled()

    fireEvent.click(screen.getByTestId('chat-screenshot-send'))
    await waitFor(() => expect(sendMock).toHaveBeenCalledTimes(2))
    expect(sendMock.mock.calls[1][0]).toMatchObject({
      conversation_id: 9, message: 'Here is the screenshot.', screenshot_upload_id: 'shot-1',
    })
  })

  it('a screenshot whose message got no answer can be sent again, without a second upload', async () => {
    uploadMock.mockResolvedValue({ id: 'shot-1', filename: 'screenshot.jpg', size_bytes: 4, expires_at: '' })
    askAfterSending('ask_screenshot', 'To see it.')
    sendMock.mockImplementationOnce(async (_body, handlers) => {
      handlers.onError('The AI provider is rate-limiting requests — try again in a moment.')
    }).mockImplementation(async (_body, handlers) => handlers.onDone({ conversation_id: 9 }))
    await clickWhenReady('chat-screenshot-take')
    fireEvent.click(await screen.findByTestId('chat-screenshot-send'))
    await waitFor(() => expect(screen.getByTestId('chat-screenshot-send')).toHaveTextContent('Send again'))
    await clickWhenReady('chat-screenshot-send')
    await waitFor(() => expect(sendMock).toHaveBeenCalledTimes(3))
    expect(sendMock.mock.calls[2][0].screenshot_upload_id).toBe('shot-1')
    expect(uploadMock).toHaveBeenCalledTimes(1)
    await waitFor(() => expect(screen.getByTestId('chat-ask-screenshot')).toHaveAttribute('data-state', 'sent'))
  })

  it('a screenshot still being taken when the user skips is dropped', async () => {
    let finish: (f: File) => void = () => {}
    vi.mocked(capturePage).mockImplementationOnce(() => new Promise<File>(resolve => { finish = resolve }))
    sendMock.mockResolvedValue(undefined)
    askAfterSending('ask_screenshot', 'To see it.')
    await clickWhenReady('chat-screenshot-take')
    fireEvent.click(screen.getByTestId('chat-screenshot-skip'))
    await act(async () => finish(new File(['jpeg'], 'screenshot.jpg', { type: 'image/jpeg' })))
    expect(screen.getByTestId('chat-ask-screenshot')).toHaveAttribute('data-state', 'skipped')
    expect(screen.queryByTestId('chat-screenshot-preview')).toBeNull()
    expect(uploadMock).not.toHaveBeenCalled()
  })

  it('a screenshot that cannot be taken says so and can be tried again', async () => {
    const toastErrorMock = vi.spyOn(toast, 'error')
    vi.mocked(capturePage).mockRejectedValueOnce(new Event('error'))
    sendMock.mockResolvedValue(undefined)
    askAfterSending('ask_screenshot', 'To see it.')
    await clickWhenReady('chat-screenshot-take')
    await waitFor(() => expect(screen.getByTestId('chat-ask-screenshot')).toHaveAttribute('data-state', 'idle'))
    expect(toastErrorMock).toHaveBeenCalledWith('Could not take the screenshot: the browser could not render the page.')
    await clickWhenReady('chat-screenshot-take')
    expect(await screen.findByTestId('chat-screenshot-preview')).toBeInTheDocument()
    expect(screen.getByTestId('chat-ask-screenshot')).toHaveAttribute('data-state', 'review')
  })

  it('a screenshot that fails to upload stays for another try, and sends nothing', async () => {
    const toastErrorMock = vi.spyOn(toast, 'error')
    uploadMock.mockRejectedValueOnce(new ApiError(503, 'File storage is not available.'))
      .mockResolvedValueOnce({ id: 'shot-2', filename: 'screenshot.jpg', size_bytes: 4, expires_at: '' })
    sendMock.mockResolvedValue(undefined)
    askAfterSending('ask_screenshot', 'To see it.')
    await clickWhenReady('chat-screenshot-take')
    fireEvent.click(await screen.findByTestId('chat-screenshot-send'))
    await waitFor(() => expect(toastErrorMock).toHaveBeenCalledWith('File storage is not available.'))
    expect(screen.getByTestId('chat-ask-screenshot')).toHaveAttribute('data-state', 'review')
    expect(screen.getByTestId('chat-screenshot-send')).toHaveTextContent('Send')
    expect(screen.getByTestId('chat-screenshot-send')).not.toHaveTextContent('again')
    expect(sendMock).toHaveBeenCalledTimes(1)
    await clickWhenReady('chat-screenshot-send')
    await waitFor(() => expect(sendMock).toHaveBeenCalledTimes(2))
    expect(uploadMock).toHaveBeenCalledTimes(2)
    expect(sendMock.mock.calls[1][0].screenshot_upload_id).toBe('shot-2')
  })

  it('a taken screenshot the user skips is never uploaded', async () => {
    sendMock.mockResolvedValue(undefined)
    askAfterSending('ask_screenshot', 'To see it.')
    await clickWhenReady('chat-screenshot-take')
    await clickWhenReady('chat-screenshot-discard')
    await waitFor(() => expect(sendMock).toHaveBeenCalledTimes(2))
    expect(sendMock.mock.calls[1][0].message).toBe("I'll skip the screenshot.")
    expect(sendMock.mock.calls[1][0].screenshot_upload_id).toBeUndefined()
    expect(uploadMock).not.toHaveBeenCalled()
    expect(screen.getByTestId('chat-ask-screenshot')).toHaveAttribute('data-state', 'skipped')
  })

  it('a screenshot that fails after the user skipped stays skipped, without an error', async () => {
    const toastErrorMock = vi.spyOn(toast, 'error')
    let fail: (e: unknown) => void = () => {}
    vi.mocked(capturePage).mockImplementationOnce(() => new Promise<File>((_, reject) => { fail = reject }))
    sendMock.mockResolvedValue(undefined)
    askAfterSending('ask_screenshot', 'To see it.')
    await clickWhenReady('chat-screenshot-take')
    fireEvent.click(screen.getByTestId('chat-screenshot-skip'))
    await act(async () => fail(new Error('boom')))
    expect(screen.getByTestId('chat-ask-screenshot')).toHaveAttribute('data-state', 'skipped')
    expect(toastErrorMock).not.toHaveBeenCalled()
  })

  it('a skipped screenshot sends only the answer', async () => {
    sendMock.mockResolvedValue(undefined)
    askAfterSending('ask_screenshot', 'To see it.')
    await clickWhenReady('chat-screenshot-skip')
    await waitFor(() => expect(sendMock).toHaveBeenCalledTimes(2))
    expect(sendMock.mock.calls[1][0].message).toBe("I'll skip the screenshot.")
    expect(sendMock.mock.calls[1][0].screenshot_upload_id).toBeUndefined()
    expect(uploadMock).not.toHaveBeenCalled()
  })

  // The trail goes with a report to the support team, never in a message:
  // a list of the user's actions in time order never reaches a model.
  it('sends no part of the action trail with a message', async () => {
    clearActionTrail()
    recordTrail({ kind: 'click', target: 'Export settings 7f3a' })
    recordTrail({ kind: 'api', method: 'POST', path: '/api/projects/12/export', status: 500 })
    sendMock.mockResolvedValue(undefined)
    renderPanel()
    openPanel()
    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'the export fails' } })
    fireEvent.click(screen.getByTestId('chat-send'))
    await waitFor(() => expect(sendMock).toHaveBeenCalledTimes(1))

    const body = JSON.stringify(sendMock.mock.calls[0][0])
    expect(actionTrail().length).toBeGreaterThanOrEqual(2)
    expect(body).toContain('the export fails')
    expect(body).not.toContain('Export settings 7f3a')
    expect(body).not.toContain('/api/projects/12/export')
    clearActionTrail()
  })

  it('a recording the assistant asks for tells it only that one is attached', async () => {
    sendMock.mockResolvedValue(undefined)
    askAfterSending('ask_repeat', 'So the team sees every step.', 'Could you do it once more?')

    await clickWhenReady('chat-repeat-start')
    expect(screen.getByTestId('chat-recording-banner')).toHaveTextContent('Do it again')
    await waitFor(() => expect(screen.getByTestId('chat-panel')).not.toBeVisible())
    const field = document.createElement('textarea')
    document.body.appendChild(field)
    field.value = 'the steps I typed'
    field.dispatchEvent(new Event('input', { bubbles: true }))

    fireEvent.click(screen.getByTestId('chat-recording-done'))
    await waitFor(() => expect(sendMock).toHaveBeenCalledTimes(2))
    expect(sendMock.mock.calls[1][0].message).toBe(
      'I repeated the steps. The recording is attached to the report for the support team.')
    expect(JSON.stringify(sendMock.mock.calls[1][0])).not.toContain('the steps I typed')
    expect(screen.getByTestId('chat-panel')).toBeVisible()
    expect(screen.queryByTestId('chat-recording-banner')).toBeNull()
    field.remove()
  })

  it('closing the panel mid-recording stops it and sends nothing', async () => {
    sendMock.mockResolvedValue(undefined)
    askAfterSending('ask_repeat', 'Once more.')
    await clickWhenReady('chat-repeat-start')
    fireEvent.click(await screen.findByTestId('chat-launcher'))
    fireEvent.click(screen.getByTestId('chat-close'))
    await waitFor(() => expect(screen.queryByTestId('chat-recording-banner')).toBeNull())
    await new Promise(r => setTimeout(r, 0))
    expect(sendMock).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('chat-panel')).toBeNull()
    expect(heldRecording()).toBeUndefined()
  })

  it("a new conversation drops the last one's recording", async () => {
    sendMock.mockResolvedValue(undefined)
    askAfterSending('ask_repeat', 'Once more.')
    await clickWhenReady('chat-repeat-start')
    fireEvent.click(screen.getByTestId('chat-recording-done'))
    await waitFor(() => expect(heldRecording()).toBeDefined())
    fireEvent.click(screen.getByTestId('chat-new'))
    expect(heldRecording()).toBeUndefined()
  })

  it('an organization with no projects is offered setup questions, and a click sends one', async () => {
    sendMock.mockResolvedValue(undefined)
    renderPanel()
    openPanel()
    fireEvent.click(await screen.findByRole('button', { name: 'Help me create my first project' }))
    await waitFor(() => expect(sendMock).toHaveBeenCalled())
    expect(sendMock.mock.calls[0][0].message).toBe('Help me create my first project')
    expect(projectsMock).toHaveBeenCalledWith({}, 0, 1)
  })

  it('an organization with projects is offered questions about its work', async () => {
    projectsMock.mockResolvedValue({ data: [{ id: 1 } as never], cursor: 0, has_more: false })
    renderPanel()
    openPanel()
    expect(await screen.findByRole('button', { name: 'What needs my attention today?' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Help me create my first project' })).not.toBeInTheDocument()
  })

  describe('playbook picker and quick replies', () => {
    const TOUR = [{ anchor: 'nav.projects', title: 'Projects', body: 'All work.' }]

    it('the header has no playbook menu: tours start from the Learn page', async () => {
      playbooksMock.mockResolvedValue([
        { name: 'demo-overview', title: 'Product overview demo', description: 'd', steps: TOUR, has_tour: true },
      ])
      renderPanel()
      openPanel()
      await screen.findByTestId('chat-new')
      expect(screen.queryByTitle('Demos and guides')).not.toBeInTheDocument()
    })

    it('a saved tour the assistant starts runs in the demo organization', async () => {
      const steps = [{ anchor: 'editor.segments', title: 'Segments', body: 'b', path: '/projects/4/jobs/9/edit' }]
      playbooksMock.mockResolvedValue([
        { name: 'demo-translate-a-document', title: 'Translate a document demo', description: 'd', steps, has_tour: true },
      ])
      sendMock.mockImplementation(async (_body, handlers) => {
        handlers.onClientAction?.('playbook_tour', { playbook: 'demo-translate-a-document' })
        handlers.onText('Starting the tour.')
        handlers.onDone({ conversation_id: 3 })
      })
      let assigned = ''
      const original = window.location
      Object.defineProperty(window, 'location', {
        configurable: true,
        value: { ...original, set href(v: string) { assigned = v }, get href() { return assigned } },
      })
      try {
        renderPanel()
        openPanel()
        fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'show me the editor' } })
        fireEvent.click(screen.getByTestId('chat-send'))
        await waitFor(() => expect(assigned).toBe('/tour/demo-translate-a-document/1'))
        // Its steps may point at the demo's own elements: never run here.
        expect(guideMock).not.toHaveBeenCalled()
      } finally {
        Object.defineProperty(window, 'location', { configurable: true, value: original })
      }
    })

    it('with a highlight showing, the replies go on its card, and come back when it is closed', async () => {
      attachMock.mockReturnValue(true)
      sendMock.mockImplementationOnce(async (_body, handlers) => {
        handlers.onClientAction?.('highlight', { anchor: 'editor.segments', caption: 'Here.' })
        handlers.onClientAction?.('suggest_replies', { replies: ['Next', 'End the demo'] })
        handlers.onDone({ conversation_id: 5 })
      })
      renderPanel()
      openPanel()
      fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'guide me' } })
      fireEvent.click(screen.getByTestId('chat-send'))
      await waitFor(() => expect(attachMock).toHaveBeenCalled())
      expect(attachMock.mock.calls[0][0].labels).toEqual(['Next', 'End the demo'])
      expect(screen.queryByTestId('chat-replies')).not.toBeInTheDocument()

      // A pick from the card is sent as the next message.
      sendMock.mockResolvedValueOnce(undefined)
      act(() => attachMock.mock.calls[0][0].onPick('Next'))
      await waitFor(() => expect(sendMock).toHaveBeenCalledTimes(2))
      expect(sendMock.mock.calls[1][0].message).toBe('Next')
    })

    it('a card closed without a pick puts the replies above the input', async () => {
      attachMock.mockReturnValue(true)
      sendMock.mockImplementationOnce(async (_body, handlers) => {
        handlers.onClientAction?.('suggest_replies', { replies: ['Next'] })
        handlers.onDone({ conversation_id: 5 })
      })
      renderPanel()
      openPanel()
      fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'guide me' } })
      fireEvent.click(screen.getByTestId('chat-send'))
      await waitFor(() => expect(attachMock).toHaveBeenCalled())
      act(() => attachMock.mock.calls[0][0].onDismiss())
      expect(await screen.findByTestId('chat-replies')).toHaveTextContent('Next')
    })

    it('reply buttons send the reply once, then disappear', async () => {
      sendMock.mockImplementationOnce(async (_body, handlers) => {
        handlers.onText('Step 1: open the editor.')
        handlers.onClientAction?.('suggest_replies', { replies: ['Next', 'Skip'] })
        handlers.onDone({ conversation_id: 5 })
      })
      renderPanel()
      openPanel()
      fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'walk me through it' } })
      fireEvent.click(screen.getByTestId('chat-send'))
      await waitFor(() => expect(screen.getByTestId('chat-replies')).toBeInTheDocument())

      sendMock.mockResolvedValueOnce(undefined)
      fireEvent.click(screen.getByRole('button', { name: 'Next' }))
      await waitFor(() => expect(sendMock).toHaveBeenCalledTimes(2))
      expect(sendMock.mock.calls[1][0].message).toBe('Next')
      expect(screen.queryByTestId('chat-replies')).not.toBeInTheDocument()
    })
  })

  describe('history survives a page refresh', () => {
    const SAVED = [
      { id: 1, conversation_id: 42, role: 'user' as const, content: 'how many jobs?', tool_calls: null, created_at: '' },
      {
        id: 2,
        conversation_id: 42,
        role: 'assistant' as const,
        content: 'You have 3 jobs.',
        tool_calls: [{ name: 'openloc_list_jobs', ok: true }],
        created_at: '',
      },
    ]

    async function chatOnce() {
      sendMock.mockImplementation(async (_body, handlers) => {
        handlers.onText('You have 3 jobs.')
        handlers.onDone({ conversation_id: 42 })
      })
      const first = renderPanel()
      openPanel()
      fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'how many jobs?' } })
      fireEvent.click(screen.getByTestId('chat-send'))
      await waitFor(() => expect(screen.getByText('You have 3 jobs.')).toBeInTheDocument())
      first.unmount()
    }

    it('reopens the last conversation with its messages and continues the thread', async () => {
      await chatOnce()
      messagesMock.mockResolvedValue(SAVED)

      renderPanel() // a fresh mount is what a refresh gives the panel
      openPanel()
      await waitFor(() => expect(screen.getByText('You have 3 jobs.')).toBeInTheDocument())
      expect(messagesMock).toHaveBeenCalledWith(42)
      expect(screen.getByText('how many jobs?')).toBeInTheDocument()
      expect(screen.getByTestId('chat-tool-chip')).toHaveTextContent('list_jobs')

      sendMock.mockReset()
      sendMock.mockResolvedValue(undefined)
      fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'and overdue?' } })
      fireEvent.click(screen.getByTestId('chat-send'))
      await waitFor(() => expect(sendMock).toHaveBeenCalled())
      expect(sendMock.mock.calls[0][0]).toMatchObject({ conversation_id: 42 })
    })

    it('a conversation that no longer exists falls back to an empty panel', async () => {
      await chatOnce()
      messagesMock.mockRejectedValue(new ApiError(404, 'conversation not found', {}))

      renderPanel()
      openPanel()
      await waitFor(() => expect(messagesMock).toHaveBeenCalled())
      expect(await screen.findByText(/Ask about your projects/)).toBeInTheDocument()

      sendMock.mockReset()
      sendMock.mockResolvedValue(undefined)
      fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'hi' } })
      fireEvent.click(screen.getByTestId('chat-send'))
      await waitFor(() => expect(sendMock).toHaveBeenCalled())
      expect(sendMock.mock.calls[0][0].conversation_id).toBeUndefined()
    })

    it('new conversation forgets the saved one', async () => {
      await chatOnce()
      messagesMock.mockResolvedValue(SAVED)
      const second = renderPanel()
      openPanel()
      await waitFor(() => expect(screen.getByText('You have 3 jobs.')).toBeInTheDocument())
      fireEvent.click(screen.getByTestId('chat-new'))
      second.unmount()
      messagesMock.mockClear()

      renderPanel()
      openPanel()
      expect(await screen.findByText(/Ask about your projects/)).toBeInTheDocument()
      expect(messagesMock).not.toHaveBeenCalled()
    })

    it('the history menu lists past conversations and opens one', async () => {
      conversationsMock.mockResolvedValue([
        { id: 42, title: 'how many jobs?', created_at: '', updated_at: '2026-09-24T10:00:00Z' },
        { id: 17, title: 'overdue work', created_at: '', updated_at: '2026-09-23T10:00:00Z' },
      ])
      messagesMock.mockResolvedValue([
        { id: 9, conversation_id: 17, role: 'assistant', content: 'Two jobs are overdue.', tool_calls: null, created_at: '' },
      ])
      renderPanel()
      openPanel()
      fireEvent.click(screen.getByTestId('chat-history'))
      fireEvent.click(await menuItem('chat-history-17'))
      await waitFor(() => expect(screen.getByText('Two jobs are overdue.')).toBeInTheDocument())
      expect(messagesMock).toHaveBeenCalledWith(17)
    })
  })

  describe('UI guide', () => {
    function streamAction(action: string, payload: Record<string, unknown>) {
      sendMock.mockImplementation(async (_body, handlers) => {
        handlers.onClientAction?.(action, payload)
        handlers.onText('Here it is.')
        handlers.onDone({ conversation_id: 3 })
      })
      renderPanel()
      openPanel()
      fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'where?' } })
      fireEvent.click(screen.getByTestId('chat-send'))
    }

    it('sends the on-screen anchors with the message', async () => {
      streamAction('noop', {})
      await waitFor(() => expect(sendMock).toHaveBeenCalled())
      expect(sendMock.mock.calls[0][0]).toMatchObject({ visible_anchors: ['projects.new', 'nav.tasks'] })
    })

    it('highlight runs a one-step guide with the caption, keeping the panel open', async () => {
      streamAction('highlight', { anchor: 'projects.new', caption: 'Start here.', path: '/projects' })
      await waitFor(() => expect(guideMock).toHaveBeenCalled())
      expect(guideMock.mock.calls[0][0]).toEqual([{ path: '/projects', anchor: 'projects.new', body: 'Start here.' }])
      expect(screen.getByTestId('chat-panel')).toBeVisible()
    })

    it('tour runs every step and minimizes the panel out of the way', async () => {
      streamAction('tour', {
        steps: [
          { anchor: 'nav.projects', title: 'Projects', body: 'All work.' },
          { anchor: 'tms.new', title: 'Memories', body: 'TMs.', path: '/memories' },
        ],
      })
      await waitFor(() => expect(guideMock).toHaveBeenCalled())
      expect(guideMock.mock.calls[0][0]).toHaveLength(2)
      expect(guideMock.mock.calls[0][0][1]).toMatchObject({ anchor: 'tms.new', path: '/memories' })
      await waitFor(() => expect(screen.getByTestId('chat-launcher')).toBeInTheDocument())
    })

    it('drops origin-shaped step paths', async () => {
      streamAction('highlight', { anchor: 'projects.new', caption: 'x', path: '//evil.example/x' })
      await waitFor(() => expect(guideMock).toHaveBeenCalled())
      expect(guideMock.mock.calls[0][0][0].path).toBeUndefined()
    })
  })

  it('renders an inline chart on a render_chart client_action', async () => {
    sendMock.mockImplementation(async (_body, handlers) => {
      handlers.onToolStart('openloc_query_metrics')
      handlers.onToolEnd('openloc_query_metrics', true)
      handlers.onClientAction?.('render_chart', {
        chart: {
          title: 'MT usage (de)',
          type: 'bar',
          x_key: 'mt_engine',
          series: ['words'],
          data: [
            { mt_engine: 'DeepL', words: 120 },
            { mt_engine: 'Claude', words: 80 },
          ],
        },
      })
      handlers.onText('DeepL leads with 120 words.')
      handlers.onDone({ conversation_id: 9 })
    })
    renderPanel()
    openPanel()
    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'chart mt usage' } })
    fireEvent.click(screen.getByTestId('chat-send'))

    await waitFor(() => expect(screen.getByText('DeepL leads with 120 words.')).toBeInTheDocument())
    // The chart block mounted inside the assistant message with its title.
    // (recharts' ResponsiveContainer measures 0x0 in jsdom, so the svg body is
    // e2e's problem — what's pinned here is the client_action → state → mount
    // plumbing that INC-class regressions would break.)
    expect(screen.getByTestId('chat-chart')).toBeInTheDocument()
    expect(screen.getByText('MT usage (de)')).toBeInTheDocument()
  })

  it('refuses origin-shaped client_action paths (defense-in-depth)', async () => {
    sendMock.mockImplementation(async (_body, handlers) => {
      handlers.onClientAction?.('navigate', { path: '//evil.example/phish' })
      handlers.onText('done')
      handlers.onDone({ conversation_id: 1 })
    })
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/tasks']}>
          <Routes>
            <Route path="*" element={<LocationProbe />} />
          </Routes>
          <ChatPanel />
        </MemoryRouter>
      </QueryClientProvider>,
    )
    openPanel()
    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'q' } })
    fireEvent.click(screen.getByTestId('chat-send'))
    await waitFor(() => expect(screen.getByText('done')).toBeInTheDocument())
    expect(screen.getByTestId('location-probe').textContent).toBe('/tasks')
  })

  it('mode picker: defaults read-only, switching to write flags the send and the badge', async () => {
    sendMock.mockImplementation(async (_body, handlers) => {
      handlers.onText('ok')
      handlers.onDone({ conversation_id: 1 })
    })
    renderPanel()
    openPanel()

    // Default: read-only — no flag on the wire.
    expect(screen.getByTestId('chat-mode-badge').textContent).toBe('read-only')
    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'q1' } })
    fireEvent.click(screen.getByTestId('chat-send'))
    await waitFor(() => expect(sendMock).toHaveBeenCalledTimes(1))
    expect(sendMock.mock.calls[0][0].allow_writes).toBeUndefined()

    // Switch to write: badge turns, flag rides the send, choice persists.
    fireEvent.click(screen.getByTestId('chat-mode-picker'))
    fireEvent.click(await menuItem('chat-mode-write'))
    expect(screen.getByTestId('chat-mode-badge').textContent).toBe('write')

    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'q2' } })
    fireEvent.click(screen.getByTestId('chat-send'))
    await waitFor(() => expect(sendMock).toHaveBeenCalledTimes(2))
    expect(sendMock.mock.calls[1][0].allow_writes).toBe(true)
    expect(localStorage.getItem('openloc-chat-mode')).toBe('write')

    // And back to read-only.
    fireEvent.click(screen.getByTestId('chat-mode-picker'))
    fireEvent.click(await menuItem('chat-mode-read'))
    expect(screen.getByTestId('chat-mode-badge').textContent).toBe('read-only')
    expect(localStorage.getItem('openloc-chat-mode')).toBe('read')
  })

  it('launcher shows the current provider logo while minimized (not the generic icon)', async () => {
    renderPanel()
    openPanel()
    await waitFor(() => expect(screen.getByTestId('chat-model-picker').textContent).toContain('Sonnet 5'))

    fireEvent.click(screen.getByTestId('chat-minimize'))
    const launcher = screen.getByTestId('chat-launcher')
    expect(launcher).toHaveAttribute('data-provider', 'claude')
    // The provider mark is an inline SVG path.
    expect(launcher.querySelector('svg path')).not.toBeNull()
  })

  it('model picker: greyed when no provider is connected at all', async () => {
    modelsMock.mockResolvedValue(MODELS.map(m => ({ ...m, connected: false, selectable: false })))
    renderPanel()
    openPanel()
    await waitFor(() =>
      expect(screen.getByTestId('chat-model-picker')).toHaveAttribute('title', 'No AI provider connected'),
    )
  })

  // The dropdown has three ways to hold no rows, and each has to say which one
  // it is. An unexplained empty menu is the exact debugging cost the model
  // catalog set out to remove, so the reason has to reach
  // the surface a user is actually looking at.
  it('model picker: says it is loading before the first response arrives', async () => {
    // Never resolves, which is the state between app mount and the first
    // /api/chat/models response — the window a user can open the picker in.
    modelsMock.mockReturnValue(new Promise(() => {}))
    renderPanel()
    openPanel()
    fireEvent.click(await screen.findByTestId('chat-model-picker'))
    expect(await menuText('Loading models…')).toBeInTheDocument()
  })

  it('model picker: an empty catalog says no provider is connected', async () => {
    modelsMock.mockResolvedValue([])
    renderPanel()
    openPanel()
    const picker = await screen.findByTestId('chat-model-picker')
    fireEvent.click(picker)
    expect(await menuText('No AI provider connected')).toBeInTheDocument()
  })

  it('model picker: a failed load says so instead of opening blank', async () => {
    modelsMock.mockRejectedValue(new Error('network is down'))
    renderPanel()
    openPanel()
    const picker = await screen.findByTestId('chat-model-picker')
    fireEvent.click(picker)
    expect(await menuText("Couldn't load models")).toBeInTheDocument()
  })

  it('minimize hides the panel but preserves history and thread; close discards both', async () => {
    sendMock.mockImplementation(async (_body, handlers) => {
      handlers.onText('the answer')
      handlers.onDone({ conversation_id: 7 })
    })
    renderPanel()
    openPanel()
    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'my question' } })
    fireEvent.click(screen.getByTestId('chat-send'))
    await waitFor(() => expect(screen.getByText('the answer')).toBeInTheDocument())

    // Minimize: panel visually gone, launcher back, nothing unmounted.
    fireEvent.click(screen.getByTestId('chat-minimize'))
    expect(screen.getByTestId('chat-panel')).not.toBeVisible()
    expect(screen.getByTestId('chat-launcher')).toBeInTheDocument()

    // Reopen: SAME history, and the next send continues the SAME conversation.
    openPanel()
    expect(screen.getByTestId('chat-panel')).toBeVisible()
    expect(screen.getByText('the answer')).toBeInTheDocument()
    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'follow-up' } })
    fireEvent.click(screen.getByTestId('chat-send'))
    await waitFor(() => expect(sendMock).toHaveBeenCalledTimes(2))
    expect(sendMock.mock.calls[1][0].conversation_id).toBe(7)

    // Close (×): discards local history — reopening starts fresh.
    fireEvent.click(screen.getByTestId('chat-close'))
    openPanel()
    expect(screen.queryByText('the answer')).not.toBeInTheDocument()
    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'new thread' } })
    fireEvent.click(screen.getByTestId('chat-send'))
    await waitFor(() => expect(sendMock).toHaveBeenCalledTimes(3))
    expect(sendMock.mock.calls[2][0].conversation_id).toBeUndefined()
  })

  it('minimizing mid-stream does NOT abort the running turn', async () => {
    let sawAbort = false
    let finish!: () => void
    sendMock.mockImplementation((_body, handlers, signal) => {
      signal?.addEventListener('abort', () => { sawAbort = true })
      return new Promise<void>(resolve => {
        finish = () => {
          handlers.onText('late answer')
          handlers.onDone({ conversation_id: 1 })
          resolve()
        }
      })
    })
    renderPanel()
    openPanel()
    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'slow q' } })
    fireEvent.click(screen.getByTestId('chat-send'))

    fireEvent.click(screen.getByTestId('chat-minimize'))
    expect(sawAbort).toBe(false)

    // The answer lands while minimized and is there on reopen.
    finish()
    openPanel()
    await waitFor(() => expect(screen.getByText('late answer')).toBeInTheDocument())
  })

  it('shows a working indicator while a turn is in flight, with elapsed after 5s', async () => {
    vi.useFakeTimers()
    try {
      let finish!: () => void
      sendMock.mockImplementation((_body, handlers) => {
        // Keep the watchdog fed so only the indicator is under test here.
        const ping = setInterval(() => handlers.onAlive?.(), 1000)
        return new Promise<void>(resolve => {
          finish = () => {
            clearInterval(ping)
            handlers.onText('done!')
            handlers.onDone({ conversation_id: 1 })
            resolve()
          }
        })
      })
      renderPanel()
      openPanel()
      fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'long question' } })
      fireEvent.click(screen.getByTestId('chat-send'))

      // Indicator appears immediately; elapsed counter only after 5s.
      await vi.advanceTimersByTimeAsync(1000)
      expect(screen.getByTestId('chat-working')).toBeInTheDocument()
      expect(screen.queryByTestId('chat-elapsed')).not.toBeInTheDocument()

      await vi.advanceTimersByTimeAsync(6000)
      expect(screen.getByTestId('chat-elapsed').textContent).toMatch(/^[67]s$/)

      finish()
      await vi.advanceTimersByTimeAsync(0)
      expect(screen.queryByTestId('chat-working')).not.toBeInTheDocument()
      expect(screen.getByText('done!')).toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })

  it('watchdog: a stream silent past the stall timeout aborts with an explicit error', async () => {
    vi.useFakeTimers()
    try {
      let aborted = false
      sendMock.mockImplementation((_body, _handlers, signal) => {
        // A stream that never yields another byte: no onAlive, never resolves
        // on its own — exactly what an LB-cut connection looks like.
        return new Promise<void>((_resolve, reject) => {
          signal?.addEventListener('abort', () => {
            aborted = true
            reject(new DOMException('aborted', 'AbortError'))
          })
        })
      })
      renderPanel()
      openPanel()
      fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'q' } })
      fireEvent.click(screen.getByTestId('chat-send'))

      await vi.advanceTimersByTimeAsync(61_000)
      expect(aborted).toBe(true)
      expect(screen.getByTestId('chat-error').textContent).toContain('went quiet')
      // The panel is usable again — not stuck in streaming state.
      expect(screen.queryByTestId('chat-stop')).not.toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })

  it('resizes by dragging the top-left handle and persists the size', () => {
    renderPanel()
    openPanel()
    const panel = screen.getByTestId('chat-panel')
    const handle = screen.getByTestId('chat-resize-handle')

    // Defaults applied as inline styles so drag math has a baseline.
    expect(panel.style.width).toBe('400px')
    expect(panel.style.height).toBe('560px')

    // Panel is anchored bottom-right: dragging the handle LEFT and UP grows it.
    fireEvent.pointerDown(handle, { clientX: 500, clientY: 300 })
    fireEvent.pointerMove(window, { clientX: 400, clientY: 200 })
    fireEvent.pointerUp(window)

    expect(panel.style.width).toBe('500px')
    expect(panel.style.height).toBe('660px')

    // Size survives close/reopen via localStorage.
    fireEvent.click(screen.getByTestId('chat-close'))
    openPanel()
    const reopened = screen.getByTestId('chat-panel')
    expect(reopened.style.width).toBe('500px')
    expect(reopened.style.height).toBe('660px')
  })

  it('clamps resize to the minimum size', () => {
    renderPanel()
    openPanel()
    const handle = screen.getByTestId('chat-resize-handle')

    // Drag hard right/down — shrinking far past the minimum.
    fireEvent.pointerDown(handle, { clientX: 100, clientY: 100 })
    fireEvent.pointerMove(window, { clientX: 900, clientY: 900 })
    fireEvent.pointerUp(window)

    const panel = screen.getByTestId('chat-panel')
    expect(panel.style.width).toBe('320px')
    expect(panel.style.height).toBe('400px')
  })

  it('renders GFM tables as real <table> elements', async () => {
    // The model answers list-y questions with GFM tables. Without remark-gfm,
    // react-markdown treats the pipes as paragraph text and the newlines
    // collapse into an unreadable single line — the exact prod symptom.
    const table = '| Project | Deadline |\n|---|---|\n| Alpha | 2026-08-03 |\n| Bravo | 2026-08-10 |'
    sendMock.mockImplementation(async (_body, handlers) => {
      handlers.onText(table)
      handlers.onDone({ conversation_id: 1 })
    })
    renderPanel()
    openPanel()
    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'overdue?' } })
    fireEvent.click(screen.getByTestId('chat-send'))

    await waitFor(() => {
      const messages = screen.getByTestId('chat-messages')
      expect(messages.querySelector('table')).not.toBeNull()
    })
    const messages = screen.getByTestId('chat-messages')
    expect(messages.querySelectorAll('th')).toHaveLength(2)
    expect(messages.querySelectorAll('td')).toHaveLength(4)
    // And the raw pipe syntax must NOT be visible as text.
    expect(messages.textContent).not.toContain('|---|')
  })

  it('collapses consecutive identical tool chips into one ×N chip', async () => {
    sendMock.mockImplementation(async (_body, handlers) => {
      handlers.onToolStart('openloc_list_projects')
      handlers.onToolEnd('openloc_list_projects', true)
      for (let i = 0; i < 39; i++) {
        handlers.onToolStart('openloc_get_project')
        handlers.onToolEnd('openloc_get_project', true)
      }
      handlers.onText('done')
      handlers.onDone({ conversation_id: 1 })
    })
    renderPanel()
    openPanel()
    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'overdue?' } })
    fireEvent.click(screen.getByTestId('chat-send'))

    await waitFor(() => expect(screen.getByText('done')).toBeInTheDocument())
    const chips = screen.getAllByTestId('chat-tool-chip')
    // 40 calls → 2 chips: list_projects, then get_project ×39.
    expect(chips).toHaveLength(2)
    expect(chips[1].textContent).toContain('get_project')
    expect(chips[1].textContent).toContain('×39')
  })
})

  it('chat-bus prompt event opens the panel with the input prefilled, not sent', async () => {
    renderPanel()
    expect(screen.queryByTestId('chat-panel')).not.toBeInTheDocument()

    act(() => {
      openChatWithPrompt('Create a dashboard called "Operations" with useful charts.')
    })

    await waitFor(() => expect(screen.getByTestId('chat-panel')).toBeVisible())
    expect(screen.getByTestId('chat-input')).toHaveValue('Create a dashboard called "Operations" with useful charts.')
    // Prefill only — nothing was sent.
    expect(sendMock).not.toHaveBeenCalled()
  })

  it('chat-bus HINT opens the panel with a greyed placeholder — input stays empty and unsendable', async () => {
    renderPanel()
    act(() => {
      openChatWithHint('Create a chart to show words processed per week over last 90 days')
    })

    await waitFor(() => expect(screen.getByTestId('chat-panel')).toBeVisible())
    const input = screen.getByTestId('chat-input')
    expect(input).toHaveValue('')
    expect(input).toHaveAttribute('placeholder', 'Create a chart to show words processed per week over last 90 days')
    // Empty input ⇒ send is inert: the hint cannot be sent as-is.
    expect(screen.getByTestId('chat-send')).toHaveAttribute('aria-disabled', 'true')
    fireEvent.click(screen.getByTestId('chat-send'))
    expect(sendMock).not.toHaveBeenCalled()
  })

  it('successful create_chart invalidates dashboards queries; other tools do not', async () => {
    sendMock.mockImplementation(async (_body, handlers) => {
      handlers.onToolStart('openloc_query_metrics')
      handlers.onToolEnd('openloc_query_metrics', true)
      handlers.onToolStart('openloc_create_chart')
      handlers.onToolEnd('openloc_create_chart', true)
      handlers.onText('Saved to Operations.')
      handlers.onDone({ conversation_id: 3 })
    })
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const invalidateSpy = vi.spyOn(qc, 'invalidateQueries')
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <ChatPanel />
        </MemoryRouter>
      </QueryClientProvider>,
    )
    openPanel()
    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: 'save the chart' } })
    fireEvent.click(screen.getByTestId('chat-send'))
    await waitFor(() => expect(screen.getByText('Saved to Operations.')).toBeInTheDocument())

    // Exactly the dashboards prefix, exactly once — the read-only
    // query_metrics call must not have triggered one.
    const dashboardCalls = invalidateSpy.mock.calls.filter(
      c => JSON.stringify(c[0]?.queryKey) === JSON.stringify(['dashboards']),
    )
    expect(dashboardCalls).toHaveLength(1)
  })
