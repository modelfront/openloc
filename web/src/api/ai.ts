import { request } from './request';

// ─── AI providers + tokens ───
//
// A provider (OpenAI / Claude / an installed extension) holds one or more named
// API keys.
// Adding a token verifies the key live, then stores it encrypted. Keys are never
// returned here — only token metadata.

// Mirrors ai.KnownProviders, plus `ext:<id>` for an installed extension.
// deepl/google hold a credential like any other provider but are NOT
// text-generation models — they have no model catalog and cannot back an AI
// engine.
export type BuiltInProviderId = 'openai' | 'claude' | 'deepl' | 'google';
export type ExtensionProviderId = `ext:${string}`;
export type AIProviderId = BuiltInProviderId | ExtensionProviderId;

// A selectable provider model (empty list for an extension). Mirrors ai.Model.
// `params` is per MODEL, not per provider: Anthropic's current generation dropped
// temperature/top_p/top_k and a fixed thinking budget (sending them is a 400)
// and takes `effort` instead, while Haiku 4.5 is the reverse.
export interface AIModel {
  id: string;
  label: string;
  params: AIModelParam[];
}

// A tunable request parameter for a provider (mirrors ai.ParamDef). Drives the
// editable "Model settings" panel + client-side hints.
export type AIParamType = 'number' | 'integer' | 'boolean' | 'enum' | 'string';
export interface AIModelParam {
  key: string;
  label: string;
  type: AIParamType;
  min?: number;
  max?: number;
  step?: number;
  options?: { value: string; label: string }[];
  description: string;
}

export interface AIVendorToken {
  id: number;
  name: string;
  key_last4: string; // non-secret hint (last 4 chars) shown in the edit UI
  /** The connection this key authenticates. */
  connection_id: number;
  /** Read-only echo of the connection's endpoint; edit it on the connection. */
  base_url?: string;
  /**
   * The key the built-in chat spends for this provider. At most one token per
   * provider carries it; with nobody chosen, chat falls back to the oldest.
   */
  use_for_chat: boolean;
  created_at: string;
}

// A connection is WHERE we reach a provider: {provider, name,
// endpoint}. Tokens are WHAT authenticates and belong to one. A connection may
// have no tokens yet — configure the endpoint now, add the key later, which is
// what token_count lets the UI say.
export interface AIConnection {
  id: number;
  provider: AIProviderId;
  name: string;
  /** Empty = the provider's public API. */
  base_url: string;
  /**
   * A connection is LIVE when this is > 0. There is no separate enabled flag —
   * nothing can call an endpoint it has no credential for, so a second switch
   * could only ever disagree with this one.
   */
  token_count: number;
  created_at: string;
}

export interface AIConnectionInput {
  /**
   * Optional: blank keeps the stored name on update, and falls back to the
   * provider's display name on create. The settings UI never sends it — a
   * free-text label on a fixed list of providers earns nothing — but the field
   * stays on the API for callers that do name their connections.
   */
  name?: string;
  base_url?: string;
}

/** What an extension can do. */
export type ExtensionCapability = 'qe' | 'ape';

/**
 * A manifest setting. Manifest v1 has `token` (secret — the API key) and an
 * optional `base_url` (the connection's endpoint, with an optional default).
 */
export interface ExtensionSetting {
  key: string;
  label: string;
  secret?: boolean;
  default?: string;
}

/** The extension behind an `ext:` vendor. Mirrors ai.extensionInfo. */
export interface AIVendorExtension {
  id: string;
  version: string;
  capabilities: ExtensionCapability[];
  settings: ExtensionSetting[];
  allowed_hosts: string[];
}

export interface AIVendorStatus {
  provider: AIProviderId;
  /** The name users see; for an extension, its manifest name. */
  display_name: string;
  /** Set only for an extension vendor. */
  extension?: AIVendorExtension;
  connected: boolean;
  tokens: AIVendorToken[];
  connections: AIConnection[];
  models: AIModel[];
  /**
   * Set when the call to the vendor failed and left `models` empty — e.g. "The
   * API key was rejected." The model dropdown shows this instead of its generic
   * empty text, so a bad key explains itself. Absent when the call succeeded,
   * including when it succeeded and returned nothing.
   */
  models_error?: string;
}

export interface VendorTokenInput {
  name: string;
  api_key?: string;
  /**
   * Which connection this key belongs to. The backend treats an ABSENT
   * connection_id as "keep the current one", so omitting it on a rename is
   * safe — but the form always sends it, so what the user sees and what is
   * stored stay in step.
   */
  connection_id?: number;
  /**
   * Make this the chat key for its provider. Setting it moves the flag off
   * whichever token held it. There is no way to set it false: a provider with
   * keys always has a chat key, so you change it by choosing another.
   */
  use_for_chat?: boolean;
}

