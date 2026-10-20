import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { MTProviderEnginesSection } from './MTProviderEnginesSection'
import { queryKeys } from '@/lib/query-keys'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const listMTEngines = vi.fn()
const listMTEngineCatalog = vi.fn()
const listAIVendors = vi.fn()
const createMTEngine = vi.fn()
const updateMTEngine = vi.fn()
const deleteMTEngine = vi.fn()
vi.mock('../../api', () => ({
  listMTEngines: () => listMTEngines(),
  listMTEngineCatalog: () => listMTEngineCatalog(),
  listAIVendors: () => listAIVendors(),
  createMTEngine: (i: unknown) => createMTEngine(i),
  updateMTEngine: (id: number, i: unknown) => updateMTEngine(id, i),
  deleteMTEngine: (id: number) => deleteMTEngine(id),
}))

const CATALOG = [
  {
    engine_type: 'deepl', name: 'DeepL', needs_token: true,
    params: [
      {
        key: 'formality', label: 'Formality', type: 'enum', description: 'How formal.',
        options: [{ value: 'more', label: 'More formal' }, { value: 'less', label: 'Less formal' }],
      },
      { key: 'glossary_id', label: 'Glossary ID', type: 'string', description: 'A DeepL glossary.' },
    ],
  },
  {
    engine_type: 'google', name: 'Google Translate', needs_token: true,
    params: [{ key: 'model', label: 'Model', type: 'enum', description: 'Which model.', options: [{ value: 'nmt', label: 'Neural' }] }],
  },
  // What the deployment supplies. No key, no options — the backend fills in the
  // endpoint from the environment.
  { engine_type: 'libretranslate', name: 'LibreTranslate', needs_token: false, params: [] },
]

const VENDORS = [
  { provider: 'deepl', connected: true, models: [], tokens: [{ id: 11, name: 'EU', key_last4: 'a1', created_at: '2026-01-01' }] },
  { provider: 'google', connected: true, models: [], tokens: [{ id: 22, name: 'GCP', key_last4: 'b2', created_at: '2026-01-01' }] },
]

function renderSection(canWrite = true) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const utils = render(
    <QueryClientProvider client={qc}>
      <MemoryRouter><MTProviderEnginesSection canWrite={canWrite} /></MemoryRouter>
    </QueryClientProvider>,
  )
  return Object.assign(utils, { qc })
}

/** Resolves once these queries have their data, so a check made after it sees
 *  the section with every other list already in. */
