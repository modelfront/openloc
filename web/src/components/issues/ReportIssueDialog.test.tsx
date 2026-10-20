import { describe, it, expect, vi, beforeEach } from 'vitest'
import { cleanup, render, screen, waitFor, fireEvent } from '@testing-library/react'
import { toast } from 'sonner'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'

vi.mock('sonner', () => ({
  toast: Object.assign(vi.fn(), { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() }),
}))

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof import('@/api')>('@/api')
  return { ...actual, createIssue: vi.fn(), getAppConfig: vi.fn(), listChatModels: vi.fn(), fetchChatScreenshot: vi.fn() }
})

import { ApiError, createIssue, fetchChatScreenshot, getAppConfig, listChatModels, type ChatModelInfo, type Issue } from '@/api'
import { CHAT_PROMPT_EVENT, type ChatPromptDetail } from '@/lib/chat-bus'
import { queryKeys } from '@/lib/query-keys'
import { clearActionTrail, recordTrail } from '@/lib/action-trail'
import { clearHeldRecording, heldRecording, startRecording } from '@/lib/watch-recording'
import { ReportIssueDialog, type ReportIssueDefaults } from './ReportIssueDialog'

const ISSUE = { id: 5, title: 'Export does nothing' } as Issue

let qc: QueryClient
function renderDialog(path = '/projects/12/jobs/34/edit?tab=qa', onClose = vi.fn(), defaults?: ReportIssueDefaults) {
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <ReportIssueDialog open onClose={onClose} defaults={defaults} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return onClose
}

const model = (selectable: boolean) => ({ id: 'm', provider: 'claude', selectable, default: true }) as ChatModelInfo

beforeEach(() => {
  vi.mocked(toast.warning).mockClear()
  vi.mocked(toast.error).mockClear()
  vi.mocked(listChatModels).mockResolvedValue([model(false)])
  vi.mocked(createIssue).mockReset().mockResolvedValue(ISSUE)
  vi.mocked(getAppConfig).mockResolvedValue({
    version: 'abc123', app_url: '', docs_url: '', docs_host: false, developer_url: '', developer_host: false,
    playground: false, issues: true,
  })
})

