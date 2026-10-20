import { describe, it, expect } from 'vitest'
import { ApiError } from '../api/request'
import {
  isQABlockError,
  getQAIssuesFromError,
  formatQAErrorToast,
  type QaIssue,
} from './qa-block'

// Server-side QA blocking surfaces 422 to the FE.
// These helpers are the chokepoint that translates the wire shape into
// FE behavior. They MUST be tested independently because:
//   1. The 422 response shape is novel in this codebase (only one other
//      handler returns 422). A regression in the body shape would break
//      the FE branch silently — `getQAIssuesFromError` would return [].
//   2. The save-mut onError handler relies on `isQABlockError` to choose
//      the right toast formatter. A misclassification would surface a
//      generic "Save failed" toast instead of the actionable issue list.

// Helper — builds a 422 ApiError with the production-shape body so each
// test doesn't have to repeat the `{ error: { code }, qa_issues }` envelope.
function qaBlockError(qaIssues: { check_type: string; severity: string; message: string }[] = []) {
  return new ApiError(422, 'fix QA errors before confirming', {
    error: { code: 'QAErrorsBlocked', message: 'fix QA errors before confirming' },
    qa_issues: qaIssues,
  })
}

describe('isQABlockError (422 + code detection)', () => {
  it('returns true for ApiError with status 422 AND error.code=QAErrorsBlocked', () => {
    expect(isQABlockError(qaBlockError())).toBe(true)
  })

  it('returns false for ApiError with status 422 but a different error.code', () => {
    // Another 422 in the codebase uses a different error
    // code (terminology MT-engine-unavailable). Without the code check,
    // that error would be misclassified as a QA block — wrong toast,
    // no actionable issue list. The narrower `code === "QAErrorsBlocked"`
    // requirement pins the identity contract.
    const err = new ApiError(422, 'mt engine unavailable', {
      error: { code: 'MTEngineUnavailable', message: 'mt engine unavailable' },
    })
    expect(isQABlockError(err)).toBe(false)
  })

  it('returns false for ApiError with status 422 but no error envelope at all', () => {
    // A bare 422 (e.g. from a misconfigured backend) must NOT be classified
    // as a QA block — the FE branch needs the issue list, which would be
    // missing.
    const err = new ApiError(422, 'unprocessable', {})
    expect(isQABlockError(err)).toBe(false)
  })

  it('returns false for ApiError with other statuses (even with QAErrorsBlocked code)', () => {
    // 409 (conflict) and 403 (forbidden) must NOT be classified as QA
    // blocks — each has its own dedicated handling in the save mut.
    // Even if some future code path emitted code=QAErrorsBlocked at a
    // different status, the status check is the primary discriminator.
    expect(isQABlockError(new ApiError(409, 'conflict', { error: { code: 'QAErrorsBlocked' } }))).toBe(false)
    expect(isQABlockError(new ApiError(403, 'forbidden', {}))).toBe(false)
    expect(isQABlockError(new ApiError(500, 'oops', {}))).toBe(false)
  })

  it('returns false for non-ApiError errors', () => {
    // Network errors, type errors, etc. fall through to the generic toast.
    expect(isQABlockError(new Error('network failure'))).toBe(false)
    expect(isQABlockError('not an error')).toBe(false)
    expect(isQABlockError(null)).toBe(false)
    expect(isQABlockError(undefined)).toBe(false)
  })
})

