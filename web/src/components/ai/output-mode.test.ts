import { describe, it, expect } from 'vitest'
import { derivePurpose, extensionMode, flagsToMode, type OutputFlags } from './output-mode'

// Flag fixtures matching the built-in default prompts (internal/aiprompt/defaults.go).
const MT: OutputFlags = { out_status: false, out_text: true, out_note: true, out_comments: false }
const QE: OutputFlags = { out_status: true, out_text: false, out_note: true, out_comments: true }
const QE_APE: OutputFlags = { out_status: true, out_text: true, out_note: true, out_comments: true }
const APE: OutputFlags = { out_status: false, out_text: true, out_note: true, out_comments: true }

describe('flagsToMode', () => {
  it('classifies each default prompt by its flags', () => {
    expect(flagsToMode(MT)).toBe('mt')
    expect(flagsToMode(QE)).toBe('qe')
    expect(flagsToMode(QE_APE)).toBe('qe_ape')
    expect(flagsToMode(APE)).toBe('ape')
  })
})

// derivePurpose is the exact rule the AI-engine form uses to decide MT vs
// QE+APE. Regression coverage for "can't create an MT engine": selecting an MT
// prompt on a new LLM engine MUST yield purpose 'mt'.
describe('derivePurpose', () => {
  it('a new LLM engine with an MT prompt is an MT engine', () => {
    expect(derivePurpose(undefined, false, MT)).toBe('mt')
  })

  it('a new LLM engine with a QE+APE, QE, or APE prompt is a QE+APE engine', () => {
    expect(derivePurpose(undefined, false, QE_APE)).toBe('qe_ape')
    expect(derivePurpose(undefined, false, QE)).toBe('qe_ape')
    expect(derivePurpose(undefined, false, APE)).toBe('qe_ape')
  })

  it('a new extension engine is always QE+APE, regardless of prompt', () => {
    expect(derivePurpose(undefined, true, undefined)).toBe('qe_ape')
    // Extension rows carry no prompt, but even if one leaked through it stays QE+APE.
    expect(derivePurpose(undefined, true, MT)).toBe('qe_ape')
  })

  it('falls back to QE+APE when no prompt is selected yet', () => {
    expect(derivePurpose(undefined, false, undefined)).toBe('qe_ape')
  })

  it('an existing engine keeps its stored purpose (immutable after creation)', () => {
    // Even if the picked prompt would derive differently, editing never changes purpose.
    expect(derivePurpose('mt', false, QE_APE)).toBe('mt')
    expect(derivePurpose('qe_ape', false, MT)).toBe('qe_ape')
  })
})

describe('extensionMode', () => {
  it('follows the capabilities the manifest declares', () => {
    expect(extensionMode(['qe', 'ape'])).toBe('qe_ape')
    expect(extensionMode(['qe'])).toBe('qe')
    expect(extensionMode(['ape'])).toBe('ape')
  })
})
