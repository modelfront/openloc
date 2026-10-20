import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import AISettingsPage from './AISettingsPage'
import { toast } from 'sonner'
import { ApiError } from '../api/request'

const toastInfo = vi.fn()
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn(), info: (...a: unknown[]) => toastInfo(...a) } }))

const listAIVendors = vi.fn()
const createVendorToken = vi.fn()
const createAIConnection = vi.fn()
const updateAIConnection = vi.fn()
const updateVendorToken = vi.fn()
const listAPITokens = vi.fn()
const listMTEngineCatalog = vi.fn()
const getMe = vi.fn()
const listKeylessEngines = vi.fn()
const attachKeyToEngines = vi.fn()
const listEnginesUsingKey = vi.fn()
const uploadExtension = vi.fn()
const deleteExtension = vi.fn()
vi.mock('../api', () => ({
  uploadExtension: (file: File) => uploadExtension(file),
  deleteExtension: (id: string) => deleteExtension(id),
  listKeylessEngines: (provider: unknown, id: unknown) => listKeylessEngines(provider, id),
  attachKeyToEngines: (provider: unknown, id: unknown, ids: unknown) => attachKeyToEngines(provider, id, ids),
  listEnginesUsingKey: (provider: unknown, id: unknown) => listEnginesUsingKey(provider, id),
  listAIVendors: () => listAIVendors(),
  createVendorToken: (provider: unknown, input: unknown) => createVendorToken(provider, input),
  updateVendorToken: (provider: unknown, id: unknown, input: unknown) => updateVendorToken(provider, id, input),
  deleteVendorToken: vi.fn(),
  createAIConnection: (provider: unknown, input: unknown) => createAIConnection(provider, input),
  updateAIConnection: (provider: unknown, id: unknown, input: unknown) => updateAIConnection(provider, id, input),
  deleteAIConnection: vi.fn(),
  listAPITokens: () => listAPITokens(),
  listMTEngineCatalog: () => listMTEngineCatalog(),
  createAPIToken: vi.fn(), revokeAPIToken: vi.fn(),
  getMe: () => getMe(),
  getAppConfig: () => Promise.resolve({ app_url: 'https://app.example.com', docs_url: '', docs_host: false, developer_url: '', developer_host: false, playground: false, issues: false }),
}))

const TOKEN_SETTING = { key: 'token', label: 'API token', secret: true }
function extensionVendor(settings: object[], extra: object = {}) {
  return {
    provider: 'ext:mockqe', display_name: 'Mock QE', connected: false, tokens: [], connections: [], models: [],
    extension: {
      id: 'mockqe', version: '1.0.0', capabilities: ['qe', 'ape'], settings,
      allowed_hosts: ['api.example.com', '{settings.base_url}'],
    },
    ...extra,
  }
}

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={qc}><AISettingsPage /></QueryClientProvider>)
}

// Opens a tile's panel and its Add key form.
async function openKeyForm(tile: string) {
  const user = userEvent.setup()
  renderPage()
  await user.click(await screen.findByTestId(tile))
  await user.click(await screen.findByTestId('ai-token-add'))
  return { user, form: await screen.findByTestId('ai-token-form') }
}

beforeEach(() => {
  vi.clearAllMocks()
  getMe.mockResolvedValue({ role: 'owner' })
  listAIVendors.mockResolvedValue([])
  createVendorToken.mockResolvedValue({ id: 1, name: 'Default', key_last4: 'a1b2', created_at: '2026-01-01' })
  updateVendorToken.mockResolvedValue({ id: 1, name: 'prod', key_last4: 'a1b2', created_at: '2026-01-01' })
  createAIConnection.mockResolvedValue({ id: 7, provider: 'openai', name: 'Default endpoint', base_url: '', token_count: 0, created_at: '2026-01-01' })
  updateAIConnection.mockResolvedValue({ id: 7, provider: 'openai', name: 'Default endpoint', base_url: '', token_count: 1, created_at: '2026-01-01' })
  listAPITokens.mockResolvedValue([])
  listKeylessEngines.mockResolvedValue([])
  attachKeyToEngines.mockResolvedValue({ attached: 1 })
  listEnginesUsingKey.mockResolvedValue([])
  // Built-in tiles render only for what the deployment OFFERS, which the MT
  // engine catalog answers, so the default fixture supplies both.
  listMTEngineCatalog.mockResolvedValue([
    { engine_type: 'deepl', name: 'DeepL', needs_token: true, params: [] },
    { engine_type: 'google', name: 'Google Translate', needs_token: true, params: [] },
    { engine_type: 'libretranslate', name: 'LibreTranslate', needs_token: false, params: [] },
    { engine_type: 'mock', name: 'Mock', needs_token: false, params: [] },
  ])
})