async function settled(qc: QueryClient, ...keys: readonly (readonly unknown[])[]) {
  await waitFor(() => {
    for (const key of keys) expect(qc.getQueryState(key)?.status).toBe('success')
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  listMTEngines.mockResolvedValue([])
  listMTEngineCatalog.mockResolvedValue(CATALOG)
  listAIVendors.mockResolvedValue(VENDORS)
  createMTEngine.mockResolvedValue({ id: 1 })
  updateMTEngine.mockResolvedValue({ id: 1 })
})

// The form-flow tests chain a typed name and several portal pickers; under
// full-suite load that chain can outrun vitest's 5s default.
const FORM_FLOW_TIMEOUT = 15_000

// The button stays disabled until the engines, the catalog and the keys are in.
async function openCreateForm(user: ReturnType<typeof userEvent.setup>) {
  const add = await screen.findByTestId('mt-engine-add')
  await waitFor(() => expect(add).toBeEnabled())
  await user.click(add)
  return screen.findByTestId('mt-engine-form')
}

describe('MTProviderEnginesSection', () => {
  // The create form seeds its provider from the catalog when it mounts, so it
  // must not open before the catalog and the keys are there.
  it('opens the create form only once the catalog has loaded', async () => {
    let resolveCatalog: (c: typeof CATALOG) => void = () => {}
    listMTEngineCatalog.mockReturnValue(new Promise(r => { resolveCatalog = r }))
    const user = userEvent.setup()
    const { qc } = renderSection()

    await settled(qc, queryKeys.mtEngines.all, queryKeys.aiVendors.all)
    const add = screen.getByTestId('mt-engine-add')
    expect(add).toBeDisabled()
    await user.click(add)
    expect(screen.queryByTestId('mt-engine-form')).not.toBeInTheDocument()

    resolveCatalog(CATALOG)
    await waitFor(() => expect(add).toBeEnabled())
    await user.click(add)
    const form = await screen.findByTestId('mt-engine-form')
    expect(within(form).getByText('Formality')).toBeInTheDocument()
  })

  it('opens the create form only once the keys have loaded', async () => {
    let resolveVendors: (v: typeof VENDORS) => void = () => {}
    listAIVendors.mockReturnValue(new Promise(r => { resolveVendors = r }))
    const user = userEvent.setup()
    const { qc } = renderSection()

    await settled(qc, queryKeys.mtEngines.all, queryKeys.mtEngines.catalog)
    expect(screen.getByTestId('mt-engine-add')).toBeDisabled()

    resolveVendors(VENDORS)
    const form = await openCreateForm(user)
    await user.click(within(form).getByTestId('mt-engine-key'))
    expect(await screen.findByRole('option', { name: 'EU' })).toBeInTheDocument()
  })

  // The edit form reads the provider from the catalog too, so no engine can be
  // opened before it arrives.
  it('lists no engine to open while the catalog is loading', async () => {
    let resolveCatalog: (c: typeof CATALOG) => void = () => {}
    listMTEngineCatalog.mockReturnValue(new Promise(r => { resolveCatalog = r }))
    listMTEngines.mockResolvedValue([{ id: 1, name: 'DeepL formal', engine_type: 'deepl', enabled: true, unusable: '', provisioned_by: '', token_id: 11, settings: {}, api_url: null, has_api_key: false, created_at: '', updated_at: '' }])
    const { qc } = renderSection()

    await settled(qc, queryKeys.mtEngines.all, queryKeys.aiVendors.all)
    expect(screen.queryByTestId('mt-engine-open-1')).not.toBeInTheDocument()

    resolveCatalog(CATALOG)
    expect(await screen.findByTestId('mt-engine-open-1')).toBeInTheDocument()
  })

  // An engine is a key plus that provider's options, built here rather than
  // appearing on its own when a key is added.
  it('creates an engine from a key and the provider options', async () => {
    const user = userEvent.setup()
    renderSection()

    const form = await openCreateForm(user)

    await user.type(within(form).getByTestId('mt-engine-name'), 'DeepL formal')
    // DeepL is the first catalogued type, so it is already selected; choosing
    // the key and the option is what is left.
    await user.click(within(form).getByTestId('mt-engine-key'))
    await user.click(await screen.findByRole('option', { name: 'EU' }))
    await user.click(within(form).getByText('Formality').closest('div')!.querySelector('button')!)
    await user.click(await screen.findByRole('option', { name: 'More formal' }))

    await user.click(within(form).getByTestId('mt-engine-submit'))

    await waitFor(() => expect(createMTEngine).toHaveBeenCalled())
    expect(createMTEngine).toHaveBeenCalledWith({
      name: 'DeepL formal',
      engine_type: 'deepl',
      token_id: 11,
      settings: { formality: 'more' },
      enabled: true,
    })
  }, FORM_FLOW_TIMEOUT)

  // The key and the options both belong to the provider that was selected when
  // they were chosen. Carrying them across would submit a Google key on a DeepL
  // engine — which the server rejects, so the form would just fail on save.
  it('clears the key and options when the provider changes', async () => {
    const user = userEvent.setup()
    renderSection()

    const form = await openCreateForm(user)
    await user.type(within(form).getByTestId('mt-engine-name'), 'Switcher')
    await user.click(within(form).getByTestId('mt-engine-key'))
    await user.click(await screen.findByRole('option', { name: 'EU' }))

    await user.click(within(form).getByTestId('mt-engine-provider'))
    await user.click(await screen.findByRole('option', { name: 'Google Translate' }))

    // Google's panel replaces DeepL's — its options are a different set.
    expect(within(form).getByText('Model')).toBeInTheDocument()
    expect(within(form).queryByText('Formality')).not.toBeInTheDocument()

    await user.click(within(form).getByTestId('mt-engine-submit'))
    await waitFor(() => expect(createMTEngine).toHaveBeenCalled())
    expect(createMTEngine).toHaveBeenCalledWith(
      expect.objectContaining({ engine_type: 'google', token_id: null, settings: {} }),
    )
  }, FORM_FLOW_TIMEOUT)

  // Deleting a built-in must not be a one-way door: whatever this deployment
  // supplies is offered in the form too, with no key asked for.
  it('builds a deployment-supplied engine with no key', async () => {
    const user = userEvent.setup()
    renderSection()

    const form = await openCreateForm(user)
    await user.type(within(form).getByTestId('mt-engine-name'), 'Our LibreTranslate')
    await user.click(within(form).getByTestId('mt-engine-provider'))
    await user.click(await screen.findByRole('option', { name: 'LibreTranslate' }))

    // The key control is inert rather than an empty picker begging a choice,
    // and the "add a key in AI settings" hint would be wrong here.
    expect(within(form).getByTestId('mt-engine-key')).toBeDisabled()
    expect(within(form).queryByText(/No key for this provider yet/)).not.toBeInTheDocument()

    await user.click(within(form).getByTestId('mt-engine-submit'))
    await waitFor(() => expect(createMTEngine).toHaveBeenCalled())
    expect(createMTEngine).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Our LibreTranslate', engine_type: 'libretranslate', token_id: null,
    }))
  }, FORM_FLOW_TIMEOUT)

  // A provider with no key yet must SAY so — an empty picker reads as a broken
  // control rather than a step you have not done.
  it('explains an empty key picker', async () => {
    listAIVendors.mockResolvedValue([{ provider: 'deepl', connected: false, models: [], tokens: [] }])
    const user = userEvent.setup()
    renderSection()

    const form = await openCreateForm(user)
    expect(within(form).getByText(/No key for this provider yet/)).toBeInTheDocument()
  })

  // A provisioned engine is the org's: it opens like any other, and everything
  // on it is editable — its type is in the catalog, so even retyping it is a
  // real choice the backend will validate.
  it('opens a provisioned engine fully editable', async () => {
    listMTEngines.mockResolvedValue([
      { id: 2, name: 'LibreTranslate', engine_type: 'libretranslate', enabled: true, unusable: '', provisioned_by: 'deployment', token_id: null, settings: {}, api_url: 'http://lt', has_api_key: false, created_at: '', updated_at: '' },
    ])
    const user = userEvent.setup()
    renderSection()

    await user.click(await screen.findByTestId('mt-engine-open-2'))
    const form = await screen.findByTestId('mt-engine-form')
    expect(within(form).getByTestId('mt-engine-name')).toBeEnabled()
    expect(within(form).getByTestId('mt-engine-enabled')).toBeEnabled()
    expect(within(form).getByTestId('mt-engine-provider')).toBeEnabled()

    await user.click(within(form).getByTestId('mt-engine-enabled'))
    await user.click(within(form).getByTestId('mt-engine-submit'))
    await waitFor(() => expect(updateMTEngine).toHaveBeenCalled())
    expect(updateMTEngine).toHaveBeenCalledWith(2, expect.objectContaining({
      name: 'LibreTranslate', engine_type: 'libretranslate', enabled: false,
    }))
  })

  // ...and it deletes like any other, which is what "created with the org, then
  // yours" means.
  it('offers delete on a provisioned engine', async () => {
    listMTEngines.mockResolvedValue([
      { id: 2, name: 'LibreTranslate', engine_type: 'libretranslate', enabled: true, unusable: '', provisioned_by: 'deployment', token_id: null, settings: {}, api_url: 'http://lt', has_api_key: false, created_at: '', updated_at: '' },
    ])
    renderSection()
    const row = await screen.findByTestId('mt-engine-row-2')
    expect(within(row).getByRole('button', { name: 'Delete' })).toBeInTheDocument()
  })

  // A configured engine opens fully, pre-filled with what it was saved with.
  it('opens a configured engine with its options', async () => {
    listMTEngines.mockResolvedValue([
      { id: 1, name: 'DeepL formal', engine_type: 'deepl', enabled: true, unusable: '', provisioned_by: '', token_id: 11, settings: { formality: 'more' }, api_url: null, has_api_key: false, created_at: '', updated_at: '' },
    ])
    const user = userEvent.setup()
    renderSection()

    await user.click(await screen.findByTestId('mt-engine-open-1'))
    const form = await screen.findByTestId('mt-engine-form')
    expect(within(form).getByTestId('mt-engine-name')).toHaveValue('DeepL formal')
    expect(within(form).getByTestId('mt-engine-provider')).toBeEnabled()
    expect(within(form).getByText('Formality')).toBeInTheDocument()
  })

  // The provider lock survives for a type the deployment NO LONGER offers — a
  // mock engine on a production deployment, say. Its type is absent from the
  // catalog, so retyping it is a trap and the picker shows the type it has.
  it('locks the provider on an engine whose type left the catalog', async () => {
    listMTEngines.mockResolvedValue([
      { id: 3, name: 'Old mock', engine_type: 'mock', enabled: true, unusable: '', provisioned_by: 'deployment', token_id: null, settings: {}, api_url: null, has_api_key: false, created_at: '', updated_at: '' },
    ])
    const user = userEvent.setup()
    renderSection()

    await user.click(await screen.findByTestId('mt-engine-open-3'))
    const form = await screen.findByTestId('mt-engine-form')
    expect(within(form).getByTestId('mt-engine-provider')).toBeDisabled()
    expect(within(form).getByTestId('mt-engine-name')).toBeEnabled()
  })

  // An engine that cannot translate says WHY, on the row, without being opened
  // — and the two causes are told apart, because "off" is undone by a switch
  // while "no key" needs the engine repointed.
  it.each([
    { unusable: 'disabled', chip: 'engine-unusable-disabled', text: 'Off' },
    { unusable: 'no_key', chip: 'engine-unusable-no_key', text: 'No key' },
  ])('flags an engine that is $unusable', async ({ unusable, chip, text }) => {
    listMTEngines.mockResolvedValue([
      { id: 1, name: 'DeepL formal', engine_type: 'deepl', enabled: unusable !== 'disabled', unusable, provisioned_by: '', token_id: unusable === 'no_key' ? null : 11, settings: {}, api_url: null, has_api_key: false, created_at: '', updated_at: '' },
    ])
    renderSection()
    const row = await screen.findByTestId('mt-engine-row-1')
    expect(within(row).getByTestId(chip)).toHaveTextContent(text)
  })

  // ...and a healthy one wears no warning at all.
  it('flags nothing on a usable engine', async () => {
    listMTEngines.mockResolvedValue([
      { id: 1, name: 'DeepL formal', engine_type: 'deepl', enabled: true, unusable: '', provisioned_by: '', token_id: 11, settings: {}, api_url: null, has_api_key: false, created_at: '', updated_at: '' },
    ])
    renderSection()
    const row = await screen.findByTestId('mt-engine-row-1')
    expect(within(row).queryByTestId('engine-unusable-disabled')).not.toBeInTheDocument()
    expect(within(row).queryByTestId('engine-unusable-no_key')).not.toBeInTheDocument()
  })

  // A manager who cannot write sees the list and nothing else — the same rule
  // the backend enforces on every write route here.
  it('hides create, edit and delete from a reader', async () => {
    listMTEngines.mockResolvedValue([
      { id: 1, name: 'DeepL formal', engine_type: 'deepl', enabled: true, unusable: '', provisioned_by: '', token_id: 11, settings: {}, api_url: null, has_api_key: false, created_at: '', updated_at: '' },
    ])
    renderSection(false)
    const row = await screen.findByTestId('mt-engine-row-1')
    expect(screen.queryByTestId('mt-engine-add')).not.toBeInTheDocument()
    expect(within(row).queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument()
    expect(row).not.toHaveAttribute('role', 'button')
  })
})

// A failed load must say so: "No MT engines yet" for a server error reads as
// "your engines are gone". The catalog and the keys build the create form, so
// either failing fails the section too — the form would offer no keys.
describe('MTProviderEnginesSection when loading fails', () => {
  const ENGINE = { id: 1, name: 'DeepL formal', engine_type: 'deepl', enabled: true, unusable: '', provisioned_by: '', token_id: 11, settings: {}, api_url: null, has_api_key: false, created_at: '', updated_at: '' }

  it.each([
    ['engines', listMTEngines],
    ['catalog', listMTEngineCatalog],
    ['keys', listAIVendors],
  ])('a failed %s load shows the error with a Retry, not the list', async (_what, failing) => {
    listMTEngines.mockResolvedValue([ENGINE])
    failing.mockRejectedValueOnce(new Error('server unavailable'))
    renderSection()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load the MT engines: server unavailable")
    expect(screen.queryByText('No MT engines yet.')).not.toBeInTheDocument()
    expect(screen.queryByTestId('mt-engine-row-1')).not.toBeInTheDocument()
    // The create form needs all three lists, so it cannot open from here.
    expect(screen.getByTestId('mt-engine-add')).toBeDisabled()
    await userEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByTestId('mt-engine-row-1')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
