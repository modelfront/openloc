import { describe, it, expect } from 'vitest'
import { chipDotClass, chipTextClass } from '@/components/ui/chip'
import { activeIssues, countQASummary, qaButtonLabel, qaRunToast, qaSelectionToast, qaIssuesEqual, mergeQAReportRows, patchQAReport, groupFindingsByTranslation, severityDotClass, severityTextClass, severityColor, severityLabel, worstSeverity, findingDotClass, IGNORED_DOT_CLASS, EMPTY_QA_SUMMARY, type QaFinding } from './qa-findings'

const finding = (over: Partial<QaFinding>): QaFinding => ({
  id: 1, translation_id: 10, check_type: 'punctuation', severity: 'warning',
  message: 'Punctuation mismatch', created_at: '2026-09-07T10:00:00Z', ignored: false, ...over,
})

describe('countQASummary', () => {
  it('moves ignored findings out of their severity bucket', () => {
    const s = countQASummary([
      finding({ id: 1, severity: 'error' }),
      finding({ id: 2, severity: 'error', ignored: true }),
      finding({ id: 3, severity: 'warning' }),
      finding({ id: 4, severity: 'info', ignored: true }),
    ])
    expect(s).toEqual({ error: 1, warning: 1, info: 0, ignored: 2 })
  })
})

describe('activeIssues', () => {
  it('drops ignored findings', () => {
    const out = activeIssues([finding({ id: 1 }), finding({ id: 2, ignored: true })])
    expect(out.map(f => f.id)).toEqual([1])
  })
})

describe('qaButtonLabel', () => {
  it('shows "Run QA" before any report', () => {
    expect(qaButtonLabel(false, null)).toBe('Run QA')
  })
  it('shows "Checking..." while pending', () => {
    expect(qaButtonLabel(true, null)).toBe('Checking...')
  })
  it('counts errors + warnings only', () => {
    expect(qaButtonLabel(false, { error: 2, warning: 3, info: 1, ignored: 0 })).toBe('QA (5)')
  })
  it('does not count or mention ignored findings', () => {
    expect(qaButtonLabel(false, { error: 1, warning: 0, info: 0, ignored: 2 })).toBe('QA (1)')
  })
  it('reads "Run QA" when nothing is open, even after a run', () => {
    expect(qaButtonLabel(false, { error: 0, warning: 0, info: 2, ignored: 3 })).toBe('Run QA')
  })
})

describe('qaRunToast', () => {
  it('mentions ignored findings only when present', () => {
    expect(qaRunToast({ error: 1, warning: 1, info: 0, ignored: 0 })).toBe('QA completed: 2 issue(s) found')
    expect(qaRunToast({ error: 0, warning: 0, info: 0, ignored: 3 })).toBe('QA completed: 0 issue(s) found, 3 ignored')
  })
})

describe('qaSelectionToast', () => {
  it('counts open findings on the selected rows only', () => {
    const results = [
      { translation_id: 1, severity: 'error', ignored: false },
      { translation_id: 1, severity: 'warning', ignored: false },
      { translation_id: 2, severity: 'error', ignored: true },
      { translation_id: 2, severity: 'info', ignored: false },
      { translation_id: 9, severity: 'error', ignored: false },
    ]
    expect(qaSelectionToast(results, [1, 2])).toBe('QA on 2 segments: 2 issue(s) found')
    expect(qaSelectionToast(results, [2])).toBe('QA on 1 segment: 0 issue(s) found')
  })
})

describe('qaIssuesEqual', () => {
  it('is false when only the ignored flag differs — the dot must re-render', () => {
    const a = [finding({ id: 1 })]
    const b = [finding({ id: 1, ignored: true })]
    expect(qaIssuesEqual(a, b)).toBe(false)
  })
  it('is true for structurally equal lists', () => {
    expect(qaIssuesEqual([finding({ id: 1 })], [finding({ id: 1 })])).toBe(true)
  })
  it('is false when lengths differ', () => {
    expect(qaIssuesEqual([finding({ id: 1 })], [])).toBe(false)
  })

  // UpsertQAResult rewrites severity + message in place on ON CONFLICT
  // (translation_id, check_type), so a re-run can hand back the SAME row id
  // with new prose. Holding id and check_type constant is what pins the
  // message and severity legs — without these two cases either can be deleted
  // from qaIssuesEqual with the whole suite still green, and the popover would
  // keep showing the previous run's text.
  it('is false when only the message differs, at the same id and check_type', () => {
    const a = [finding({ id: 1, message: 'Punctuation mismatch — missing leading "•"' })]
    const b = [finding({ id: 1, message: 'Punctuation mismatch — trailing "." not in source' })]
    expect(qaIssuesEqual(a, b)).toBe(false)
  })

  it('is false when only the severity differs, at the same id and check_type', () => {
    const a = [finding({ id: 1, severity: 'warning' })]
    const b = [finding({ id: 1, severity: 'error' })]
    expect(qaIssuesEqual(a, b)).toBe(false)
  })

  it('is false when only the check_type differs, at the same id', () => {
    const a = [finding({ id: 1, check_type: 'punctuation' })]
    const b = [finding({ id: 1, check_type: 'terminology' })]
    expect(qaIssuesEqual(a, b)).toBe(false)
  })
})