describe('ReportIssueDialog', () => {
  it('shows what goes with the report', async () => {
    renderDialog()
    const preview = screen.getByTestId('report-issue-context')
    expect(preview).toHaveTextContent('/projects/12/jobs/34/edit')
    expect(preview).toHaveTextContent('Project12')
    expect(preview).toHaveTextContent('Job34')
    await waitFor(() => expect(preview).toHaveTextContent('abc123'))
  })

  it('refuses an empty report and sends nothing', async () => {
    renderDialog()
    await userEvent.click(screen.getByTestId('report-issue-submit'))
    expect(await screen.findByText('Add a short title.')).toBeInTheDocument()
    expect(screen.getByText('Describe what happened.')).toBeInTheDocument()
    expect(createIssue).not.toHaveBeenCalled()
  })

  it('sends a bug with its severity, area, text, context and files, then closes', async () => {
    const onClose = renderDialog()
    await userEvent.click(screen.getByRole('radio', { name: /Critical/ }))
    await userEvent.type(screen.getByTestId('report-issue-title'), 'Export does nothing')
    await userEvent.type(screen.getByTestId('report-issue-description'), 'I click Export and nothing happens.')
    const shot = new File(['png'], 'screen.png', { type: 'image/png' })
    const log = new File(['panic'], 'server.log', { type: 'text/plain' })
    fireEvent.change(screen.getByTestId('report-issue-files-input'), { target: { files: [shot, log] } })
    await userEvent.click(screen.getByTestId('report-issue-submit'))

    await waitFor(() => expect(createIssue).toHaveBeenCalledTimes(1))
    const sent = vi.mocked(createIssue).mock.calls[0][0]
    expect(sent).toMatchObject({
      kind: 'bug', severity: 'critical', area: 'other',
      // The picked image goes in the text where the cursor was; the log does not.
      title: 'Export does nothing', description: 'I click Export and nothing happens.\n![screen.png](upload:0)',
      context: { path: '/projects/12/jobs/34/edit', project_id: 12, job_id: 34 },
    })
    expect(sent.files).toEqual([shot, log])
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  // A save that fails must leave the typed text to send again.
  it('stays open while it sends: Cancel is off and Escape does not close it', async () => {
    let finish: (issue: Issue) => void = () => {}
    vi.mocked(createIssue).mockReturnValue(new Promise<Issue>(resolve => { finish = resolve }))
    const onClose = renderDialog()
    await userEvent.type(screen.getByTestId('report-issue-title'), 'Export does nothing')
    await userEvent.type(screen.getByTestId('report-issue-description'), 'I click Export and nothing happens.')
    await userEvent.click(screen.getByTestId('report-issue-submit'))
    await waitFor(() => expect(createIssue).toHaveBeenCalledTimes(1))

    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
    await userEvent.keyboard('{Escape}')
    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getByTestId('report-issue-title')).toHaveValue('Export does nothing')

    finish(ISSUE)
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it("sends the assistant's check without showing it to the reporter", async () => {
    const verification = { result: 'verified' as const, note: 'The job has 120 segments.', token: 'tok' }
    renderDialog(undefined, vi.fn(), {
      kind: 'bug', title: 'Editor stops at 100', description: 'It does.', fromAssistant: true, verification,
    })
    expect(screen.getByTestId('report-issue-dialog')).not.toHaveTextContent('Verified')
    expect(screen.getByTestId('report-issue-dialog')).not.toHaveTextContent(verification.note)
    await userEvent.click(screen.getByTestId('report-issue-submit'))
    await waitFor(() => expect(createIssue).toHaveBeenCalledTimes(1))
    expect(vi.mocked(createIssue).mock.calls[0][0]).toMatchObject({ verification, context: { via: 'AI assistant' } })
  })

  it('shows the recent actions it sends with the report', async () => {
    clearActionTrail()
    recordTrail({ kind: 'page', path: '/projects/12' })
    recordTrail({ kind: 'api', method: 'POST', path: '/api/projects/12/export', status: 500 })
    renderDialog()
    await userEvent.click(screen.getByTestId('report-issue-trail-toggle'))
    expect(screen.getByTestId('report-issue-trail-events')).toHaveTextContent('POST /api/projects/12/export failed (500)')
    await userEvent.type(screen.getByTestId('report-issue-title'), 'Export fails')
    await userEvent.type(screen.getByTestId('report-issue-description'), 'It does.')
    await userEvent.click(screen.getByTestId('report-issue-submit'))
    await waitFor(() => expect(createIssue).toHaveBeenCalledTimes(1))
    expect(vi.mocked(createIssue).mock.calls[0][0].trail?.map(e => e.kind)).toEqual(['page', 'api'])
  })

  it("attaches the assistant's screenshots as files the user sees", async () => {
    const shot = new File(['jpeg'], 'screenshot-abc.jpg', { type: 'image/jpeg' })
    vi.mocked(fetchChatScreenshot).mockResolvedValue(shot)
    renderDialog(undefined, vi.fn(), { title: 'Grid is empty', description: 'It is.', screenshotUploadIds: ['abc'] })
    expect(await screen.findByText('screenshot-abc.jpg')).toBeInTheDocument()
    expect(fetchChatScreenshot).toHaveBeenCalledWith('abc')
    await userEvent.click(screen.getByTestId('report-issue-submit'))
    await waitFor(() => expect(createIssue).toHaveBeenCalledTimes(1))
    expect(vi.mocked(createIssue).mock.calls[0][0].files).toEqual([shot])
  })

  it("sends the recording the user made for the assistant, and only from the assistant's report", async () => {
    clearHeldRecording()
    const stop = startRecording(() => {})
    recordTrail({ kind: 'click', target: 'Export' })
    stop()
    await waitFor(() => expect(heldRecording()).toBeDefined())

    renderDialog(undefined, vi.fn(), { title: 'Export fails', description: 'It does.' })
    expect(screen.queryByTestId('report-issue-recording')).toBeNull()
    cleanup()

    renderDialog(undefined, vi.fn(), { title: 'Export fails', description: 'It does.', fromAssistant: true })
    await userEvent.click(screen.getByTestId('report-issue-recording-toggle'))
    expect(screen.getByTestId('report-issue-recording-events')).toHaveTextContent('Clicked “Export”')
    await userEvent.click(screen.getByTestId('report-issue-submit'))
    await waitFor(() => expect(createIssue).toHaveBeenCalledTimes(1))
    expect(vi.mocked(createIssue).mock.calls[0][0].recording?.events).toHaveLength(1)
    await waitFor(() => expect(heldRecording()).toBeUndefined())
  })

  it('waits for the screenshots from the chat before it sends', async () => {
    let arrive: (f: File) => void = () => {}
    vi.mocked(fetchChatScreenshot).mockReturnValue(new Promise(resolve => { arrive = resolve }))
    renderDialog(undefined, vi.fn(), { title: 'Grid is empty', description: 'It is.', screenshotUploadIds: ['abc'] })
    const submit = screen.getByTestId('report-issue-submit')
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    fireEvent.click(submit)
    expect(createIssue).not.toHaveBeenCalled()
    arrive(new File(['jpeg'], 'screenshot-abc.jpg', { type: 'image/jpeg' }))
    expect(await screen.findByText('screenshot-abc.jpg')).toBeInTheDocument()
    await userEvent.click(screen.getByTestId('report-issue-submit'))
    await waitFor(() => expect(createIssue).toHaveBeenCalledTimes(1))
    expect(vi.mocked(createIssue).mock.calls[0][0].files).toHaveLength(1)
  })

  it('sends without a screenshot that has expired, and says so', async () => {
    vi.mocked(fetchChatScreenshot).mockRejectedValue(new ApiError(404, 'The screenshot is gone.'))
    renderDialog(undefined, vi.fn(), { title: 'Grid is empty', description: 'It is.', screenshotUploadIds: ['gone'] })
    await waitFor(() => expect(screen.getByTestId('report-issue-submit')).not.toHaveAttribute('aria-disabled', 'true'))
    expect(toast.warning).toHaveBeenCalledWith(expect.stringContaining('has expired'))
    expect(toast.error).not.toHaveBeenCalled()
    await userEvent.click(screen.getByTestId('report-issue-submit'))
    await waitFor(() => expect(createIssue).toHaveBeenCalledTimes(1))
    expect(vi.mocked(createIssue).mock.calls[0][0].files).toEqual([])
  })

  it('offers to try again when a screenshot from the chat fails to load', async () => {
    vi.mocked(fetchChatScreenshot).mockRejectedValueOnce(new ApiError(500, 'Server error.'))
      .mockResolvedValueOnce(new File(['jpeg'], 'screenshot-abc.jpg', { type: 'image/jpeg' }))
    renderDialog(undefined, vi.fn(), { title: 'Grid is empty', description: 'It is.', screenshotUploadIds: ['abc'] })
    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    expect(toast.warning).not.toHaveBeenCalled()
    const [, options] = vi.mocked(toast.error).mock.calls[0] as [string, { action: { label: string; onClick: () => void } }]
    expect(options.action.label).toBe('Try again')
    options.action.onClick()
    expect(await screen.findByText('screenshot-abc.jpg')).toBeInTheDocument()
  })

  it('leaves out the recorded steps when the user removes them', async () => {
    clearHeldRecording()
    const stop = startRecording(() => {})
    recordTrail({ kind: 'click', target: 'Export' })
    stop()
    renderDialog(undefined, vi.fn(), { title: 'Export fails', description: 'It does.', fromAssistant: true })
    await userEvent.click(screen.getByTestId('report-issue-recording-remove'))
    expect(screen.queryByTestId('report-issue-recording')).toBeNull()
    await userEvent.click(screen.getByTestId('report-issue-submit'))
    await waitFor(() => expect(createIssue).toHaveBeenCalledTimes(1))
    expect(vi.mocked(createIssue).mock.calls[0][0].recording).toBeUndefined()
    await waitFor(() => expect(heldRecording()).toBeUndefined())
  })

  it('asks a severity of bugs only', async () => {
    renderDialog()
    expect(screen.getByTestId('report-issue-severity')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('radio', { name: /Feature request/ }))
    expect(screen.queryByTestId('report-issue-severity')).toBeNull()
    await userEvent.type(screen.getByTestId('report-issue-title'), 'Dark mode')
    await userEvent.type(screen.getByTestId('report-issue-description'), 'Please.')
    await userEvent.click(screen.getByTestId('report-issue-submit'))

    await waitFor(() => expect(createIssue).toHaveBeenCalledTimes(1))
    const sent = vi.mocked(createIssue).mock.calls[0][0]
    expect(sent.kind).toBe('feature_request')
    expect(sent.severity).toBeUndefined()
  })

  it('leaves out files over the limits', async () => {
    renderDialog()
    const big = new File(['x'], 'huge.zip')
    Object.defineProperty(big, 'size', { value: 25 * 1024 * 1024 + 1 })
    const many = Array.from({ length: 11 }, (_, i) => new File(['x'], `f${i}.txt`))
    fireEvent.change(screen.getByTestId('report-issue-files-input'), { target: { files: [big, ...many] } })
    const chips = screen.getAllByTestId('report-issue-files-file')
    expect(chips).toHaveLength(10)
    expect(screen.queryByText('huge.zip')).toBeNull()
    expect(screen.queryByText('f10.txt')).toBeNull()
  })

  it('removes one picked file', async () => {
    renderDialog()
    const files = [new File(['a'], 'a.txt'), new File(['b'], 'b.txt')]
    fireEvent.change(screen.getByTestId('report-issue-files-input'), { target: { files } })
    await userEvent.click(screen.getByRole('button', { name: 'Remove a.txt' }))
    expect(screen.queryByText('a.txt')).toBeNull()
    expect(screen.getByText('b.txt')).toBeInTheDocument()
  })

  it('offers the assistant when the chat has a key, and hands it the report', async () => {
    vi.mocked(listChatModels).mockResolvedValue([model(false), model(true)])
    const prompts: ChatPromptDetail[] = []
    const listen = (e: Event) => prompts.push((e as CustomEvent<ChatPromptDetail>).detail)
    window.addEventListener(CHAT_PROMPT_EVENT, listen)
    const onClose = renderDialog()
    await userEvent.click(await screen.findByTestId('report-issue-ask-assistant'))
    window.removeEventListener(CHAT_PROMPT_EVENT, listen)
    expect(onClose).toHaveBeenCalled()
    expect(prompts).toEqual([{ prompt: 'I want to report an issue to the support team: ' }])
    expect(createIssue).not.toHaveBeenCalled()
  })

  it('does not offer the assistant without a usable key', async () => {
    renderDialog()
    // The models have loaded: the link's absence is the selectable check, not the wait.
    await waitFor(() => expect(qc.getQueryState(queryKeys.chat.models)?.status).toBe('success'))
    expect(screen.queryByTestId('report-issue-assistant')).toBeNull()
  })
})
