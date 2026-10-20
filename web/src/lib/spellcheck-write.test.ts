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
  message: 'typo', suggestions: ['Hello'], ruleCategory: 'TYPOS',
})

function result(over: Partial<BatchDraftResult> = {}): BatchDraftResult {
  return { applied: [], conflicts: [], locked: [], held: [], rejected: [], taskReopened: false, ...over }
}

function args(over: Partial<WriteSpellFixesArgs> = {}): WriteSpellFixesArgs {
  return {
    fileId: 11,
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
    expect(a.onTaskLocked).toHaveBeenCalledWith(locked)
  })
})
