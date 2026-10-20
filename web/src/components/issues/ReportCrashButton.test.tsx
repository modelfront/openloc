// The crash screen's "Report this issue": offered to a signed-in member on a
// deployment that takes reports, outside the Playground, with the error as
// the bug's title.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof import('@/api')>('@/api')
  return { ...actual, getMe: vi.fn(), getAppConfig: vi.fn(), isLoggedIn: vi.fn(), listChatModels: vi.fn() }
})

import { getAppConfig, getMe, isLoggedIn, listChatModels, type AppConfig, type UserProfile } from '@/api'
import { ReportCrashButton } from './ReportCrashButton'

const CONFIG = {
  version: 'v1', app_url: '', docs_url: '', docs_host: false, developer_url: '', developer_host: false,
  playground: false, issues: true,
} as AppConfig

let qc: QueryClient
function renderButton(message = 'Cannot read properties of undefined') {
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter><ReportCrashButton error={new Error(message)} /></MemoryRouter>
    </QueryClientProvider>,
  )
}

/** Resolves once the config and the user have loaded, whatever they hold. */
const settled = () => waitFor(() => {
  expect(qc.isFetching()).toBe(0)
  expect(getAppConfig).toHaveBeenCalled()
})

beforeEach(() => {
  vi.mocked(isLoggedIn).mockReturnValue(true)
  vi.mocked(getAppConfig).mockResolvedValue(CONFIG)
  vi.mocked(getMe).mockResolvedValue({ id: 1, demo: false } as UserProfile)
  vi.mocked(listChatModels).mockResolvedValue([])
})

describe('ReportCrashButton', () => {
  it('opens the report form with the error as a bug title, cut to 200 characters', async () => {
    renderButton('x'.repeat(300))
    await userEvent.click(await screen.findByTestId('report-crash'))
    expect(screen.getByRole('radio', { name: /Bug/ })).toBeChecked()
    expect(screen.getByTestId('report-issue-title')).toHaveValue(`Crash: ${'x'.repeat(300)}`.slice(0, 200))
  })

  it.each([
    ['signed out', () => vi.mocked(isLoggedIn).mockReturnValue(false)],
    ['a deployment without reports', () => vi.mocked(getAppConfig).mockResolvedValue({ ...CONFIG, issues: false })],
    ['the Playground', () => vi.mocked(getMe).mockResolvedValue({ id: 1, demo: true } as UserProfile)],
  ])('is not offered when %s', async (_, setup) => {
    setup()
    renderButton()
    await settled()
    expect(screen.queryByTestId('report-crash')).toBeNull()
  })
})