// The page holds surfaces with DIFFERENT access rules: the org's vendor API
// keys (owner/admin), and connecting the user's own AI tools (every role). The route is deliberately ungated — the split happens here.
describe('AISettingsPage', () => {
  it('shows all three sections to an admin, with only the vendor grid expanded', async () => {
    listAIVendors.mockResolvedValue([{ provider: 'openai', connected: true, tokens: [], connections: [{ id: 7, provider: 'openai', name: 'Default endpoint', base_url: '', token_count: 0, created_at: '2026-01-01' }], models: [] }])
    renderPage()
    expect(screen.getByRole('heading', { name: 'AI settings' })).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('ai-connections')).toBeInTheDocument())
    expect(screen.getByTestId('api-tokens-section')).toBeInTheDocument()
    // Access is present but CLOSED — connecting an agent is a
    // once-in-a-while task, unlike the vendor grid people come here for. Its
    // body is what proves the state; the section marker exists either way.
    expect(screen.queryByTestId('create-token-btn')).not.toBeInTheDocument()
    // Connect an AI tool is present but closed too.
    expect(screen.getByTestId('connect-ai-tools-section')).toBeInTheDocument()
    expect(screen.queryByTestId('mcp-server-url')).not.toBeInTheDocument()
  })

  // The reason the route isn't admin-gated: a linguist needs their own token to
  // drive the CAT-editor tools from an agent. Redirecting them would take that
  // away, and the backend still lets them hold one.
  it('shows a linguist their access tokens but not the org providers', async () => {
    getMe.mockResolvedValue({ role: 'linguist' })
    listAIVendors.mockResolvedValue([{ provider: 'openai', connected: true, tokens: [], connections: [{ id: 7, provider: 'openai', name: 'Default endpoint', base_url: '', token_count: 0, created_at: '2026-01-01' }], models: [] }])
    renderPage()
    await waitFor(() => expect(screen.getByTestId('api-tokens-section')).toBeInTheDocument())
    // The tokens section renders before the role arrives, so let getMe settle
    // and the page re-render before asserting what is absent.
    await waitFor(() => expect(getMe).toHaveBeenCalled())
    await act(async () => {
      await getMe.mock.results[0].value
      await new Promise(resolve => setTimeout(resolve, 0))
    })
    expect(screen.getByTestId('api-tokens-section')).toBeInTheDocument()
    // Connecting by sign-in is open to every role too.
    expect(screen.getByTestId('connect-ai-tools-section')).toBeInTheDocument()
    expect(screen.queryByTestId('ai-connections')).not.toBeInTheDocument()
    expect(screen.queryByText('Providers')).not.toBeInTheDocument()
    expect(listAIVendors).not.toHaveBeenCalled()
  })

  // The backend lists vendors to managers (RequireManager) but keeps every
  // write owner/admin, so a manager gets the grid with nothing to change.
  it('shows a project manager the providers read-only', async () => {
    getMe.mockResolvedValue({ role: 'project_manager' })
    listAIVendors.mockResolvedValue([{ provider: 'openai', connected: true, tokens: [{ id: 1, name: 'prod', key_last4: 'a1b2', connection_id: 7, created_at: '2026-01-01' }], connections: [{ id: 7, provider: 'openai', name: 'Default endpoint', base_url: '', token_count: 1, created_at: '2026-01-01' }], models: [] }])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-7'))
    const panel = await screen.findByTestId('ai-connection-panel')
    expect(within(panel).getByTestId('ai-token-row-1')).toHaveTextContent('prod')
    expect(within(panel).queryByTestId('ai-connection-form')).not.toBeInTheDocument()
    expect(within(panel).queryByTestId('ai-token-add')).not.toBeInTheDocument()
    expect(within(panel).queryByRole('button', { name: 'Remove' })).not.toBeInTheDocument()
    expect(screen.queryByTestId('ai-custom-provider-add')).not.toBeInTheDocument()
  })

  // Connections from every vendor share ONE grid, and EVERY known vendor has a
  // tile whether or not it is configured — an unconfigured vendor that rendered
  // nothing would be undiscoverable, which is how you end up believing the app
  // only supports the two vendors somebody already set up.
  it('renders a tile for every vendor, configured or not', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'openai', connected: true, tokens: [{ id: 1, name: 'prod', key_last4: 'a1b2', connection_id: 7, created_at: '2026-01-01' }], connections: [{ id: 7, provider: 'openai', name: 'OpenAI prod', base_url: '', token_count: 1, created_at: '2026-01-01' }], models: [] },
      { provider: 'claude', connected: false, tokens: [], connections: [{ id: 8, provider: 'claude', name: 'Claude eu', base_url: '', token_count: 0, created_at: '2026-01-01' }], models: [] },
    ])
    renderPage()
    await waitFor(() => expect(screen.getByTestId('ai-connection-tile-7')).toBeInTheDocument())
    expect(screen.getByTestId('ai-connection-tile-8')).toBeInTheDocument()
    // DeepL has no connection, so it gets a placeholder tile rather than
    // vanishing from the grid — carrying the vendor name and nothing else.
    const unconfigured = screen.getByTestId('ai-connection-tile-new-deepl')
    expect(within(unconfigured).getByText('DeepL')).toBeInTheDocument()
    // A disabled connection carries NO status text either — the dimmed tile is
    // the whole signal.
    expect(within(screen.getByTestId('ai-connection-tile-8')).queryByText(/disabled/i)).not.toBeInTheDocument()
  })

  // The vendor is carried by the tile, so the form must not ask again — a picker
  // here is how a connection gets created against the wrong vendor.
  it('has no vendor picker in the connection form', async () => {
    listAIVendors.mockResolvedValue([{ provider: 'openai', connected: false, tokens: [], connections: [], models: [] }])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-new-claude'))
    const form = await screen.findByTestId('ai-connection-form')
    expect(within(form).queryByTestId('ai-connection-provider')).not.toBeInTheDocument()
    expect(within(form).queryByText('Vendor')).not.toBeInTheDocument()
  })

  // Clicking an unconfigured tile creates against THAT vendor. The provider is
  // the first argument to the API call, so a regression here is silent.
  it('creates against the vendor whose tile was clicked', async () => {
    listAIVendors.mockResolvedValue([{ provider: 'openai', connected: false, tokens: [], connections: [], models: [] }])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-new-claude'))
    const form = await screen.findByTestId('ai-connection-form')
    await user.click(within(form).getByRole('button', { name: 'Update' }))
    await waitFor(() => expect(createAIConnection).toHaveBeenCalled())
    expect(createAIConnection.mock.calls[0][0]).toBe('claude')
  })

  // LibreTranslate ships configured by the deployment. It must read as live
  // without anyone entering anything, and — the standing rule — must never
  // offer a field for its API key or URL.
  it('shows LibreTranslate as built in, with nothing to configure', async () => {
    listAIVendors.mockResolvedValue([{ provider: 'openai', connected: false, tokens: [], connections: [], models: [] }])
    const user = userEvent.setup()
    renderPage()
    const tile = await screen.findByTestId('ai-connection-tile-new-libretranslate')
    // Live on arrival: tinted, not the dashed treatment of an unconfigured one.
    expect(tile.className).toContain('border-primary/50')
    expect(within(tile).getByText('No key required')).toBeInTheDocument()

    await user.click(tile)
    const panel = await screen.findByTestId('ai-connection-panel')
    expect(within(panel).getByTestId('ai-builtin-note')).toBeInTheDocument()
    // No endpoint field, no key list, and above all no way to add a token.
    expect(within(panel).queryByTestId('ai-connection-base-url')).not.toBeInTheDocument()
    expect(within(panel).queryByTestId('ai-token-add')).not.toBeInTheDocument()
    expect(within(panel).queryByTestId('ai-tokens')).not.toBeInTheDocument()
    // It works today, so it must NOT be written off as unbuilt.
    expect(toastInfo).not.toHaveBeenCalled()
  })

  // A built-in tile is only honest if the deployment actually offers the engine:
  // a deployment with no LIBRETRANSLATE_URL must not show a vendor nothing
  // backs, and mock must stay hidden in production. The catalog is what answers
  // that — an engine ROW would be the wrong source, since an org may delete its
  // LibreTranslate engine and build another later.
  it('hides a built-in vendor the deployment does not offer', async () => {
    listAIVendors.mockResolvedValue([{ provider: 'openai', connected: false, tokens: [], connections: [], models: [] }])
    listMTEngineCatalog.mockResolvedValue([
      { engine_type: 'deepl', name: 'DeepL', needs_token: true, params: [] },
      { engine_type: 'libretranslate', name: 'LibreTranslate', needs_token: false, params: [] },
    ])
    renderPage()
    const mt = await screen.findByTestId('ai-connection-group-mt')
    await waitFor(() => expect(within(mt).getByText('LibreTranslate')).toBeInTheDocument())
    // Not in the catalog → no mock tile.
    expect(within(mt).queryByText('Mock')).not.toBeInTheDocument()
    // The configurable vendors are unaffected — they do not depend on the
    // built-in half of the catalog.
    expect(within(mt).getByText('DeepL')).toBeInTheDocument()
  })

  // An installed extension joins the QE+APE row under its manifest name, and
  // its panel asks only for what the manifest declares: a key labelled by the
  // `token` setting, and no endpoint without a `base_url` setting.
  it('gives an extension without a base_url setting a keys-only panel', async () => {
    listAIVendors.mockResolvedValue([extensionVendor([TOKEN_SETTING], {
      tokens: [{ id: 5, name: 'Default', key_last4: 'z9y8', connection_id: 9, created_at: '2026-01-01' }],
      connections: [{ id: 9, provider: 'ext:mockqe', name: 'Mock QE', base_url: '', token_count: 1, created_at: '2026-01-01' }],
    })])
    const user = userEvent.setup()
    renderPage()
    const tile = await screen.findByTestId('ai-connection-tile-9')
    expect(within(screen.getByTestId('ai-connection-group-qe')).getByTestId('ai-connection-tile-9')).toBe(tile)
    expect(within(tile).getByText('Mock QE')).toBeInTheDocument()
    await user.click(tile)
    const panel = await screen.findByTestId('ai-connection-panel')

    expect(within(panel).queryByTestId('ai-connection-base-url')).not.toBeInTheDocument()
    expect(within(panel).getByTestId('ai-token-row-5')).toBeInTheDocument()
    await user.click(within(panel).getByTestId('ai-token-add'))
    expect(within(await screen.findByTestId('ai-token-form')).getByText('API token')).toBeInTheDocument()
  })

  it('prefills an extension endpoint with its manifest default', async () => {
    listAIVendors.mockResolvedValue([extensionVendor([TOKEN_SETTING, { key: 'base_url', label: 'Endpoint', default: 'https://api.example.com' }])])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-new-ext:mockqe'))
    const form = await screen.findByTestId('ai-connection-form')
    expect(within(form).getByText('Endpoint')).toBeInTheDocument()
    expect(within(form).getByTestId('ai-connection-base-url')).toHaveValue('https://api.example.com')
  })

  // The key goes to the extension's own provider, which is how the backend
  // knows to check it with the extension.
  it('adds an extension key against its ext: provider', async () => {
    listAIVendors.mockResolvedValue([extensionVendor([TOKEN_SETTING])])
    createAIConnection.mockResolvedValue({ id: 9, provider: 'ext:mockqe', name: 'Mock QE', base_url: '', token_count: 0, created_at: '2026-01-01' })
    const { user, form } = await openKeyForm('ai-connection-tile-new-ext:mockqe')
    await user.type(within(form).getByTestId('ai-token-api-key'), 'secret-key')
    await user.click(within(form).getByRole('button', { name: 'Add' }))
    await waitFor(() => expect(createVendorToken).toHaveBeenCalled())
    expect(createAIConnection.mock.calls[0][0]).toBe('ext:mockqe')
    expect(createVendorToken.mock.calls[0][0]).toBe('ext:mockqe')
    expect(createVendorToken.mock.calls[0][1]).toMatchObject({ api_key: 'secret-key', connection_id: 9 })
  })

  // The catalogued vendors keep the field.
  it('keeps the endpoint field for the other vendors', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'openai', connected: true, tokens: [], connections: [{ id: 7, provider: 'openai', name: 'OpenAI prod', base_url: '', token_count: 1, created_at: '2026-01-01' }], models: [] },
    ])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-7'))
    const panel = await screen.findByTestId('ai-connection-panel')
    expect(within(panel).getByTestId('ai-connection-base-url')).toBeInTheDocument()
  })

  // The key count is the one thing a tile says that you cannot learn without
  // opening the vendor. Singular vs plural because "1 tokens" is the kind of
  // detail that survives forever once shipped.
  it.each([
    { count: 1, expected: '1 key' },
    { count: 3, expected: '3 keys' },
  ])('shows "$expected" under the vendor name for $count', async ({ count, expected }) => {
    listAIVendors.mockResolvedValue([
      { provider: 'openai', connected: true, tokens: [], connections: [{ id: 7, provider: 'openai', name: 'OpenAI prod', base_url: '', token_count: count, created_at: '2026-01-01' }], models: [] },
    ])
    renderPage()
    const tile = await screen.findByTestId('ai-connection-tile-7')
    expect(within(tile).getByText(expected)).toBeInTheDocument()
  })

  // A keyless connection says nothing — the dimmed tile is the whole signal, and
  // "0 tokens" would be placeholder noise.
  it('says nothing under the name when a connection has no keys', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'openai', connected: false, tokens: [], connections: [{ id: 7, provider: 'openai', name: 'OpenAI prod', base_url: '', token_count: 0, created_at: '2026-01-01' }], models: [] },
    ])
    renderPage()
    const tile = await screen.findByTestId('ai-connection-tile-7')
    expect(within(tile).queryByText(/key/i)).not.toBeInTheDocument()
    expect(within(tile).getByText('OpenAI prod')).toBeInTheDocument()
  })

  // A connection is live BECAUSE it has a key — there is no separate switch.
  // `aria-pressed` is not it (that is selection); the lit/dim treatment is, and
  // it must follow token_count.
  it('lights a tile only when its connection has a key', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'openai', connected: true, tokens: [], connections: [{ id: 7, provider: 'openai', name: 'OpenAI prod', base_url: '', token_count: 1, created_at: '2026-01-01' }], models: [] },
      { provider: 'claude', connected: false, tokens: [], connections: [{ id: 8, provider: 'claude', name: 'Claude prod', base_url: '', token_count: 0, created_at: '2026-01-01' }], models: [] },
    ])
    renderPage()
    const keyed = await screen.findByTestId('ai-connection-tile-7')
    const keyless = screen.getByTestId('ai-connection-tile-8')
    // The keyed one is tinted; the keyless one is dashed-and-faded like an
    // unconfigured vendor, because it is just as uncallable.
    expect(keyed.className).toContain('border-primary/50')
    expect(keyless.className).toContain('border-dashed')
    expect(keyless.className).not.toContain('border-primary/50')
  })

  // A single-field form with one action should not need the mouse.
  it('submits the endpoint on Enter', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'openai', connected: true, tokens: [], connections: [{ id: 7, provider: 'openai', name: 'OpenAI prod', base_url: '', token_count: 1, created_at: '2026-01-01' }], models: [] },
    ])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-7'))
    const field = await screen.findByTestId('ai-connection-base-url')

    await user.click(field)
    await user.keyboard('https://gw.example.com{Enter}')

    await waitFor(() => expect(updateAIConnection).toHaveBeenCalled())
    expect(updateAIConnection.mock.calls[0][2]).toMatchObject({ base_url: 'https://gw.example.com' })
  })

  // The keys list is part of the panel, not a reward for saving the endpoint
  // first. It renders for a vendor that has no connection row at all.
  it('shows the keys list even for a vendor with no connection', async () => {
    listAIVendors.mockResolvedValue([{ provider: 'openai', connected: false, tokens: [], connections: [], models: [] }])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-new-claude'))
    const panel = await screen.findByTestId('ai-connection-panel')
    expect(within(panel).getByTestId('ai-tokens')).toBeInTheDocument()
    expect(within(panel).getByTestId('ai-token-add')).toBeInTheDocument()
  })

  // Adding the first key to a fresh vendor creates the connection row behind
  // it. The row is plumbing — the user asked for a key, not a connection — so
  // it must not become a required first step.
  it('creates the connection implicitly when the first key is added', async () => {
    listAIVendors.mockResolvedValue([{ provider: 'openai', connected: false, tokens: [], connections: [], models: [] }])
    createAIConnection.mockResolvedValue({ id: 42, provider: 'claude', name: 'Claude', base_url: '', token_count: 0, created_at: '2026-01-01' })
    const { user, form } = await openKeyForm('ai-connection-tile-new-claude')

    // No picker: there is nothing to choose between yet.
    expect(within(form).queryByTestId('ai-token-connection')).not.toBeInTheDocument()

    await user.type(within(form).getByPlaceholderText('sk-…'), 'sk-secret')
    await user.click(within(form).getByRole('button', { name: 'Add' }))

    await waitFor(() => expect(createVendorToken).toHaveBeenCalled())
    expect(createAIConnection).toHaveBeenCalledWith('claude', {})
    // The key lands on the row that was just created for it.
    expect(createVendorToken.mock.calls[0][1]).toMatchObject({ connection_id: 42 })

    // A second key on the same panel reuses that row. The provider list has not
    // refetched, so the connection is still invisible to the panel — if the
    // only guard is "did the list come back with one", every further key mints
    // another empty connection and the provider grid grows a duplicate tile.
    await user.click(await screen.findByTestId('ai-token-add'))
    const again = await screen.findByTestId('ai-token-form')
    await user.type(within(again).getByPlaceholderText('sk-…'), 'sk-second')
    await user.click(within(again).getByRole('button', { name: 'Add' }))

    await waitFor(() => expect(createVendorToken).toHaveBeenCalledTimes(2))
    expect(createAIConnection).toHaveBeenCalledTimes(1)
    expect(createVendorToken.mock.calls[1][1]).toMatchObject({ connection_id: 42 })
  })

  // Update is for the endpoint and nothing else — it must not be the thing that
  // brings a connection into existence, nor touch the name.
  it('sends only the endpoint on Update', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'openai', connected: true, tokens: [], connections: [{ id: 7, provider: 'openai', name: 'OpenAI prod', base_url: '', token_count: 1, created_at: '2026-01-01' }], models: [] },
    ])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-7'))
    const form = await screen.findByTestId('ai-connection-form')
    await user.type(within(form).getByTestId('ai-connection-base-url'), 'https://gw.example.com')
    await user.click(within(form).getByRole('button', { name: 'Update' }))

    await waitFor(() => expect(updateAIConnection).toHaveBeenCalled())
    expect(updateAIConnection.mock.calls[0][2]).toEqual({ base_url: 'https://gw.example.com' })
    expect(createAIConnection).not.toHaveBeenCalled()
  })

  // The panel is one connection of one vendor, so the token form must not ask
  // which connection the key is for — you answered that by clicking the tile.
  // Re-asking reads as if the click did not register, and the picker had
  // exactly one right answer anyway.
  it('does not ask which connection a new key belongs to', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'openai', connected: true, tokens: [], connections: [{ id: 7, provider: 'openai', name: 'OpenAI prod', base_url: '', token_count: 1, created_at: '2026-01-01' }], models: [] },
      { provider: 'claude', connected: true, tokens: [], connections: [{ id: 8, provider: 'claude', name: 'Claude prod', base_url: '', token_count: 1, created_at: '2026-01-01' }], models: [] },
    ])
    const { form } = await openKeyForm('ai-connection-tile-7')

    expect(within(form).queryByTestId('ai-token-connection')).not.toBeInTheDocument()
    expect(within(form).queryByText('Connection')).not.toBeInTheDocument()
    // Name and the key are all that is left to answer.
    expect(within(form).getAllByRole('textbox')).toHaveLength(1)
    expect(within(form).getByPlaceholderText('sk-…')).toBeInTheDocument()
  })

  // ...and the key still lands on the connection whose panel it was added from,
  // even though nothing asked.
  it('attaches a new key to the panel\'s own connection', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'claude', connected: true, tokens: [], connections: [{ id: 8, provider: 'claude', name: 'Claude prod', base_url: '', token_count: 1, created_at: '2026-01-01' }], models: [] },
    ])
    const { user, form } = await openKeyForm('ai-connection-tile-8')
    await user.type(within(form).getByPlaceholderText('sk-…'), 'sk-secret')
    await user.click(within(form).getByRole('button', { name: 'Add' }))

    await waitFor(() => expect(createVendorToken).toHaveBeenCalled())
    expect(createVendorToken.mock.calls[0][0]).toBe('claude')
    expect(createVendorToken.mock.calls[0][1]).toMatchObject({ connection_id: 8 })
  })

  // Pins that the chat key is chosen in the token form, and only on vendors
  // chat can drive — a checkbox on an MT vendor would change nothing.
  it('offers the chat choice on a chat vendor and sends it', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'claude', connected: true, tokens: [], connections: [{ id: 8, provider: 'claude', name: 'Claude prod', base_url: '', token_count: 1, created_at: '2026-01-01' }], models: [] },
    ])
    const { user, form } = await openKeyForm('ai-connection-tile-8')

    await user.type(within(form).getByPlaceholderText('sk-…'), 'sk-secret')
    await user.click(within(form).getByTestId('ai-token-use-for-chat'))
    await user.click(within(form).getByRole('button', { name: 'Add' }))

    await waitFor(() => expect(createVendorToken).toHaveBeenCalled())
    expect(createVendorToken.mock.calls[0][1]).toMatchObject({ use_for_chat: true })
  })

  // Left unchecked the field is OMITTED, not sent false: a vendor with keys
  // always has a chat key, so "false" is not a state the API should receive.
  it('omits the chat choice entirely when left unchecked', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'claude', connected: true, tokens: [], connections: [{ id: 8, provider: 'claude', name: 'Claude prod', base_url: '', token_count: 1, created_at: '2026-01-01' }], models: [] },
    ])
    const { user, form } = await openKeyForm('ai-connection-tile-8')
    await user.type(within(form).getByPlaceholderText('sk-…'), 'sk-secret')
    await user.click(within(form).getByRole('button', { name: 'Add' }))

    await waitFor(() => expect(createVendorToken).toHaveBeenCalled())
    expect(createVendorToken.mock.calls[0][1]).not.toHaveProperty('use_for_chat')
  })

  // An extension only reviews and the MT vendors translate — chat cannot drive
  // either, so the control must not appear there.
  it.each(['ext:mockqe', 'deepl'])('hides the chat choice on %s', async provider => {
    const connections = [{ id: 9, provider, name: 'Conn', base_url: '', token_count: 1, created_at: '2026-01-01' }]
    listAIVendors.mockResolvedValue([
      provider === 'ext:mockqe'
        ? extensionVendor([TOKEN_SETTING], { connected: true, connections })
        : { provider, connected: true, tokens: [], connections, models: [] },
    ])
    const { form } = await openKeyForm('ai-connection-tile-9')
    expect(within(form).queryByTestId('ai-token-use-for-chat')).not.toBeInTheDocument()
  })

  // Which key chat spends is invisible from the list otherwise — and with
  // several keys on one vendor, that answer is the whole point of the choice.
  it('marks the chat key in the token list', async () => {
    listAIVendors.mockResolvedValue([
      {
        provider: 'claude', connected: true,
        tokens: [
          { id: 1, name: 'cheap', key_last4: 'a1b2', connection_id: 8, use_for_chat: false, created_at: '2026-01-01' },
          { id: 2, name: 'prod', key_last4: 'c3d4', connection_id: 8, use_for_chat: true, created_at: '2026-01-02' },
        ],
        connections: [{ id: 8, provider: 'claude', name: 'Claude prod', base_url: '', token_count: 2, created_at: '2026-01-01' }],
        models: [],
      },
    ])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-8'))

    expect(await screen.findByTestId('ai-token-chat-badge-2')).toBeInTheDocument()
    // ...and only on the one that carries it.
    expect(screen.queryByTestId('ai-token-chat-badge-1')).not.toBeInTheDocument()
  })

  // A vendor chat cannot drive must not carry the badge, even if the flag is
  // somehow set — it would point at a surface that vendor does not have.
  it('never marks a chat key on a non-chat vendor', async () => {
    listAIVendors.mockResolvedValue([
      {
        ...extensionVendor([TOKEN_SETTING]),
        tokens: [{ id: 5, name: 'ext', key_last4: 'z9y8', connection_id: 9, use_for_chat: true, created_at: '2026-01-01' }],
        connections: [{ id: 9, provider: 'ext:mockqe', name: 'Mock QE', base_url: '', token_count: 1, created_at: '2026-01-01' }],
      },
    ])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-9'))
    await screen.findByTestId('ai-token-row-5')
    expect(screen.queryByTestId('ai-token-chat-badge-5')).not.toBeInTheDocument()
  })

  // Every vendor holds as many keys as the org has accounts, so the copy is
  // plural throughout and the Add control is always there.
  it.each(['deepl', 'openai'])('offers an unlimited key list on %s', async provider => {
    listAIVendors.mockResolvedValue([
      { provider, connected: false, tokens: [], connections: [{ id: 9, provider, name: 'Conn', base_url: '', token_count: 0, created_at: '2026-01-01' }], models: [] },
    ])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-9'))
    const keys = await screen.findByTestId('ai-tokens')

    expect(within(keys).getByText('Keys')).toBeInTheDocument()
    expect(within(keys).getByText('No API keys yet.')).toBeInTheDocument()
    expect(within(keys).getByTestId('ai-token-add')).toHaveTextContent('Add key')
  })

  // The Add control survives the first key on an MT vendor: a second DeepL
  // account is a second engine, not a rejected request.
  it('still offers Add key after an MT vendor has one', async () => {
    listAIVendors.mockResolvedValue([
      {
        provider: 'deepl', connected: true,
        tokens: [{ id: 4, name: 'Default', key_last4: 'a1b2', connection_id: 9, use_for_chat: false, created_at: '2026-01-01' }],
        connections: [{ id: 9, provider: 'deepl', name: 'DeepL', base_url: '', token_count: 1, created_at: '2026-01-01' }],
        models: [],
      },
    ])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-9'))
    await screen.findByTestId('ai-token-row-4')
    expect(screen.getByTestId('ai-token-add')).toBeInTheDocument()
  })

  // The count is a real tally on every vendor, so an MT tile reports it like
  // any other.
  it('counts the keys on an MT tile', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'deepl', connected: true, tokens: [], connections: [{ id: 9, provider: 'deepl', name: 'DeepL', base_url: '', token_count: 2, created_at: '2026-01-01' }], models: [] },
    ])
    renderPage()
    const tile = await screen.findByTestId('ai-connection-tile-9')
    expect(within(tile).getByText('2 keys')).toBeInTheDocument()
  })

  // ...and a keyless vendor says nothing at all, like every other unconfigured
  // tile.
  it('says nothing for a vendor with no key', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'deepl', connected: false, tokens: [], connections: [{ id: 9, provider: 'deepl', name: 'DeepL', base_url: '', token_count: 0, created_at: '2026-01-01' }], models: [] },
    ])
    renderPage()
    const tile = await screen.findByTestId('ai-connection-tile-9')
    expect(within(tile).queryByText(/\bkeys?\b/i)).not.toBeInTheDocument()
    expect(within(tile).getByText('DeepL')).toBeInTheDocument()
  })

  // The name is the only way to say which key is which — and on an MT vendor it
  // also names the engine the key projects into.
  it.each(['deepl', 'openai'])('%s token form asks for a name', async provider => {
    listAIVendors.mockResolvedValue([
      { provider, connected: false, tokens: [], connections: [{ id: 9, provider, name: 'Conn', base_url: '', token_count: 0, created_at: '2026-01-01' }], models: [] },
    ])
    const { form } = await openKeyForm('ai-connection-tile-9')

    expect(within(form).getByPlaceholderText('Default')).toBeInTheDocument()
  })

  // One field, and it is the endpoint: the name is derived from the vendor, and
  // a connection is live when it has a key rather than when a switch says so.
  it('offers only the endpoint field', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'openai', connected: true, tokens: [], connections: [{ id: 7, provider: 'openai', name: 'OpenAI prod', base_url: '', token_count: 0, created_at: '2026-01-01' }], models: [] },
    ])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-7'))
    const form = await screen.findByTestId('ai-connection-form')
    expect(within(form).getAllByRole('textbox')).toHaveLength(1)
    expect(within(form).getByTestId('ai-connection-base-url')).toBeInTheDocument()
    expect(within(form).queryByTestId('ai-connection-enabled')).not.toBeInTheDocument()
  })

  // The vendor list is split by capability: things that can run QE+APE, and
  // machine translation engines.
  it('splits vendors into a QE+APE group and an MT group', async () => {
    listAIVendors.mockResolvedValue([{ provider: 'openai', connected: false, tokens: [], connections: [], models: [] }])
    renderPage()
    const qe = await screen.findByTestId('ai-connection-group-qe')
    const mt = screen.getByTestId('ai-connection-group-mt')
    for (const name of ['OpenAI', 'Claude']) {
      expect(within(qe).getByText(name)).toBeInTheDocument()
    }
    // Built-in tiles come from a SECOND query (the engine list), so they land a
    // tick after the vendor tiles — wait for one before asserting the rest.
    await waitFor(() => expect(within(mt).getByText('LibreTranslate')).toBeInTheDocument())
    for (const name of ['DeepL', 'Google Translate', 'Mock']) {
      expect(within(mt).getByText(name)).toBeInTheDocument()
    }
    // An MT engine must not leak into the QE+APE row and vice versa.
    expect(within(qe).queryByText('DeepL')).not.toBeInTheDocument()
    expect(within(mt).queryByText('Claude')).not.toBeInTheDocument()
  })

  // Mock is offered where the deployment provisions it, and it takes no
  // credential — the same shape as LibreTranslate. Whether it appears at all is
  // covered by "hides a built-in vendor the deployment does not offer";
  // production never provisions it (`mtdefaults`).
  it('offers Mock as a built-in with nothing to configure', async () => {
    listAIVendors.mockResolvedValue([{ provider: 'openai', connected: false, tokens: [], connections: [], models: [] }])
    const user = userEvent.setup()
    renderPage()
    const tile = await screen.findByTestId('ai-connection-tile-new-mock')
    expect(within(tile).getByText('No key required')).toBeInTheDocument()

    await user.click(tile)
    const panel = await screen.findByTestId('ai-connection-panel')
    expect(within(panel).getByTestId('ai-builtin-note')).toBeInTheDocument()
    expect(within(panel).queryByTestId('ai-connection-base-url')).not.toBeInTheDocument()
    expect(within(panel).queryByTestId('ai-token-add')).not.toBeInTheDocument()
  })

  // Every tile must lead somewhere. Each one either opens a panel (wired, or
  // built-in) — none is a dead end that only raises "under development". That
  // toast is kept as a runtime backstop for a catalog entry added ahead of its
  // Go integration; today no vendor should reach it.
  it('opens a panel for every vendor, with none written off as unbuilt', async () => {
    listAIVendors.mockResolvedValue([{ provider: 'openai', connected: false, tokens: [], connections: [], models: [] }])
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('ai-connections')

    for (const id of ['openai', 'claude', 'deepl', 'google', 'libretranslate']) {
      await user.click(screen.getByTestId(`ai-connection-tile-new-${id}`))
      expect(await screen.findByTestId('ai-connection-panel')).toBeInTheDocument()
      // Close it again so the next iteration starts clean.
      await user.click(screen.getByTestId(`ai-connection-tile-new-${id}`))
    }
    expect(toastInfo).not.toHaveBeenCalled()
  })

  // On a grid where most tiles are unconfigured, a "Not configured" / "No key
  // yet" line on every one is noise, and the dimmed treatment already carries
  // the meaning.
  it('shows no placeholder text on unconfigured tiles and no Connections heading', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'openai', connected: true, tokens: [], connections: [{ id: 7, provider: 'openai', name: 'OpenAI prod', base_url: '', token_count: 0, created_at: '2026-01-01' }], models: [] },
    ])
    renderPage()
    await screen.findByTestId('ai-connections')
    expect(screen.queryByText(/not configured/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/no key yet/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/^connections$/i)).not.toBeInTheDocument()
    // A custom endpoint is the ONLY thing a tile ever says under the name.
    expect(within(screen.getByTestId('ai-connections')).queryByText(/^disabled$/i)).not.toBeInTheDocument()
  })

  // Saving must NOT close the panel: the keys list is right below the endpoint
  // and is usually the next thing you touch, so closing would make you re-open
  // the vendor to carry on.
  it('keeps the panel open after Update', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'openai', connected: true, tokens: [], connections: [{ id: 7, provider: 'openai', name: 'OpenAI prod', base_url: '', token_count: 0, created_at: '2026-01-01' }], models: [] },
    ])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-7'))
    const form = await screen.findByTestId('ai-connection-form')

    await user.click(within(form).getByRole('button', { name: 'Update' }))
    await waitFor(() => expect(updateAIConnection).toHaveBeenCalled())
    expect(screen.getByTestId('ai-connection-panel')).toBeInTheDocument()
  })

  // The tile is the toggle, which is what lets the form drop its Cancel button.
  it('closes the panel when the open vendor is clicked again', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'openai', connected: true, tokens: [], connections: [{ id: 7, provider: 'openai', name: 'OpenAI prod', base_url: '', token_count: 0, created_at: '2026-01-01' }], models: [] },
    ])
    const user = userEvent.setup()
    renderPage()
    const tile = await screen.findByTestId('ai-connection-tile-7')

    await user.click(tile)
    expect(await screen.findByTestId('ai-connection-panel')).toBeInTheDocument()

    await user.click(tile)
    await waitFor(() => expect(screen.queryByTestId('ai-connection-panel')).not.toBeInTheDocument())
  })

  // Escape is the other way out. It must work from inside the panel AND from
  // the tile that opened it.
  it.each([
    { from: 'inside the panel', target: () => screen.getByTestId('ai-connection-base-url') },
    { from: 'the tile that opened it', target: () => screen.getByTestId('ai-connection-tile-7') },
  ])('closes the panel on Escape from $from', async ({ target }) => {
    listAIVendors.mockResolvedValue([
      { provider: 'openai', connected: true, tokens: [], connections: [{ id: 7, provider: 'openai', name: 'OpenAI prod', base_url: '', token_count: 0, created_at: '2026-01-01' }], models: [] },
    ])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-7'))
    await screen.findByTestId('ai-connection-panel')

    target().focus()
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByTestId('ai-connection-panel')).not.toBeInTheDocument())
  })

  // ...but NOT from a tile that opened nothing. Escape there is a stray
  // keystroke, and closing on it would make the panel feel like it drops out at
  // random while tabbing across the grid.
  it('leaves the panel open on Escape from a different vendor tile', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'openai', connected: true, tokens: [], connections: [{ id: 7, provider: 'openai', name: 'OpenAI prod', base_url: '', token_count: 0, created_at: '2026-01-01' }], models: [] },
    ])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-7'))
    await screen.findByTestId('ai-connection-panel')

    screen.getByTestId('ai-connection-tile-new-claude').focus()
    await user.keyboard('{Escape}')
    expect(screen.getByTestId('ai-connection-panel')).toBeInTheDocument()
  })

  // Keys live inside their connection's panel, so they are hidden until you open
  // that vendor — and a panel shows ONLY its own connection's keys.
  it('shows a connection\'s keys inside its panel, and only its own', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'openai', connected: true, tokens: [{ id: 1, name: 'openai-key', key_last4: 'a1b2', connection_id: 7, created_at: '2026-01-01' }], connections: [{ id: 7, provider: 'openai', name: 'OpenAI prod', base_url: '', token_count: 1, created_at: '2026-01-01' }], models: [] },
      { provider: 'claude', connected: true, tokens: [{ id: 2, name: 'claude-key', key_last4: 'c3d4', connection_id: 8, created_at: '2026-01-01' }], connections: [{ id: 8, provider: 'claude', name: 'Claude prod', base_url: '', token_count: 1, created_at: '2026-01-01' }], models: [] },
    ])
    const user = userEvent.setup()
    renderPage()
    // Nothing is expanded, so no keys are on screen at all. Wait on the TILE,
    // not the grid container — the container renders before the fetch resolves,
    // so a wait on it would run these assertions a tick too early.
    const tile = await screen.findByTestId('ai-connection-tile-7')
    expect(screen.queryByTestId('ai-token-row-1')).not.toBeInTheDocument()

    await user.click(tile)
    const panel = await screen.findByTestId('ai-connection-panel')
    expect(within(panel).getByTestId('ai-token-row-1')).toBeInTheDocument()
    // Claude's key must NOT leak into OpenAI's panel.
    expect(screen.queryByTestId('ai-token-row-2')).not.toBeInTheDocument()
  })

  it('opens the add-token form inside the vendor panel', async () => {
    listAIVendors.mockResolvedValue([{ provider: 'openai', connected: false, tokens: [], connections: [{ id: 7, provider: 'openai', name: 'Default endpoint', base_url: '', token_count: 0, created_at: '2026-01-01' }], models: [] }])
    const { form } = await openKeyForm('ai-connection-tile-7')
    expect(form).toBeInTheDocument()
  })

  it('shows the stored key hint when editing a vendor token', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'openai', connected: true, tokens: [{ id: 1, name: 'prod', key_last4: 'a1b2', connection_id: 7, created_at: '2026-01-01' }], connections: [{ id: 7, provider: 'openai', name: 'Default endpoint', base_url: '', token_count: 1, created_at: '2026-01-01' }], models: [] },
    ])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-7'))
    await user.click(await screen.findByText('prod'))
    const form = await screen.findByTestId('ai-token-form')
    // Blank field keeps the current key; its placeholder reveals the last 4 chars.
    expect(within(form).getByPlaceholderText(/a1b2$/)).toBeInTheDocument()
  })

  // The endpoint lives on the connection, not the token.
  // Editing it must round-trip: an edit that doesn't touch it must still send
  // the stored value back, or the tenant silently reverts to api.openai.com
  it('connection endpoint round-trips through the edit form', async () => {
    const azure = 'https://acme.openai.azure.com/openai'
    listAIVendors.mockResolvedValue([
      {
        provider: 'openai', connected: true,
        tokens: [{ id: 1, name: 'prod', key_last4: 'a1b2', connection_id: 7, base_url: azure, created_at: '2026-01-01' }],
        connections: [{ id: 7, provider: 'openai', name: 'Azure Acme', base_url: azure, token_count: 1, created_at: '2026-01-01' }],
        models: [],
      },
    ])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-7'))
    const form = await screen.findByTestId('ai-connection-form')

    // The stored endpoint is visible rather than hidden behind an API-only field.
    expect(within(form).getByTestId('ai-connection-base-url')).toHaveValue(azure)

    await user.click(within(form).getByRole('button', { name: 'Update' }))
    await waitFor(() => expect(updateAIConnection).toHaveBeenCalled())
    expect(updateAIConnection.mock.calls[0][2]).toMatchObject({ base_url: azure })
  })

  it('sends a new connection endpoint on create', async () => {
    listAIVendors.mockResolvedValue([{ provider: 'openai', connected: false, tokens: [], connections: [], models: [] }])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-new-openai'))
    const form = await screen.findByTestId('ai-connection-form')

    await user.type(within(form).getByTestId('ai-connection-base-url'), 'https://acme.openai.azure.com/')
    await user.click(within(form).getByRole('button', { name: 'Update' }))

    await waitFor(() => expect(createAIConnection).toHaveBeenCalled())
    // Sent verbatim — the backend owns normalisation, so there's one source of
    // truth for what "https://…azure.com/" becomes.
    expect(createAIConnection.mock.calls[0][1]).toMatchObject({
      base_url: 'https://acme.openai.azure.com/',
    })
  })

  // With no connection anywhere there is nowhere to put a key, so the affordance
  // offered is the connection, not the token.
  it('hides Add token until a connection exists', async () => {
    listAIVendors.mockResolvedValue([{ provider: 'openai', connected: false, tokens: [], connections: [], models: [] }])
    renderPage()
    await waitFor(() => expect(screen.getByTestId('ai-connection-tile-new-openai')).toBeInTheDocument())
    expect(screen.queryByTestId('ai-token-add')).not.toBeInTheDocument()
  })

  // An engine row points at a token directly, so there is no
  // named provider config to create. What the section offers instead is one tile
  // per provider, and every affordance hangs off a tile — so the positive shape
  // (a tile for the connection) is what pins it. Asserting on the section's own
  // title would say nothing: it IS "Providers".
  it('presents a tile per provider, with no named-config create control', async () => {
    listAIVendors.mockResolvedValue([{ provider: 'openai', connected: true, tokens: [], connections: [{ id: 7, provider: 'openai', name: 'Default endpoint', base_url: '', token_count: 0, created_at: '2026-01-01' }], models: [] }])
    renderPage()
    // The OpenAI connection renders as its own tile, keyed by connection id.
    await waitFor(() => expect(screen.getByTestId('ai-connection-tile-7')).toBeInTheDocument())
    // Nothing creates a standalone, named config. The key-add control lives
    // inside a tile's panel, so it is absent until a tile is opened.
    expect(screen.queryByRole('button', { name: /new provider|add provider|new config/i })).not.toBeInTheDocument()
    expect(screen.queryByTestId('ai-token-add')).not.toBeInTheDocument()
  })

  // A refused key is the form's problem, so the form says it, under the field
  // the server named, and stays open. No toast repeats it.
  describe('key form errors', () => {
    const apiError = (status: number, message: string, field?: string) =>
      new ApiError(status, message, { error: { code: 'x', message, ...(field ? { field } : {}) } })
    const failWith = (status: number, message: string, field?: string) =>
      createVendorToken.mockRejectedValue(apiError(status, message, field))
    const fieldOf = (form: HTMLElement, label: RegExp) => within(form).getByText(label, { selector: 'label' }).parentElement

    async function submitKey(name?: string) {
      listAIVendors.mockResolvedValue([{ provider: 'openai', connected: false, tokens: [], connections: [], models: [] }])
      const { user, form } = await openKeyForm('ai-connection-tile-new-openai')
      if (name) await user.type(within(form).getByPlaceholderText('Default'), name)
      await user.type(within(form).getByPlaceholderText('sk-…'), 'sk-bad')
      await user.click(within(form).getByRole('button', { name: 'Add' }))
      return { user, form }
    }

    it('shows a rejected key under the API key field', async () => {
      const msg = 'OpenAI rejected this API key. Check that you copied the whole key.'
      failWith(400, msg, 'api_key')
      const { form } = await submitKey()
      const err = await within(form).findByText(msg)
      expect(within(form).getByText(/^API key/, { selector: 'label' }).parentElement).toContainElement(err)
      expect(screen.getByTestId('ai-token-form')).toBeInTheDocument()
      expect(toast.error).not.toHaveBeenCalled()
    })

    it('shows a taken name under the Name field', async () => {
      const msg = 'A key named "Default" already exists for OpenAI. Choose another name.'
      failWith(409, msg, 'name')
      const { form } = await submitKey('Default')
      const err = await within(form).findByText(msg)
      expect(within(form).getByText(/^Name/, { selector: 'label' }).parentElement).toContainElement(err)
      expect(within(form).getByText(/^API key/, { selector: 'label' }).parentElement).not.toContainElement(err)
      expect(toast.error).not.toHaveBeenCalled()
    })

    it('shows an error with no field above the buttons', async () => {
      failWith(500, 'failed to create token')
      const { form } = await submitKey()
      expect(await within(form).findByRole('alert')).toHaveTextContent('failed to create token')
      expect(toast.error).not.toHaveBeenCalled()
    })

    it('drops the old error when the form opens again', async () => {
      const msg = 'OpenAI rejected this API key. Check that you copied the whole key.'
      failWith(400, msg, 'api_key')
      const { user, form } = await submitKey()
      await within(form).findByText(msg)
      await user.click(within(form).getByRole('button', { name: 'Cancel' }))
      await user.click(await screen.findByTestId('ai-token-add'))
      const again = await screen.findByTestId('ai-token-form')
      expect(within(again).queryByText(msg)).not.toBeInTheDocument()
    })

    it('shows an edit error under its field, and drops it when the form opens again', async () => {
      listAIVendors.mockResolvedValue([
        { provider: 'openai', connected: true, tokens: [{ id: 1, name: 'prod', key_last4: 'a1b2', connection_id: 7, created_at: '2026-01-01' }], connections: [{ id: 7, provider: 'openai', name: 'OpenAI prod', base_url: '', token_count: 1, created_at: '2026-01-01' }], models: [] },
      ])
      const msg = 'A key named "staging" already exists for OpenAI. Choose another name.'
      updateVendorToken.mockRejectedValue(apiError(409, msg, 'name'))
      const user = userEvent.setup()
      renderPage()
      await user.click(await screen.findByTestId('ai-connection-tile-7'))
      await user.click(await screen.findByTestId('ai-token-row-1'))
      const form = await screen.findByTestId('ai-token-form')
      await user.clear(within(form).getByPlaceholderText('Default'))
      await user.type(within(form).getByPlaceholderText('Default'), 'staging')
      await user.click(within(form).getByRole('button', { name: 'Update' }))

      const err = await within(form).findByText(msg)
      expect(fieldOf(form, /^Name/)).toContainElement(err)
      expect(toast.error).not.toHaveBeenCalled()

      await user.click(within(form).getByRole('button', { name: 'Cancel' }))
      await user.click(await screen.findByTestId('ai-token-row-1'))
      const again = await screen.findByTestId('ai-token-form')
      expect(within(again).queryByText(msg)).not.toBeInTheDocument()
    })
  })

  // A new key is offered to the vendor's engines that have none; only the ones
  // it can run are ticked, and only those are sent.
  describe('engines without a key', () => {
    async function addKey() {
      listAIVendors.mockResolvedValue([{ provider: 'openai', connected: false, tokens: [], connections: [], models: [] }])
      const { user, form } = await openKeyForm('ai-connection-tile-new-openai')
      await user.type(within(form).getByPlaceholderText('sk-…'), 'sk-new')
      await user.click(within(form).getByRole('button', { name: 'Add' }))
      return user
    }

    it('offers the key to keyless engines and sends only the ticked ones', async () => {
      listKeylessEngines.mockResolvedValue([
        { kind: 'ai', id: 5, name: 'Reviewer', fits: true },
        { kind: 'mt', id: 9, name: 'GPT translation', fits: true },
        { kind: 'ai', id: 6, name: 'Old reviewer', fits: false, reason: 'This key cannot use gpt-3.' },
      ])
      const user = await addKey()
      const dialog = await screen.findByTestId('attach-key-dialog')
      expect(within(dialog).getByText('3 engines have no API key')).toBeInTheDocument()
      expect(listKeylessEngines).toHaveBeenCalledWith('openai', 1)
      const stale = within(dialog).getByTestId('attach-key-engine-ai:6')
      expect(stale).toBeDisabled()
      expect(stale).not.toBeChecked()
      expect(stale.closest('label')).toHaveTextContent('This key cannot use gpt-3.')
      expect(within(dialog).getByTestId('attach-key-engine-ai:5')).toBeChecked()

      await user.click(within(dialog).getByTestId('attach-key-engine-mt:9'))
      await user.click(within(dialog).getByRole('button', { name: 'Use this key' }))
      await waitFor(() => expect(attachKeyToEngines).toHaveBeenCalledWith('openai', 1, { mt_engine_ids: [], ai_engine_ids: [5] }))
      await waitFor(() => expect(screen.queryByTestId('attach-key-dialog')).not.toBeInTheDocument())
    })

    it('stays closed when every engine has a key', async () => {
      await addKey()
      await waitFor(() => expect(listKeylessEngines).toHaveBeenCalled())
      // Let the empty list arrive and render before checking.
      await act(async () => { await listKeylessEngines.mock.results[0].value })
      expect(screen.queryByTestId('attach-key-dialog')).not.toBeInTheDocument()
      expect(attachKeyToEngines).not.toHaveBeenCalled()
    })
  })

  it('names the engines a key serves before it is removed', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'openai', connected: true, tokens: [{ id: 1, name: 'prod', key_last4: 'a1b2', connection_id: 7, created_at: '2026-01-01' }], connections: [{ id: 7, provider: 'openai', name: 'OpenAI prod', base_url: '', token_count: 1, created_at: '2026-01-01' }], models: [] },
    ])
    listEnginesUsingKey.mockResolvedValue([
      { kind: 'ai', id: 5, name: 'Reviewer', fits: true },
      { kind: 'mt', id: 9, name: 'GPT translation', fits: true },
    ])
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-7'))
    const row = await screen.findByTestId('ai-token-row-1')
    await user.click(within(row).getByRole('button', { name: 'Remove' }))
    expect(await screen.findByText('2 engines use this key and will stop working until they get another one:')).toBeInTheDocument()
    const list = screen.getByTestId('delete-key-engines')
    expect(within(list).getAllByRole('listitem').map(li => li.textContent)).toEqual(['Reviewer', 'GPT translation'])
    expect(listEnginesUsingKey).toHaveBeenCalledWith('openai', 1)
  })

  it('names at most three engines and counts the rest', async () => {
    listAIVendors.mockResolvedValue([
      { provider: 'openai', connected: true, tokens: [{ id: 1, name: 'prod', key_last4: 'a1b2', connection_id: 7, created_at: '2026-01-01' }], connections: [{ id: 7, provider: 'openai', name: 'OpenAI prod', base_url: '', token_count: 1, created_at: '2026-01-01' }], models: [] },
    ])
    listEnginesUsingKey.mockResolvedValue(['A', 'B', 'C', 'D', 'E'].map((name, i) => ({ kind: 'ai', id: i + 1, name, fits: true })))
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('ai-connection-tile-7'))
    const row = await screen.findByTestId('ai-token-row-1')
    await user.click(within(row).getByRole('button', { name: 'Remove' }))
    expect(await screen.findByText('5 engines use this key and will stop working until they get another one:')).toBeInTheDocument()
    const list = screen.getByTestId('delete-key-engines')
    expect(within(list).getAllByRole('listitem').map(li => li.textContent)).toEqual(['A', 'B', 'C', 'and 2 more'])
    expect(screen.getByText('Their settings and routes are kept.')).toBeInTheDocument()
  })
})

