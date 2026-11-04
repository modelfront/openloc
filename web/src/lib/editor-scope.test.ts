import { describe, expect, it } from 'vitest'
import { groupByJob, openTogetherBlock } from './editor-scope'
import { editorScopePath, parseScopeJobs } from './editor-link'

const job = (project_id: number, target_language: string) => ({ project_id, target_language })

describe('openTogetherBlock', () => {
  it('lets jobs of one project and one language open together', () => {
    expect(openTogetherBlock([job(1, 'de'), job(1, 'de')])).toBeNull()
  })
  it('refuses two projects, two languages, nothing, or too many', () => {
    expect(openTogetherBlock([job(1, 'de'), job(2, 'de')])).toBe('Select files of one project')
    expect(openTogetherBlock([job(1, 'de'), job(1, 'fr')])).toBe('Select files of one target language')
    expect(openTogetherBlock([])).toBe('Select the files to open')
    expect(openTogetherBlock(Array.from({ length: 501 }, () => job(1, 'de')))).toBe('Open at most 500 files together')
  })
})

describe('editorScopePath', () => {
  it('keeps the single-file route for one job', () => {
    expect(editorScopePath(7, [3])).toBe('/projects/7/jobs/3/edit')
  })
  it('lists several jobs in the order given', () => {
    expect(editorScopePath(7, [5, 3])).toBe('/projects/7/edit?jobs=5,3')
  })
})

describe('parseScopeJobs', () => {
  it('reads the ids back', () => {
    expect(parseScopeJobs('5,3')).toEqual([5, 3])
  })
  it('rejects anything that is not a list of ids', () => {
    expect(parseScopeJobs(null)).toBeNull()
    expect(parseScopeJobs('')).toBeNull()
    expect(parseScopeJobs('5,x')).toBeNull()
    expect(parseScopeJobs('5,,3')).toBeNull()
    expect(parseScopeJobs('-1')).toBeNull()
  })
})

describe('editor ids', () => {
  it('is the job for one job, the project and jobs for several', async () => {
    const { scopeEditorId, editorJobIds, editorApiPath } = await import('./editor-scope')
    expect(scopeEditorId(7, [3])).toBe(3)
    const id = scopeEditorId(7, [5, 3])
    expect(editorJobIds(id)).toEqual([5, 3])
    expect(editorApiPath(3, 'index')).toBe('/jobs/3/editor/index')
    expect(editorApiPath(id, 'rows', 'segment_ids=1,2')).toBe('/projects/7/editor/rows?jobs=5,3&segment_ids=1,2')
  })

  it('finds every open scope that holds a job', async () => {
    const { QueryClient } = await import('@tanstack/react-query')
    const { editorIdsHolding } = await import('./editor-scope')
    const qc = new QueryClient()
    qc.setQueryData(['job-translations', '7:5,3', 'index'], {})
    qc.setQueryData(['job-translations', '7:4,6', 'index'], {})
    expect(editorIdsHolding(qc, 3)).toEqual([3, '7:5,3'])
  })
})

describe('groupByJob', () => {
  it('groups the ids by job in the order given, and counts those of files the caller may not edit', () => {
    const files: Record<number, { jobId: number; canEdit: boolean }> = {
      10: { jobId: 1, canEdit: true }, 11: { jobId: 1, canEdit: true },
      20: { jobId: 2, canEdit: false }, 30: { jobId: 3, canEdit: true },
    }
    const { byJob, readOnly } = groupByJob([30, 11, 20, 10, 99], id => files[id])
    expect([...byJob]).toEqual([[3, [30]], [1, [11, 10]]])
    expect(readOnly).toBe(1)
  })
})