export const listAIVendors = () =>
  request<{ data: AIVendorStatus[] }>('/ai/vendors').then(r => r.data);

// Add a token to a provider. Verifies the key live; rejects (with the provider's
// error) on failure — the key is only stored on success.
export const createVendorToken = (provider: AIProviderId, input: VendorTokenInput) =>
  request<{ data: AIVendorToken }>(`/ai/vendors/${provider}/tokens`, {
    method: 'POST',
    body: JSON.stringify(input),
  }).then(r => r.data);

// Edit a token. A blank api_key keeps the stored key (renames only).
export const updateVendorToken = (provider: AIProviderId, id: number, input: VendorTokenInput) =>
  request<{ data: AIVendorToken }>(`/ai/vendors/${provider}/tokens/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(input),
  }).then(r => r.data);

export const deleteVendorToken = (provider: AIProviderId, id: number) =>
  request<void>(`/ai/vendors/${provider}/tokens/${id}`, { method: 'DELETE' });

/** An engine a key can fix (keyless) or serves (in use). `fits` is false when
 *  the key cannot run the engine's model; `reason` says why. */
export interface KeyEngine {
  kind: 'mt' | 'ai';
  id: number;
  name: string;
  purpose?: string;
  fits: boolean;
  reason?: string;
}

// Engines of the key's vendor that have no key.
export const listKeylessEngines = (provider: AIProviderId, tokenId: number) =>
  request<{ data: KeyEngine[] }>(`/ai/vendors/${provider}/tokens/${tokenId}/keyless-engines`).then(r => r.data);

// Gives the key to the chosen keyless engines, all or nothing.
export const attachKeyToEngines = (provider: AIProviderId, tokenId: number, ids: { mt_engine_ids: number[]; ai_engine_ids: number[] }) =>
  request<{ data: { attached: number } }>(`/ai/vendors/${provider}/tokens/${tokenId}/attach`, {
    method: 'POST',
    body: JSON.stringify(ids),
  }).then(r => r.data);

// Engines that stop working if the key is deleted.
export const listEnginesUsingKey = (provider: AIProviderId, tokenId: number) =>
  request<{ data: KeyEngine[] }>(`/ai/vendors/${provider}/tokens/${tokenId}/engines`).then(r => r.data);

// ─── Model settings ───
//
// The parameter blob a model runs with, stored on an AI engine row
// (ai_engine_rows.settings). Keys are whitelisted per model
// by the provider catalog above.
export type ModelSettings = Record<string, number | boolean | string>;

// ─── AI connections ───

export const createAIConnection = (provider: AIProviderId, input: AIConnectionInput) =>
  request<{ data: AIConnection }>(`/ai/vendors/${provider}/connections`, {
    method: 'POST',
    body: JSON.stringify(input),
  }).then(r => r.data);

export const updateAIConnection = (provider: AIProviderId, id: number, input: AIConnectionInput) =>
  request<{ data: AIConnection }>(`/ai/vendors/${provider}/connections/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(input),
  }).then(r => r.data);

export const deleteAIConnection = (provider: AIProviderId, id: number) =>
  request<void>(`/ai/vendors/${provider}/connections/${id}`, { method: 'DELETE' });

// ─── Extensions ───
//
// An extension is an uploaded zip (manifest.json + WASM) that adds a QE/APE
// provider. Once installed it is listed as an AI vendor (`ext:<id>`) and takes
// keys like any other. Listing is for managers and up; uploading and removing
// are owner/admin.

export interface Extension {
  id: string;
  name: string;
  version: string;
  description?: string;
  provider: ExtensionProviderId;
  capabilities: ExtensionCapability[];
  settings: ExtensionSetting[];
  /** Hosts the extension may call. */
  allowed_hosts: string[];
  created_at: string;
  updated_at: string;
}

/** Installs a zip, replacing an installed extension with the same id. A bad
 *  zip or manifest rejects with a field error on `file`. */
export const uploadExtension = (file: File) => {
  const form = new FormData();
  form.append('file', file);
  return request<{ data: Extension }>('/ai/extensions', { method: 'POST', body: form }).then(r => r.data);
};

/** Removes an extension and deletes its keys and endpoints; engines on them read as having no key. */
export const deleteExtension = (id: string) =>
  request<void>(`/ai/extensions/${id}`, { method: 'DELETE' });
