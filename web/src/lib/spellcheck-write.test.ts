// @vitest-environment node
// The spell check's "Change" / "Change all": what the write sends and the
// warnings for the segments it left out.
//
// Anti-tautology: dropping the `warnHeldSkipped(result.held.length)` call
// makes "warns when someone else has a corrected segment open" fail.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { toast } from 'sonner'
import { writeSpellFixes, type WriteSpellFixesArgs } from './spellcheck-write'
import { ApiError } from '../api/request'
import type { BatchDraftResult } from '../api/spellcheck'
import type { SpellError } from './spellcheck-run'
import type { Translation } from '../api'

const mockBatchDraft = vi.fn()
vi.mock('../api/spellcheck', () => ({
  batchDraftTranslations: (...args: unknown[]) => mockBatchDraft(...args),
}))
vi.mock('sonner', () => ({ toast: { warning: vi.fn(), error: vi.fn(), success: vi.fn() } }))

const typo = (translationId: number): SpellError => ({
  translationId, segmentId: translationId, segmentIndex: 0, offset: 0, length: 5, word: 'Hallo',
  message: 'typo', suggestions: ['Hello'], ruleCategory: 'TYPOS', targetText: '', sourceText: '',
})

function result(over: Partial<BatchDraftResult> = {}): BatchDraftResult {
  return { applied: [], conflicts: [], locked: [], held: [], rejected: [], taskReopened: false, ...over }
}

function args(over: Partial<WriteSpellFixesArgs> = {}): WriteSpellFixesArgs {
  return {
    fileOf: () => ({ jobId: 1, fileId: 11 }),
    errors: [typo(1), typo(2)],
    replacement: 'Hello',
    includeLocked: false,
    lookup: () => ({ active_text: 'Hallo world', updated_at: '2026-10-01T10:00:00Z' }),
    onTaskLocked: vi.fn(),
    onTaskReopened: vi.fn(),
    ...over,
  }
}

beforeEach(() => {
  mockBatchDraft.mockReset()
  vi.mocked(toast.warning).mockClear()
})

describe('writeSpellFixes', () => {
  it('warns when someone else has a corrected segment open', async () => {
    mockBatchDraft.mockResolvedValue(result({ applied: [{ id: 1 } as Translation], held: [2] }))

    await expect(writeSpellFixes(args())).resolves.toBe(1)

    expect(mockBatchDraft).toHaveBeenCalledWith(11, [
      expect.objectContaining({ translation_id: 1, human_text: 'Hello world' }),
      expect.objectContaining({ translation_id: 2, human_text: 'Hello world' }),
    ], { includeLocked: false })
    expect(toast.warning).toHaveBeenCalledWith('1 segment was skipped because someone else is editing it.')
  })

  it('says nothing about held segments when none was skipped', async () => {
    mockBatchDraft.mockResolvedValue(result({ applied: [{ id: 1 } as Translation, { id: 2 } as Translation] }))

    await expect(writeSpellFixes(args())).resolves.toBe(2)
    expect(toast.warning).not.toHaveBeenCalled()
  })

  it('resolves null and reports the lock when the task refuses the write', async () => {
    const locked = new ApiError(409, 'This task is completed.', { error: { code: 'task_completed' } })
    mockBatchDraft.mockRejectedValue(locked)
    const a = args()

    await expect(writeSpellFixes(a)).resolves.toBeNull()
    expect(a.onTaskLocked).toHaveBeenCalledWith(locked, 1)
  })

  // A multi-file editor: translations 1 and 2 in file 11 (job 1), 3 in file 33 (job 3).
  const twoFiles = (id: number) => (id < 3 ? { jobId: 1, fileId: 11 } : { jobId: 3, fileId: 33 })

  it('sends one batch per file', async () => {
    mockBatchDraft.mockImplementation(async (_fileId: number, fixes: { translation_id: number }[]) =>
      result({ applied: fixes.map(f => ({ id: f.translation_id }) as Translation) }))

    await expect(writeSpellFixes(args({ fileOf: twoFiles, errors: [typo(1), typo(3), typo(2)] }))).resolves.toBe(3)
    expect(mockBatchDraft.mock.calls.map(c => [c[0], c[1].map((f: { translation_id: number }) => f.translation_id)]))
      .toEqual([[11, [1, 2]], [33, [3]]])
  })

  it('a file whose task refuses the write leaves the others written', async () => {
    const locked = new ApiError(409, 'This task is completed.', { error: { code: 'task_completed' } })
    mockBatchDraft.mockImplementation(async (fileId: number) => {
      if (fileId === 11) throw locked
      return result({ applied: [{ id: 3 } as Translation], taskReopened: true })
    })
    const a = args({ fileOf: twoFiles, errors: [typo(1), typo(3)] })

    await expect(writeSpellFixes(a)).resolves.toBe(1)
    expect(a.onTaskLocked).toHaveBeenCalledWith(locked, 1)
    expect(a.onTaskReopened).toHaveBeenCalledWith(3)
  })

  it('a file whose write fails for another reason leaves the others written', async () => {
    mockBatchDraft.mockImplementation(async (fileId: number) => {
      if (fileId === 11) throw new Error('Server error')
      return result({ applied: [{ id: 3 } as Translation] })
    })

    await expect(writeSpellFixes(args({ fileOf: twoFiles, errors: [typo(1), typo(3)] }))).resolves.toBe(1)
    expect(mockBatchDraft).toHaveBeenCalledTimes(2)
    expect(toast.warning).toHaveBeenCalledWith('1 file could not be saved, try again')
  })

  it('throws when no file could be written for another reason', async () => {
    const boom = new Error('Server error')
    mockBatchDraft.mockRejectedValue(boom)
    await expect(writeSpellFixes(args())).rejects.toBe(boom)
  })

  it('a locked file and a failed one, nothing written: both are reported', async () => {
    const locked = new ApiError(409, 'This task is completed.', { error: { code: 'task_completed' } })
    mockBatchDraft.mockImplementation(async (fileId: number) => {
      throw fileId === 11 ? locked : new Error('Server error')
    })
    const a = args({ fileOf: twoFiles, errors: [typo(1), typo(3)] })

    await expect(writeSpellFixes(a)).resolves.toBeNull()
    expect(a.onTaskLocked).toHaveBeenCalledWith(locked, 1)
    expect(toast.warning).toHaveBeenCalledWith('1 file could not be saved, try again')
  })
})
