import type { Job } from '@/api'

// The canonical Job fixture for component tests.
//
// A Job carries ~20 required fields, most of which any given test ignores.
// Spelling them out per spec file produces copies that drift on details no
// test asserts on. Tests override only what they assert on, so a new required
// field lands here once.
//
// Defaults describe an idle, untouched job: nothing running, nothing stopping,
// no workflow step. `counts` is a whole object, so overriding one bucket means
// spreading a base — `makeJobCounts({ approved: 10 })` does that.
export function makeJob(overrides: Partial<Job> = {}): Job {
  return {
    id: 1,
    file_id: 10,
    target_language: 'es',
    word_count: 100,
    project_id: 42,
    project_name: 'Test Project',
    source_language: 'en',
    file_name: 'test.txt',
    file_format: 'txt',
    file_created_at: '2026-09-07T00:00:00Z',
    segment_count: 10,
    status: 'created',
    pipeline_status: 'idle',
    counts: makeJobCounts(),
    created_at: '2026-09-07T00:00:00Z',
    updated_at: '2026-09-07T00:00:00Z',
    ...overrides,
  }
}

// Counts for a 10-segment job: total 10, every bucket 0 — override the
// buckets a test asserts on.
export function makeJobCounts(overrides: Partial<Job['counts']> = {}): Job['counts'] {
  return {
    total: 10,
    untranslated: 0,
    pre_translated: 0,
    auto_approved: 0,
    needs_review: 0,
    approved: 0,
    clearable_non_confirmed: 0,
    clearable_all: 0,
    ...overrides,
  }
}
