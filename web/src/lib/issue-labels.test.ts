import { describe, it, expect } from 'vitest'
import { issueDefaultsFromToolArgs, issueWaitingOn, waitingOnLabel } from './issue-labels'

describe('issueWaitingOn', () => {
  it('waits on the side that did not act last on an open issue', () => {
    expect(issueWaitingOn({ status: 'open', last_activity_by: 'reporter' })).toBe('support')
    expect(issueWaitingOn({ status: 'open', last_activity_by: 'operator' })).toBe('reporter')
  })

  it('waits on the support team while in progress, whoever moved it there', () => {
    expect(issueWaitingOn({ status: 'in_progress', last_activity_by: 'operator' })).toBe('support')
    expect(issueWaitingOn({ status: 'in_progress', last_activity_by: 'reporter' })).toBe('support')
  })

  it('waits on the reporter\'s org for an answer', () => {
    expect(issueWaitingOn({ status: 'needs_info', last_activity_by: 'operator' })).toBe('reporter')
  })

  it('waits on nobody once the issue is closed', () => {
    for (const status of ['resolved', 'wont_fix', 'duplicate'] as const) {
      expect(issueWaitingOn({ status, last_activity_by: 'reporter' })).toBeNull()
    }
  })

  it('names the reporter\'s side for each viewer', () => {
    expect(waitingOnLabel('reporter', 'mine')).toBe('Your organization')
    expect(waitingOnLabel('reporter', 'platform')).toBe("Reporter's org")
    expect(waitingOnLabel('support', 'mine')).toBe('Support team')
  })
})

describe('issueDefaultsFromToolArgs', () => {
  const args = {
    kind: 'bug', severity: 'major', area: 'editor', title: 'Editor stops at 100', description: 'It does.',
    verification: 'verified', verification_note: 'The job has 120 segments.',
  }

  it("keeps the assistant's check with the token that proves it", () => {
    expect(issueDefaultsFromToolArgs(args, 'tok').verification)
      .toEqual({ result: 'verified', note: 'The job has 120 segments.', token: 'tok' })
  })

  it('drops a check without a token, a note or a known result', () => {
    expect(issueDefaultsFromToolArgs(args).verification).toBeUndefined()
    expect(issueDefaultsFromToolArgs({ ...args, verification_note: undefined }, 'tok').verification).toBeUndefined()
    expect(issueDefaultsFromToolArgs({ ...args, verification: 'probably' }, 'tok').verification).toBeUndefined()
  })

  it('falls back to the form defaults for values it does not know', () => {
    expect(issueDefaultsFromToolArgs({ kind: 'question', area: 'kitchen' })).toMatchObject({ kind: undefined, area: undefined })
  })
})
