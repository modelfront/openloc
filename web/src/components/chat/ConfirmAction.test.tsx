// The confirm card for the assistant's issue report: it opens the real report
// form filled in, and the form, not the card, sends the report.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof import('@/api')>('@/api')
  return {
    ...actual,
    createIssue: vi.fn(), getAppConfig: vi.fn(), listChatModels: vi.fn(),
    confirmChatAction: vi.fn(), recordChatIssueReported: vi.fn(),
  }
})

import { confirmChatAction, createIssue, getAppConfig, listChatModels, recordChatIssueReported, type ChatConfirmRequest, type ChatModelInfo, type Issue } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { ConfirmAction, type ConfirmState } from './ConfirmAction'

const REPORT: ChatConfirmRequest = {
  tool: 'openloc_report_issue',
  args: {
    kind: 'bug', severity: 'critical', area: 'billing', title: 'Export does nothing',
    description: 'I click Export and nothing happens.', page: '/projects/1',
  },
  summary: 'Send “Export does nothing” to the support team.',
  token: 'tok', expires_at: '',
}

let qc: QueryClient
function renderCard(request = REPORT, onStateChange = vi.fn<(s: ConfirmState) => void>()) {
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/projects/12']}>
        <ConfirmAction item={{ request, state: 'pending' }} getConversationId={() => 7} onStateChange={onStateChange} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return onStateChange
}

beforeEach(() => {
  vi.mocked(createIssue).mockReset().mockResolvedValue({ id: 42, title: 'Export does nothing' } as Issue)
  vi.mocked(recordChatIssueReported).mockReset().mockResolvedValue({ ok: true, message: '' })
  vi.mocked(confirmChatAction).mockReset()
  vi.mocked(listChatModels).mockResolvedValue([])
  vi.mocked(getAppConfig).mockResolvedValue({
    version: 'abc123', app_url: '', docs_url: '', docs_host: false, developer_url: '', developer_host: false,
    playground: false, issues: true,
  })
})

describe('ConfirmAction for an issue report', () => {
  it('opens the report form filled in; the form sends it, marked as the assistant’s', async () => {
    // A usable key: outside the assistant's report the form would offer it.
    vi.mocked(listChatModels).mockResolvedValue([{ id: 'm', provider: 'claude', selectable: true, default: true } as ChatModelInfo])
    const onStateChange = renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Review and send' }))

    expect(screen.getByTestId('report-issue-title')).toHaveValue('Export does nothing')
    expect(screen.getByTestId('report-issue-description')).toHaveValue('I click Export and nothing happens.')
    expect(screen.getByRole('radio', { name: /Critical/ })).toBeChecked()
    expect(screen.getByTestId('report-issue-context')).toHaveTextContent('AI assistant')
    await waitFor(() => expect(qc.getQueryState(queryKeys.chat.models)?.status).toBe('success'))
    expect(screen.queryByTestId('report-issue-assistant')).toBeNull()

    await userEvent.click(screen.getByTestId('report-issue-submit'))
    await waitFor(() => expect(createIssue).toHaveBeenCalledTimes(1))
    const sent = vi.mocked(createIssue).mock.calls[0][0]
    // An area the form does not know falls back to its default.
    expect(sent).toMatchObject({
      kind: 'bug', severity: 'critical', area: 'other', title: 'Export does nothing',
      context: { path: '/projects/12', via: 'AI assistant' },
    })
    await waitFor(() => expect(onStateChange).toHaveBeenCalledWith('done'))
    expect(recordChatIssueReported).toHaveBeenCalledWith(7, 42)
    expect(confirmChatAction).not.toHaveBeenCalled()
  })

  it('stays pending when the form is closed without sending', async () => {
    const onStateChange = renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Review and send' }))
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(createIssue).not.toHaveBeenCalled()
    expect(onStateChange).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'Review and send' })).toBeInTheDocument()
  })

  it('keeps Review for any other tool', () => {
    renderCard({ ...REPORT, tool: 'openloc_delete_custom_palette' })
    expect(screen.getByRole('button', { name: 'Review' })).toBeInTheDocument()
  })
})
