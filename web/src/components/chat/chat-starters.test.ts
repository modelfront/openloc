import { describe, it, expect } from 'vitest'
import { chatStarter, LINGUIST_STARTER, SETUP_STARTER, WORK_STARTER } from './chat-starters'

describe('chatStarter', () => {
  it('a manager with no projects gets the setup question', () => {
    expect(chatStarter('project_manager', false)).toBe(SETUP_STARTER)
  })

  it('a manager with projects gets the work question', () => {
    expect(chatStarter('owner', true)).toBe(WORK_STARTER)
  })

  it('a linguist gets the task question, whatever the projects', () => {
    expect(chatStarter('linguist', undefined)).toBe(LINGUIST_STARTER)
    expect(chatStarter('linguist', false)).toBe(LINGUIST_STARTER)
  })

  it('offers nothing while the project check is loading', () => {
    expect(chatStarter('admin', undefined)).toBeUndefined()
  })
})
