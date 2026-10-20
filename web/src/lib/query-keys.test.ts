import { describe, it, expect } from 'vitest'
import { queryKeys } from './query-keys'

describe('queryKeys', () => {
  it('returns consistent static keys', () => {
    expect(queryKeys.projects.all).toEqual(['projects'])
    expect(queryKeys.tms.all).toEqual(['tms'])
    expect(queryKeys.terminology.all).toEqual(['terminology'])
    expect(queryKeys.mtEngines.all).toEqual(['mt-engines'])
    expect(queryKeys.templates.all).toEqual(['templates'])
    expect(queryKeys.qaProfiles.all).toEqual(['qa-profiles'])
    expect(queryKeys.languages.all).toEqual(['languages'])
    expect(queryKeys.notifications.all).toEqual(['notifications'])
    expect(queryKeys.notifications.count).toEqual(['notification-count'])
  })

  it('returns parameterized keys with correct structure', () => {
    expect(queryKeys.job.detail(42)).toEqual(['job', 42])
    expect(queryKeys.job.translations(42)).toEqual(['job-translations', 42])
    expect(queryKeys.job.steps(42)).toEqual(['job-steps', 42])
    expect(queryKeys.job.analysis(42)).toEqual(['job-analysis', 42])
    expect(queryKeys.jobs.byProject(10)).toEqual(['jobs', 10])
    expect(queryKeys.files.byProject(10)).toEqual(['files', 10])
    expect(queryKeys.segments.byFile(5)).toEqual(['segments', 5])
    expect(queryKeys.template.detail(3)).toEqual(['template', 3])
    expect(queryKeys.qaProfile.detail(7)).toEqual(['qa-profile', 7])
    expect(queryKeys.comments.byTranslation(99)).toEqual(['comments', 99])
    expect(queryKeys.projectAnalysis.v2(1)).toEqual(['project-analysis-v2', 1])
  })

  it('terminology is the canonical name (not term-bases)', () => {
    // Pins one canonical name, so every terminology consumer shares the
    // same cache and invalidation.
    expect(queryKeys.terminology.all).toEqual(['terminology'])
    expect(queryKeys.terminology.all[0]).not.toBe('term-bases')
  })
})