describe('getQAIssuesFromError (body extractor)', () => {
  it('extracts the qa_issues array from a QA-block 422 body', () => {
    const issues: QaIssue[] = [
      { check_type: 'tag_missing', severity: 'error', message: 'Missing tag {name}' },
      { check_type: 'punctuation', severity: 'warning', message: 'Trailing period' },
    ]
    expect(getQAIssuesFromError(qaBlockError(issues))).toEqual(issues)
  })

  it('returns an empty array when the QA-block body has no qa_issues field', () => {
    // Defensive — production always emits the field (verified by the
    // backend integration test "ResponseHasEmptyArrayWhenNoWriteTMs"
    // sibling pattern). Helper must still degrade gracefully if a
    // future backend changes the shape.
    const err = new ApiError(422, 'fix QA errors', {
      error: { code: 'QAErrorsBlocked' },
    })
    expect(getQAIssuesFromError(err)).toEqual([])
  })

  it('returns an empty array for non-QA-block errors', () => {
    // Wrong status → no issue extraction. Otherwise a 409 with an
    // accidentally-matching body shape would leak QA semantics into
    // the conflict-handling branch.
    const err = new ApiError(409, 'conflict', { qa_issues: [{ check_type: 'x', severity: 'error', message: 'leak' }] })
    expect(getQAIssuesFromError(err)).toEqual([])
  })

  it('returns an empty array for a non-QA 422 (different error.code)', () => {
    // A 422 with code != "QAErrorsBlocked" is not
    // a QA block. Even if the body happened to carry `qa_issues` (it
    // shouldn't, but defensive), the extractor must NOT leak the data
    // into a non-QA branch.
    const err = new ApiError(422, 'mt engine unavailable', {
      error: { code: 'MTEngineUnavailable' },
      qa_issues: [{ check_type: 'leak', severity: 'error', message: 'should not surface' }],
    })
    expect(getQAIssuesFromError(err)).toEqual([])
  })
})

describe('formatQAErrorToast ignores ignored findings', () => {
  it('counts and quotes only findings that still block', () => {
    // A segment with one ignored error and one live error must read as ONE
    // blocker, and must not quote the ignored finding as the example.
    expect(formatQAErrorToast([
      { check_type: 'terminology', severity: 'error', message: 'Term(s) not used: Acme', ignored: true },
      { check_type: 'tag_missing', severity: 'error', message: 'Source tag(s) missing from target: </1>' },
    ])).toBe('Fix QA error before confirming: Source tag(s) missing from target: </1>')
  })

  it('falls back to the generic message when every error is ignored', () => {
    expect(formatQAErrorToast([
      { check_type: 'terminology', severity: 'error', message: 'Term(s) not used: Acme', ignored: true },
    ])).toBe('Fix QA errors before confirming')
  })
})

describe('formatQAErrorToast (toast text)', () => {
  it('renders the single-error message inline', () => {
    const text = formatQAErrorToast([
      { check_type: 'tag_missing', severity: 'error', message: 'Missing tag {name}' },
    ])
    expect(text).toBe('Fix QA error before confirming: Missing tag {name}')
  })

  it('summarizes multiple errors with a count + first-message sample', () => {
    // Three errors → toast carries the count and the first message as a
    // hint. The translator sees "Fix 3 QA errors..." and knows to look
    // at the inline issue panel for the full list. Avoids a dialog
    // explosion.
    const text = formatQAErrorToast([
      { check_type: 'tag_missing', severity: 'error', message: 'Missing tag {name}' },
      { check_type: 'tag_extra', severity: 'error', message: 'Extra tag {x}' },
      { check_type: 'punctuation', severity: 'error', message: 'Trailing space' },
    ])
    expect(text).toContain('Fix 3 QA errors before confirming')
    expect(text).toContain('Missing tag {name}')
  })

  it('falls back to a generic message when the list has zero errors', () => {
    // Defensive — the helper might be called with all-warnings or
    // empty input from a code path that didn't pre-filter. The
    // confirm-block UX still expects a coherent message.
    expect(formatQAErrorToast([])).toBe('Fix QA errors before confirming')
    expect(formatQAErrorToast([{ check_type: 'p', severity: 'warning', message: 'soft' }])).toBe('Fix QA errors before confirming')
  })

  it('ignores warning/info severities when picking the sample message', () => {
    // The toast should ALWAYS quote an error message, never a warning
    // or info — otherwise the user might think the toast is about a
    // benign hint when in fact a hard error is blocking them.
    const text = formatQAErrorToast([
      { check_type: 'punctuation', severity: 'warning', message: 'Trailing period — soft' },
      { check_type: 'tag_missing', severity: 'error', message: 'HARD ERROR' },
    ])
    expect(text).toBe('Fix QA error before confirming: HARD ERROR')
  })
})