describe('patchQAReport', () => {
  it('is the last writer even when a stale refetch is in flight', async () => {
    const { QueryClient } = await import('@tanstack/react-query')
    const qc = new QueryClient()
    const key = ['job-qa-report', 42]
    const stale = { summary: { error: 1, warning: 0, info: 0, ignored: 0 }, results: [finding({ id: 1, translation_id: 10, severity: 'error' })] }
    qc.setQueryData(key, stale)

    // A refetch that will resolve LATE with the pre-confirm rows.
    let resolveFetch!: (v: typeof stale) => void
    const inflight = qc.fetchQuery({
      queryKey: key,
      queryFn: () => new Promise<typeof stale>(res => { resolveFetch = res }),
      staleTime: 0,
    }).catch(() => undefined) // cancellation rejects; that is the point

    // Fresh per-segment result: the finding is now ignored.
    await patchQAReport(qc, key, 10, [finding({ id: 1, translation_id: 10, severity: 'error', ignored: true })])
    resolveFetch(stale)
    await inflight

    const after = qc.getQueryData<typeof stale>(key)!
    expect(after.results[0].ignored, 'the late refetch must not overwrite the patch').toBe(true)
    expect(after.summary).toEqual({ error: 0, warning: 0, info: 0, ignored: 1 })
  })
})

describe('mergeQAReportRows', () => {
  it('seeds an empty report and recounts', () => {
    const out = mergeQAReportRows(undefined, 10, [finding({ id: 1, translation_id: 10, severity: 'error' })])
    expect(out.summary).toEqual({ error: 1, warning: 0, info: 0, ignored: 0 })
  })
  it('replaces only the given translation rows', () => {
    const prev = { summary: EMPTY_QA_SUMMARY, results: [finding({ id: 1, translation_id: 10 }), finding({ id: 2, translation_id: 11 })] }
    const out = mergeQAReportRows(prev, 10, [])
    expect(out.results.map(r => r.translation_id)).toEqual([11])
  })
})

describe('groupFindingsByTranslation', () => {
  it('returns an empty map for no report', () => {
    expect(groupFindingsByTranslation(undefined).size).toBe(0)
  })
  it('groups rows by translation_id, each with its own array', () => {
    const map = groupFindingsByTranslation([
      finding({ id: 1, translation_id: 100, check_type: 'tag_missing' }),
      finding({ id: 2, translation_id: 100, check_type: 'number_consistency' }),
      finding({ id: 3, translation_id: 200 }),
    ])
    expect(map.size).toBe(2)
    expect(map.get(100)!.map(f => f.check_type)).toEqual(['tag_missing', 'number_consistency'])
    map.get(100)!.push(finding({ id: 9, translation_id: 100 }))
    expect(map.get(200)!.length).toBe(1)
  })
})

describe('severity display helpers', () => {
  it('maps severities to chip colours, unknown to blue', () => {
    expect(severityColor('error')).toBe('red')
    expect(severityColor('warning')).toBe('yellow')
    expect(severityColor('info')).toBe('blue')
    expect(severityColor('unknown')).toBe('blue')
  })
  it('labels severities for display and passes unknown values through', () => {
    expect(severityLabel('error')).toBe('Error')
    expect(severityLabel('warning')).toBe('Warning')
    expect(severityLabel('custom')).toBe('custom')
  })

  // Dot and word take the chip palette, so a severity reads in the same colour
  // as its chip on the QA profiles page.
  it.each([
    ['error', 'red'],
    ['warning', 'yellow'],
    ['info', 'blue'],
    ['unknown', 'blue'],
  ] as const)('paints %s with the %s chip palette', (severity, color) => {
    expect(severityDotClass(severity)).toBe(chipDotClass(color))
    expect(severityTextClass(severity)).toBe(chipTextClass(color))
  })
})

describe('worstSeverity', () => {
  it('picks error over warning over info, whatever the order', () => {
    expect(worstSeverity([finding({ severity: 'info' }), finding({ severity: 'error' }), finding({ severity: 'warning' })])).toBe('error')
    expect(worstSeverity([finding({ severity: 'info' }), finding({ severity: 'warning' })])).toBe('warning')
    expect(worstSeverity([finding({ severity: 'info' })])).toBe('info')
  })
  it('ignores ignored findings — an ignored error does not outrank an open warning', () => {
    expect(worstSeverity([finding({ severity: 'error', ignored: true }), finding({ severity: 'warning' })])).toBe('warning')
  })
  it('is null when every finding is ignored, or there are none', () => {
    expect(worstSeverity([finding({ severity: 'error', ignored: true })])).toBeNull()
    expect(worstSeverity([])).toBeNull()
  })
})

describe('findingDotClass', () => {
  it('fills by severity, rings when ignored', () => {
    expect(findingDotClass(finding({ severity: 'error' }))).toBe(chipDotClass('red'))
    expect(findingDotClass(finding({ severity: 'error', ignored: true }))).toBe(IGNORED_DOT_CLASS)
  })
})
