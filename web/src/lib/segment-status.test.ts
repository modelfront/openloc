import { describe, it, expect } from 'vitest'
import { deriveDisplayStatus, showStatusChip, deriveMatchChip, isAssistantConfirmed } from './segment-status'
import type { Translation } from '../api'

// Builds a Translation with sensible defaults; override only what each case
// needs. Cast is fine — deriveDisplayStatus reads a small fixed subset.
function tr(overrides: Partial<Translation>): Translation {
  return {
    id: 1,
    segment_id: 1,
    target_language: 'fr',
    status: 'needs_review',
    active_text: null,
    tm_match_percent: null,
    predict_outcome: { predict_outcome: '', valid: false },
    ...overrides,
  } as unknown as Translation
}

describe('deriveDisplayStatus', () => {
  it('returns untranslated when there is no translation row', () => {
    expect(deriveDisplayStatus(undefined)).toBe('untranslated')
    expect(deriveDisplayStatus(null)).toBe('untranslated')
  })

  it('maps raw approved / auto_approved straight through', () => {
    expect(deriveDisplayStatus(tr({ status: 'approved', active_text: 'x' }))).toBe('approved')
    expect(deriveDisplayStatus(tr({ status: 'auto_approved', active_text: 'x' }))).toBe('auto_approved')
  })

  it('returns untranslated for needs_review with no active_text', () => {
    expect(deriveDisplayStatus(tr({ status: 'needs_review', active_text: null }))).toBe('untranslated')
    expect(deriveDisplayStatus(tr({ status: 'needs_review', active_text: '' }))).toBe('untranslated')
  })

  it('returns pre_translated for text nobody edited in this step, with no AI verdict', () => {
    expect(deriveDisplayStatus(tr({
      status: 'needs_review',
      active_text: 'Bonjour',
      origin: { translation_origin: 'tm', valid: true },
      step_edited: false,
    }))).toBe('pre_translated')
  })

  it('returns needs_review once a person edits the text in this step', () => {
    expect(deriveDisplayStatus(tr({
      status: 'needs_review',
      active_text: 'Bonjour',
      origin: { translation_origin: 'tm', valid: true },
      edited_by: 7,
      step_edited: true,
    }))).toBe('needs_review')
  })

  it('returns needs_review once the AI gave a verdict in this step', () => {
    expect(deriveDisplayStatus(tr({
      status: 'needs_review',
      active_text: 'Bonjour',
      origin: { translation_origin: 'mt', valid: true },
      step_edited: false,
      predict_outcome: { predict_outcome: 'rejected', valid: true },
    }))).toBe('needs_review')
  })

  it('treats an earlier step\'s AI verdict as history: pre_translated', () => {
    const row = tr({
      status: 'needs_review',
      active_text: 'Bonjour',
      origin: { translation_origin: 'mt', valid: true },
      step_edited: false,
      predict_outcome: { predict_outcome: 'rejected', valid: true },
    })
    expect(deriveDisplayStatus(row, { predictedBeforeStep: true })).toBe('pre_translated')
  })

  it('treats an earlier step\'s human edit as history: pre_translated', () => {
    // Typed from scratch in step 1 (no origin), untouched since step 2 began.
    expect(deriveDisplayStatus(tr({
      status: 'needs_review',
      active_text: 'Bonjour',
      origin: undefined,
      edited_by: 7,
      step_edited: false,
    }))).toBe('pre_translated')
  })
})

describe('showStatusChip (Status column visibility)', () => {
  it('shows a badge only for progressed states: Draft / Auto-approved / Approved', () => {
    expect(showStatusChip('needs_review')).toBe(true)
    expect(showStatusChip('auto_approved')).toBe(true)
    expect(showStatusChip('approved')).toBe(true)
  })

  it('keeps the cell empty for default states (untranslated / pre_translated)', () => {
    // Provenance for pretranslated rows is the Match column's job (MT / % /
    // File chips) — the Status column stays quiet until something progresses.
    expect(showStatusChip('untranslated')).toBe(false)
    expect(showStatusChip('pre_translated')).toBe(false)
  })
})

describe('deriveMatchChip (Match column — one exclusive chip per row)', () => {
  it('maps each origin to its chip when no repetition flags are set', () => {
    expect(deriveMatchChip({ origin: 'tm' })).toBe('tm')
    expect(deriveMatchChip({ origin: 'ape' })).toBe('ape')
    expect(deriveMatchChip({ origin: 'mt' })).toBe('mt')
    expect(deriveMatchChip({ origin: 'file_import' })).toBe('file')
  })

  it('returns null for rows with no automation lineage', () => {
    expect(deriveMatchChip({ origin: '' })).toBe(null)
  })

  it('repetition flags beat every origin (mutual exclusivity priority)', () => {
    expect(deriveMatchChip({ isRepetitionSource: true, origin: 'tm' })).toBe('repetition_source')
    expect(deriveMatchChip({ isRepetition: true, origin: 'mt' })).toBe('repetition')
    expect(deriveMatchChip({ isRepetition: true, origin: 'file_import' })).toBe('repetition')
  })

  it('repetition source beats repetition target when both are (incorrectly) set', () => {
    // The parent component computes these as mutually exclusive; if that ever
    // breaks, source (the propagation trigger) is the safer chip to show.
    expect(deriveMatchChip({ isRepetitionSource: true, isRepetition: true, origin: '' })).toBe('repetition_source')
  })

})

describe('isAssistantConfirmed', () => {
  it('is true only for an approved row the assistant confirmed', () => {
    expect(isAssistantConfirmed(tr({ status: 'approved', active_text: 'x', confirmed_via: 'assistant' }))).toBe(true)
    expect(isAssistantConfirmed(tr({ status: 'approved', active_text: 'x', confirmed_via: 'user' }))).toBe(false)
    expect(isAssistantConfirmed(tr({ status: 'approved', active_text: 'x' }))).toBe(false)
  })

  it('is false once the row is no longer approved (a later draft)', () => {
    expect(isAssistantConfirmed(tr({ status: 'needs_review', active_text: 'x', confirmed_via: 'assistant' }))).toBe(false)
    expect(isAssistantConfirmed(null)).toBe(false)
  })
})
