import type { AIProviderId, AIVendorExtension, AIVendorStatus, ExtensionProviderId } from '../../api'

export type VendorGroup = 'qe' | 'mt'

export type CatalogVendor = {
  /** Matches an `ai_vendors.provider` value once wired; a display key until then. */
  id: string
  name: string
  group: VendorGroup
  /** False = renders, refuses to configure, explains itself when clicked. */
  wired: boolean
  /**
   * Ships with its credential already supplied by the deployment, so there is
   * nothing for a user to enter and nothing to add. Renders as live, opens a
   * read-only panel.
   */
  builtIn?: boolean
}

// Group 1: anything that can run QE+APE — the LLMs via the {status, postedit,
// score} contract, and installed extensions (added to the grid from the vendor
// list, not from this catalog). Group 2: machine translation.
//
// ONLY providers we already integrate with. The grid is not a roadmap: a tile for
// something with no code behind it promises a capability the product does not
// have, and the list would drift as an aspirational one no test can check.
// Adding a provider here should follow the integration, not precede it.
//
// Group 2 is exactly the MT engine types `internal/mtengine` accepts. Their
// credentials are entered here and nowhere else; an MT engine on the MT page then
// names one of these keys and adds the provider's request options
/**
 * Every provider the AI settings grid shows, and which of them actually work.
 *
 * The catalog is FRONTEND-ONLY on purpose. The `wired` entries must match
 * `ai.KnownProviders` on the Go side, and `ai.ValidProvider` accepts only those,
 * so an unwired provider cannot be configured even if someone forges the
 * request. Built-ins render only when the deployment's MT engine catalog offers
 * them.
 */
export const VENDOR_CATALOG: CatalogVendor[] = [
  { id: 'openai', name: 'OpenAI', group: 'qe', wired: true },
  { id: 'claude', name: 'Claude', group: 'qe', wired: true },

  { id: 'deepl', name: 'DeepL', group: 'mt', wired: true },
  { id: 'google', name: 'Google Translate', group: 'mt', wired: true },
  { id: 'libretranslate', name: 'LibreTranslate', group: 'mt', wired: false, builtIn: true },
  { id: 'mock', name: 'Mock', group: 'mt', wired: false, builtIn: true },
]

// Mock is listed but, like LibreTranslate, only RENDERS where the deployment
// actually provisions it — and `mtdefaults` never provisions it in production.
// A built-in tile shows only when the MT engine catalog offers it, so this list
// is what a deployment *may* supply, not what every deployment does.
//
// LibreTranslate IS listed, but as `builtIn`: the deployment supplies its
// endpoint and credential (LIBRETRANSLATE_URL), so there is nothing for a user
// to type. That is what keeps it inside the standing rule that its API key and
// URL must never appear as fields in the UI — it has no fields at all, no Add
// token button, and no editable endpoint.

// Render order only — the groups are NOT labelled in the UI. The separation is
// spatial (one row each).
export const VENDOR_GROUPS: { key: VendorGroup }[] = [{ key: 'qe' }, { key: 'mt' }]

export const vendorsIn = (group: VendorGroup) => VENDOR_CATALOG.filter(v => v.group === group)

/** Wired providers only — the set the API will actually accept. */
export const wiredProviderIds = () =>
  VENDOR_CATALOG.filter(v => v.wired).map(v => v.id as AIProviderId)

/** Display name for a provider id, falling back to the id for unknown providers.
 *  An extension's name comes from its manifest — use `vendorLabel` where the
 *  vendor is at hand. */
export const vendorName = (id: string) =>
  VENDOR_CATALOG.find(v => v.id === id)?.name ?? (isExtensionProvider(id) ? id.slice(EXT_PREFIX.length) : id)

/** The name users see for a vendor: the server's display name, which is the
 *  manifest name for an extension. */
export const vendorLabel = (v: Pick<AIVendorStatus, 'provider' | 'display_name'>) =>
  v.display_name || vendorName(v.provider)

const EXT_PREFIX = 'ext:'

/** True for an installed extension's provider (`ext:<id>`). */
export const isExtensionProvider = (provider: string): provider is ExtensionProviderId =>
  provider.startsWith(EXT_PREFIX)

/** A manifest setting by key (`token`, `base_url`). */
export const extensionSetting = (ext: AIVendorExtension | undefined, key: string) =>
  ext?.settings.find(s => s.key === key)

/** Whether an extension can review translations (QE, APE, or both). */
export const reviewsTranslations = (ext: Pick<AIVendorExtension, 'capabilities'>) =>
  ext.capabilities.includes('qe') || ext.capabilities.includes('ape')

/** Which group a provider belongs to, or undefined for an unknown id. */
export const vendorGroupOf = (id: string): VendorGroup | undefined =>
  VENDOR_CATALOG.find(v => v.id === id)?.group

/**
 * Whether a provider's endpoint can be pointed somewhere else. An extension
 * has an endpoint only when its manifest declares a `base_url` setting; a
 * built-in provider has none to change.
 */
export const allowsCustomEndpoint = (provider: string, ext?: AIVendorExtension) =>
  isExtensionProvider(provider) ? !!extensionSetting(ext, 'base_url') : !isBuiltIn(provider)

/** True for providers whose credential comes from the deployment, not the user. */
export const isBuiltIn = (provider: string) =>
  VENDOR_CATALOG.find(v => v.id === provider)?.builtIn === true

/**
 * Providers the built-in chat can drive — mirrors `chat.chatProviders`.
 *
 * The "use for chat" choice is only offered on these: an extension only
 * reviews translations, and the MT providers translate rather than converse. A control
 * that cannot change anything is worse than no control.
 */
export const drivesChat = (provider: string) => provider === 'claude' || provider === 'openai'
