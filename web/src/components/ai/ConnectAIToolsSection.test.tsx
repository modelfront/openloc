import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ConnectAIToolsSection } from './ConnectAIToolsSection'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const getAppConfig = vi.fn()
vi.mock('@/api', () => ({ getAppConfig: () => getAppConfig() }))

const CONFIG = {
  app_url: 'https://app.example.com/',
  docs_url: 'https://docs.example.com',
  docs_host: false,
  developer_url: '',
  developer_host: false,
  playground: false,
  issues: false,
}

// The section starts closed, so every test opens it from its real header.
async function renderSection() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const result = render(
    <QueryClientProvider client={client}>
      <ConnectAIToolsSection />
    </QueryClientProvider>,
  )
  await userEvent.click(screen.getByRole('button', { name: /^Connect an AI tool/ }))
  return result
}

beforeEach(() => {
  vi.clearAllMocks()
  getAppConfig.mockResolvedValue(CONFIG)
})

describe('ConnectAIToolsSection', () => {
  it('starts closed', () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <ConnectAIToolsSection />
      </QueryClientProvider>,
    )
    expect(screen.getByRole('button', { name: /^Connect an AI tool/ })).toBeInTheDocument()
    expect(screen.queryByTestId('mcp-server-url')).not.toBeInTheDocument()
  })

  // The address a tool needs is the app's own, not the page's: the app may be
  // opened under another name than BASE_URL.
  it('shows the MCP address on the app URL', async () => {
    await renderSection()
    expect(await screen.findByDisplayValue('https://app.example.com/mcp')).toHaveAttribute('readonly')
  })

  // Claude and ChatGPT are shortcuts, not the list of what works.
  it('says other MCP tools can connect, not only the two with links', async () => {
    await renderSection()
    expect(screen.getByTestId('connect-any-tool')).toHaveTextContent(/Most AI tools that support MCP can connect/)
    expect(screen.getByText('Direct links for two common tools')).toBeInTheDocument()
  })

  it('opens each tool where a custom connector is added, in a new tab', async () => {
    await renderSection()
    const claude = within(await screen.findByTestId('connect-claude')).getByRole('link', { name: /open claude connectors/i })
    expect(claude).toHaveAttribute('href', 'https://claude.ai/customize/connectors?modal=add-custom-connector')
    expect(claude).toHaveAttribute('target', '_blank')
    const chatgpt = within(screen.getByTestId('connect-chatgpt')).getByRole('link', { name: /open chatgpt plugins/i })
    expect(chatgpt).toHaveAttribute('href', 'https://chatgpt.com/plugins')
    expect(chatgpt).toHaveAttribute('rel', expect.stringContaining('noopener'))
  })

  // With a docs host the guide lives there; the link must not point at /docs
  // on the app, which only redirects.
  it('links the full guide on the docs site', async () => {
    await renderSection()
    await screen.findByDisplayValue('https://app.example.com/mcp')
    expect(screen.getByRole('link', { name: 'Connect external AI tools' })).toHaveAttribute('href', 'https://docs.example.com/guides/mcp')
  })

  it('falls back to this address before the config loads', async () => {
    getAppConfig.mockReturnValue(new Promise(() => {}))
    await renderSection()
    expect(screen.getByDisplayValue(`${window.location.origin}/mcp`)).toBeInTheDocument()
  })
})
