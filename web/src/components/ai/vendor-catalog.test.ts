import { describe, it, expect } from 'vitest'
import {
  VENDOR_CATALOG, vendorsIn, wiredProviderIds, vendorName, vendorLabel,
  isBuiltIn, allowsCustomEndpoint, isExtensionProvider,
} from './vendor-catalog'
import type { AIVendorExtension } from '../../api'

// The catalog is the single list of vendors the AI settings grid renders, and
// the only thing standing between "add one more entry" and a UI that offers
// credentials it must not.
describe('vendor catalog', () => {
  // Mock is listed but built-in: it never takes a credential, and it only
  // renders where the deployment provisions the engine — which `mtdefaults`
  // refuses to do in production.
  it('lists the mock engine as a built-in, never a configurable vendor', () => {
    expect(isBuiltIn('mock')).toBe(true)
    expect(allowsCustomEndpoint('mock')).toBe(false)
  })

  // LibreTranslate's API key and URL must never be offered as fields. Being
  // `builtIn` is what enforces that: the panel shows a read-only note instead of
  // an endpoint field and a keys list.
  it('gives LibreTranslate no way to enter a credential or endpoint', () => {
    expect(isBuiltIn('libretranslate')).toBe(true)
    expect(allowsCustomEndpoint('libretranslate')).toBe(false)
  })

  // The carve-out must stay a carve-out — every other MT engine takes
  // user-supplied keys and endpoint.
  it('leaves the other MT engines user-configurable', () => {
    for (const v of vendorsIn('mt').filter(v => !v.builtIn)) {
      expect(isBuiltIn(v.id)).toBe(false)
      expect(allowsCustomEndpoint(v.id)).toBe(true)
    }
  })

  // The grid is not a roadmap: every tile must have code behind it. Adding an
  // entry to the MT row fails here, so the list only grows on purpose. This
  // pins the frontend list; it does not read the backend's.
  it('offers exactly these MT engines', () => {
    expect(vendorsIn('mt').map(v => v.id).sort()).toEqual(['deepl', 'google', 'libretranslate', 'mock'])
  })

  // Same rule on the other side: the QE+APE row only grows on purpose.
  it('offers exactly these QE+APE vendors', () => {
    expect(vendorsIn('qe').map(v => v.id).sort()).toEqual(['claude', 'openai'])
  })

  // Only wired vendors open a credential form, so flipping one to wired is a
  // deliberate change that has to show up here.
  it('marks exactly these providers as wired', () => {
    expect(wiredProviderIds().sort()).toEqual(['claude', 'deepl', 'google', 'openai'])
  })

  // The invariant behind the whole catalog: a tile either accepts a credential
  // (wired) or supplies its own (builtIn). Anything else is a dead end the user
  // can click but not use.
  it('leaves no vendor that is neither wired nor built in', () => {
    const dead = VENDOR_CATALOG.filter(v => !v.wired && !v.builtIn).map(v => v.id)
    expect(dead).toEqual([])
  })

  it('has unique ids and a display name for every entry', () => {
    const ids = VENDOR_CATALOG.map(v => v.id)
    expect(new Set(ids).size).toBe(ids.length)
    for (const v of VENDOR_CATALOG) {
      expect(v.name.trim()).not.toBe('')
      expect(vendorName(v.id)).toBe(v.name)
    }
  })

  // Extensions are not in the catalog: they come from the vendor list, with the
  // manifest deciding the name and whether there is an endpoint to set.
  describe('extension vendors', () => {
    const ext = (settings: AIVendorExtension['settings']): AIVendorExtension =>
      ({ id: 'mockqe', version: '1.0.0', capabilities: ['qe', 'ape'], settings, allowed_hosts: [] })

    it('recognises the ext: provider prefix', () => {
      expect(isExtensionProvider('ext:mockqe')).toBe(true)
      expect(isExtensionProvider('openai')).toBe(false)
    })

    it('offers an endpoint only when the manifest has a base_url setting', () => {
      expect(allowsCustomEndpoint('ext:mockqe', ext([{ key: 'token', label: 'API token', secret: true }, { key: 'base_url', label: 'Endpoint' }]))).toBe(true)
      expect(allowsCustomEndpoint('ext:mockqe', ext([{ key: 'token', label: 'API token', secret: true }]))).toBe(false)
    })

    it('names an extension by its display name, else by its id', () => {
      expect(vendorLabel({ provider: 'ext:mockqe', display_name: 'Mock QE' })).toBe('Mock QE')
      expect(vendorName('ext:mockqe')).toBe('mockqe')
    })
  })
})
