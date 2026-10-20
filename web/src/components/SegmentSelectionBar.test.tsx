import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SegmentSelectionBar } from './SegmentSelectionBar'

// Lock approves the selected segments, so only managers get it; anyone who
// may edit gets Unlock (the routes enforce the same: RequireManager on lock,
// no role gate on unlock).

function renderBar(gates: { canLock: boolean; canUnlock: boolean }) {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <SegmentSelectionBar
        jobId={1}
        projectId={1}
        picked={[10]}
        selectedCount={1}
        translationsById={new Map()}
        canConfirm
        canClear={false}
        canPretranslate={false}
        beforeRun={async () => {}}
        onSpellCheck={() => {}}
        onFindReplace={() => {}}
        onClearSelection={() => {}}
        {...gates}
      />
    </QueryClientProvider>,
  )
}

describe('SegmentSelectionBar lock gates', () => {
  it('a translator gets Unlock but not Lock', () => {
    renderBar({ canLock: false, canUnlock: true })
    expect(screen.queryByTestId('segment-bulk-lock')).toBeNull()
    expect(screen.getByTestId('segment-bulk-unlock')).toBeTruthy()
  })

  it('a manager gets both', () => {
    renderBar({ canLock: true, canUnlock: true })
    expect(screen.getByTestId('segment-bulk-lock')).toBeTruthy()
    expect(screen.getByTestId('segment-bulk-unlock')).toBeTruthy()
  })
})
