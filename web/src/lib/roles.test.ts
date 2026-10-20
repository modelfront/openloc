import { describe, it, expect } from 'vitest'
import { isAdminRole, isManagerRole, isLinguistRole, roleLabel } from './roles'

describe('roles', () => {
  it('isAdminRole is true only for owner/admin', () => {
    expect(isAdminRole('owner')).toBe(true)
    expect(isAdminRole('admin')).toBe(true)
    for (const r of ['project_manager', 'linguist', '', null, undefined]) {
      expect(isAdminRole(r)).toBe(false)
    }
  })

  it('isManagerRole is true for owner/admin/project_manager', () => {
    for (const r of ['owner', 'admin', 'project_manager']) {
      expect(isManagerRole(r)).toBe(true)
    }
    for (const r of ['linguist', '', null, undefined]) {
      expect(isManagerRole(r)).toBe(false)
    }
  })

  it('isLinguistRole is true only for linguist', () => {
    expect(isLinguistRole('linguist')).toBe(true)
    for (const r of ['owner', 'admin', 'project_manager', '', null, undefined]) {
      expect(isLinguistRole(r)).toBe(false)
    }
  })

  it('roleLabel maps each role to its sentence-case label', () => {
    expect(roleLabel('owner')).toBe('Owner')
    expect(roleLabel('admin')).toBe('Admin')
    expect(roleLabel('project_manager')).toBe('Project manager')
    expect(roleLabel('linguist')).toBe('Linguist')
  })

  it('roleLabel falls back to the raw value for unknown/empty roles', () => {
    expect(roleLabel('reviewer')).toBe('reviewer')
    expect(roleLabel('')).toBe('')
    expect(roleLabel(null)).toBe('')
    expect(roleLabel(undefined)).toBe('')
  })
})
