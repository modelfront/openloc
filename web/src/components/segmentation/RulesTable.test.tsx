import { describe, it, expect } from 'vitest'
import { useState } from 'react'
import { render, screen, fireEvent } from '@testing-library/react'
import { RulesTable, withKey, type KeyedRule } from './RulesTable'

const rule = (before: string, brk = true) => withKey({ before, after: '\\s', break: brk })

function Harness({ initial, readOnly = false, errorIndex = null, error, onRules }: {
  initial: KeyedRule[]; readOnly?: boolean; errorIndex?: number | null; error?: string; onRules?: (r: KeyedRule[]) => void
}) {
  const [rules, setRules] = useState(initial)
  return (
    <RulesTable rules={rules} readOnly={readOnly} errorIndex={errorIndex} error={error}
      onChange={r => { setRules(r); onRules?.(r) }} />
  )
}

describe('RulesTable', () => {
  it('adds a break rule at the end, edits a pattern and removes a rule', () => {
    let latest: KeyedRule[] = []
    render(<Harness initial={[rule('\\.'), rule('!', false)]} onRules={r => { latest = r }} />)

    fireEvent.click(screen.getByTestId('segmentation-rule-add'))
    expect(latest.map(r => [r.before, r.break])).toEqual([['\\.', true], ['!', false], ['', true]])

    fireEvent.change(screen.getByTestId('segmentation-rule-before-2'), { target: { value: '\\?' } })
    expect(latest[2].before).toBe('\\?')

    fireEvent.click(screen.getByTestId('segmentation-rule-remove-0'))
    expect(latest.map(r => r.before)).toEqual(['!', '\\?'])
    expect(screen.getByTestId('segmentation-rule-before-0')).toHaveValue('!')
  })

  it('switches a rule between Break and No break', () => {
    let latest: KeyedRule[] = []
    render(<Harness initial={[rule('\\.')]} onRules={r => { latest = r }} />)
    fireEvent.click(screen.getByRole('radio', { name: 'No break' }))
    expect(latest[0].break).toBe(false)
  })

  it('marks the rule an invalid pattern names, with the server message', () => {
    render(<Harness initial={[rule('\\.'), rule('(?=x)')]} errorIndex={1}
      error="Rule 2: the before pattern is not valid: look-ahead is not supported." />)
    expect(screen.getByTestId('segmentation-rule-before-1')).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByTestId('segmentation-rule-before-0')).not.toHaveAttribute('aria-invalid')
    expect(screen.getByTestId('segmentation-rule-error-1')).toHaveTextContent(/^The before pattern is not valid: look-ahead is not supported\.$/)
  })

  it('shows the message under its rule without the rule number', () => {
    render(<Harness initial={[rule('\\.'), withKey({ before: '', after: '', break: true })]} errorIndex={1}
      error="Rule 2 needs a before or an after pattern." />)
    expect(screen.getByTestId('segmentation-rule-error-1')).toHaveTextContent(/^Rule needs a before or an after pattern\.$/)
  })

  it('read-only: no drag handles, remove buttons or add row, and the fields do not change', () => {
    render(<Harness initial={[rule('\\.')]} readOnly />)
    expect(screen.queryByTestId('segmentation-rule-drag-0')).toBeNull()
    expect(screen.queryByTestId('segmentation-rule-remove-0')).toBeNull()
    expect(screen.queryByTestId('segmentation-rule-add')).toBeNull()
    expect(screen.getByTestId('segmentation-rule-before-0')).toHaveAttribute('readonly')
  })
})
