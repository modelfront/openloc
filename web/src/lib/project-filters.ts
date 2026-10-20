// Shared parser for the URL-backed project list filters.
// One canonical parser used by:
//   - ProjectList (queries useInfiniteQuery with the parsed filters)
//   - ProjectFilterBar (renders chip values + handles toggles)
//
// Why shared: the URL is the single source of truth; if the page reads
// `?status=draft` and the chip bar reads `?status=foo`, the UI desyncs from
// the data fetch. One parser eliminates that drift class entirely.
//
// The parsed shape is `ProjectListFilters` from api/projects.ts. Keys are
// added in a fixed order (status → customer_id → source → target → created_by → q)
// so consumers that JSON.stringify() the result for cache keys get a stable
// string regardless of which params were set.

import { PROJECT_STATUSES, type ProjectStatus, type ProjectListFilters } from '../api'

export function parseProjectFilters(params: URLSearchParams): ProjectListFilters {
  const f: ProjectListFilters = {}
  const status = params.get('status')
  if (status && PROJECT_STATUSES.includes(status as ProjectStatus)) {
    f.status = status as ProjectStatus
  }
  const customerID = params.get('customer_id')
  if (customerID) f.customer_id = Number(customerID)
  const source = params.get('source')
  if (source) f.source = source
  const target = params.get('target')
  if (target) f.target = target
  const createdBy = params.get('created_by')
  if (createdBy) f.created_by = Number(createdBy)
  const q = params.get('q')
  if (q) f.q = q
  return f
}
