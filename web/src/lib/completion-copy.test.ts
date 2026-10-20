import { describe, it, expect } from 'vitest'
import { finalStepAutomations, finalStepSentence, finalStepTooltip } from './completion-copy'

describe('finalStepAutomations', () => {
  it('lists completion and the TM save', () => {
    const lines = finalStepAutomations({ approvedCount: 9 })
    expect(lines.map(l => l.key)).toEqual(['complete', 'tm'])
    expect(lines[0].text).toBe('Mark the job as completed')
  })

  it('interpolates the approved count with singular/plural', () => {
    expect(finalStepAutomations({ approvedCount: 9 })[1].text).toBe('Save 9 human-approved segments to Translation memory')
    expect(finalStepAutomations({ approvedCount: 1 })[1].text).toBe('Save 1 human-approved segment to Translation memory')
  })

  it('says nothing will be saved when there are no approved segments', () => {
    const zero = finalStepAutomations({ approvedCount: 0 })
    expect(zero[1].text).toContain('none yet')
    expect(zero.map(l => l.key)).toEqual(['complete', 'tm'])
  })
})

describe('finalStepSentence', () => {
  it('includes the TM save when segments are approved', () => {
    expect(finalStepSentence({ approvedCount: 9 })).toBe(
      'This will mark the job completed and save 9 human-approved segments to Translation memory.',
    )
  })

  it('handles zero approved segments', () => {
    expect(finalStepSentence({ approvedCount: 0 })).toBe('This will mark the job completed.')
  })

  it('names the file when fileLabel is given', () => {
    expect(finalStepSentence({ approvedCount: 1, fileLabel: 'report.docx (de)' })).toBe(
      'This will mark report.docx (de) as completed and save 1 human-approved segment to Translation memory.',
    )
  })
})

describe('finalStepTooltip', () => {
  it('includes the TM save when segments are approved', () => {
    expect(finalStepTooltip({ approvedCount: 9 })).toBe(
      'Final step — completing it will mark the job completed and save 9 human-approved segments to Translation memory.',
    )
  })

  it('drops the TM clause when nothing is approved', () => {
    expect(finalStepTooltip({ approvedCount: 0 })).toBe('Final step — completing it will mark the job completed.')
  })
})
