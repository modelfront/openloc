import type { FilterProfile } from '@/api'

// The canonical FilterProfile fixture for component tests: a built-in when
// builtinKey is set, else one of the org's. Tests override only what they
// assert on, so a new required field lands here once.
export function makeFilterProfile(id: number, format: string, name: string, builtinKey?: string, overrides: Partial<FilterProfile> = {}): FilterProfile {
  return {
    id, format, name, builtin: !!builtinKey, builtin_key: builtinKey, org_default: false,
    description: '', options: {}, created_at: '', updated_at: '',
    ...overrides,
  }
}
