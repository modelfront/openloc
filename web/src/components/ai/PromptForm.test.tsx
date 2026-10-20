import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { PromptForm } from './PromptForm'
import type { AIPromptWithAdditions } from '../../api'
import { endReveal, revealAnchor } from '../../lib/guide-reveal'

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

const previewAIPrompt = vi.fn()

vi.mock('../../api', () => ({
  listAIVendors: () => Promise.resolve([
    { provider: 'openai', models: [{ id: 'gpt-4o', label: 'GPT-4o' }] },
    { provider: 'claude', models: [{ id: 'claude-haiku-4-5', label: 'Haiku' }] },
  ]),
  previewAIPrompt: (input: unknown) => previewAIPrompt(input),
  // The language-additions field offers the full language list.
  listLanguages: () => Promise.resolve([{ code: 'de', name: 'German' }]),
}))

function prompt(overrides: Partial<AIPromptWithAdditions> = {}): AIPromptWithAdditions {
  return {
    id: 1,
    tenant_id: 1,
    name: 'Built-in QE',
    prompt_text: 'Review the translation.',
    // QE: status + note, no target text — keeps it off the MT-purpose list.
    out_status: true,
    out_text: false,
    out_note: true,
    out_comments: false,
    created_at: '2026-09-01T00:00:00Z',
    updated_at: '2026-09-01T00:00:00Z',
    is_default: true,
    language_additions: [],
    segment_context: [],
    ...overrides,
  }
}

// Renders the form directly. `readOnly` follows is_default, the way
// PromptDetailPage sets it.
function renderForm(p: AIPromptWithAdditions, purpose: 'mt' | 'qe_ape' = 'qe_ape') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <PromptForm
          initial={p}
          readOnly={p.is_default}
          purpose={purpose}
          isPending={false}
          onSubmit={() => {}}
          onCancel={() => {}}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

// The instructions field is a TipTap surface, not a textarea — it has to draw
// the language tokens as chips inside the text being edited. So it is read as
// an element with text content rather than as a control with a value.
const instructions = () => screen.getByTestId('ai-prompt-instructions')
const instructionsText = () => instructions().textContent ?? ''

beforeEach(() => {
  vi.clearAllMocks()
})

describe('PromptForm — saved type', () => {
  it('leaves the saved name and type to the page breadcrumb', () => {
    renderForm(prompt({ is_default: false }))
    expect(screen.queryByTestId('ai-prompt-name')).not.toBeInTheDocument()
    expect(screen.queryByTestId('ai-prompt-type')).not.toBeInTheDocument()
    expect(screen.queryByText('What this prompt does')).not.toBeInTheDocument()
  })
})

describe('PromptForm — unsaved changes', () => {
  it('enables Update only for changes, including context, and disables it after reverting', async () => {
    renderForm(prompt({ is_default: false }))
    const update = screen.getByRole('button', { name: 'Update' })
    expect(update).toBeDisabled()
    expect(screen.queryByRole('button', { name: /^Delete$/ })).not.toBeInTheDocument()
    await userEvent.click(screen.getByTestId('ai-prompt-context-toggle'))
    await userEvent.click(screen.getByTestId('segment-context-terms'))
    expect(update).toBeEnabled()
    await userEvent.click(screen.getByTestId('segment-context-terms'))
    expect(update).toBeDisabled()
  })

  it('keeps context explanations in info tooltips', async () => {
    renderForm(prompt({ is_default: false }))
    await userEvent.click(screen.getByTestId('ai-prompt-context-toggle'))
    expect(screen.queryByText(/The closest TM entry/)).not.toBeInTheDocument()
    await userEvent.hover(screen.getByLabelText('About Translation memory match'))
    expect(await screen.findByText(/The closest TM entry/)).toBeVisible()
  })
})

// A built-in ships the stock wording, so it opens to read rather than to edit.
describe('PromptForm — read-only built-ins', () => {
  it('disables every control', async () => {
    renderForm(prompt({ is_default: true }))

    expect(instructions()).toHaveAttribute('contenteditable', 'false')
    expect(screen.queryByTestId('ai-prompt-type')).not.toBeInTheDocument()
    await userEvent.click(screen.getByTestId('ai-prompt-context-toggle'))
    expect(screen.getByTestId('segment-context-tm_match')).toBeDisabled()
  })

  it('leaves a user-created prompt editable', async () => {
    renderForm(prompt({ is_default: false }))

    expect(instructions()).toHaveAttribute('contenteditable', 'true')
    expect(screen.queryByTestId('ai-prompt-type')).not.toBeInTheDocument()
  })
})

// Placeholders are inserted at the cursor rather than shown as a legend the
// author has to retype.
describe('PromptForm — placeholders', () => {
  // Only the language pair is offered: nothing else in a system prompt is
  // substituted, and a token that never resolves reaches the model verbatim.
  it('offers the language tokens and nothing else', async () => {
    renderForm(prompt({ is_default: false }))

    expect(screen.getByRole('button', { name: '<source_lang>' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '<customer>' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '<domain>' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '<step>' })).not.toBeInTheDocument()
  })

  it('inserts the token into the instructions', async () => {
    renderForm(prompt({ is_default: false, prompt_text: '' }))

    await userEvent.click(screen.getByRole('button', { name: '<target_lang>' }))

    expect(instructionsText()).toBe('<target_lang>')
  })

  // The tokens look like something the model reads literally, so the hint has
  // to say what they become.
  it('shows token help only on hover', async () => {
    renderForm(prompt({ is_default: false }))
    expect(screen.queryByTestId('ai-prompt-placeholder-hint')).not.toBeInTheDocument()
    await userEvent.hover(screen.getByRole('button', { name: '<target_lang>' }))
    const hint = await screen.findByTestId('ai-prompt-placeholder-hint')
    expect(hint).toHaveTextContent("Replaced with the job's target language")
    expect(hint).toHaveTextContent('Austrian German (de-AT)')
    await userEvent.unhover(screen.getByRole('button', { name: '<target_lang>' }))
  })

  it('offers no insert buttons on a read-only built-in', async () => {
    renderForm(prompt({ is_default: true }))

    expect(screen.queryByRole('button', { name: '<target_lang>' })).not.toBeInTheDocument()
    expect(screen.queryByTestId('ai-prompt-placeholder-hint')).not.toBeInTheDocument()
  })
})

// The platform's half of the prompt — the appended contract and the generated
// segment body, loaded when the preview is expanded.
const PREVIEW = {
  instruction: 'Review the translation.',
  appended: '\n\nYou are evaluating a machine translation from <source_lang> to <target_lang>.',
  system_prompt: 'irrelevant here',
  context_preview: [{ id: '1', source: '<source text>', translation: '<target text>' }],
  output_format: 'Return one result per segment with approved and score.',
  mode: 'qe',
  schema_enforced: false,
}

// The author cannot change any of this, which is exactly why it is shown.
describe('PromptForm — what gets sent to the model', () => {
  // The contract names the context fields the segments will carry, so the
  // preview has to be told which ones are checked — otherwise it describes a
  // different request from the one that runs.
  it('asks for the contract of the prompt’s own mode and context', async () => {
    previewAIPrompt.mockResolvedValue(PREVIEW)
    renderForm(prompt({ is_default: false, segment_context: ['tm_match'] }))

    await userEvent.click(screen.getByTestId('ai-prompt-output_format-toggle'))
    await screen.findByTestId('prompt-output_format')
    expect(previewAIPrompt).toHaveBeenCalledWith(
      expect.objectContaining({
        out_status: true,
        out_text: false,
        segment_context: ['tm_match'],
      }),
    )
  })

  it('says so instead of rendering nothing when the preview fails', async () => {
    previewAIPrompt.mockRejectedValue(new Error('boom'))
    renderForm(prompt({ is_default: true }))

    await userEvent.click(screen.getByTestId('ai-prompt-output_format-toggle'))
    expect(await screen.findByText(/could not be loaded/i)).toBeInTheDocument()
  })
})

describe('PromptForm — collapsible prompt and formats', () => {
  beforeEach(() => { previewAIPrompt.mockResolvedValue(PREVIEW) })

  it('preserves base prompt edits when collapsed and reopened', async () => {
    renderForm(prompt({ is_default: false, prompt_text: '' }))
    await userEvent.click(screen.getByRole('button', { name: '<target_lang>' }))
    await userEvent.click(screen.getByTestId('ai-prompt-base-toggle'))
    expect(screen.queryByTestId('ai-prompt-instructions')).not.toBeInTheDocument()
    await userEvent.click(screen.getByTestId('ai-prompt-base-toggle'))
    expect(instructionsText()).toBe('<target_lang>')
    expect(screen.getByRole('button', { name: 'Update' })).toBeEnabled()
  })

  it('shows one editor page without Build or Preview tabs', () => {
    renderForm(prompt({ is_default: false }))
    expect(screen.queryByRole('tab', { name: 'Build' })).not.toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: 'Preview' })).not.toBeInTheDocument()
    expect(screen.getByTestId('ai-prompt-instructions')).toBeVisible()
    expect(screen.getByTestId('ai-prompt-context-toggle')).toBeVisible()
    expect(screen.getByTestId('ai-prompt-output_format-toggle')).toBeVisible()
    // No input format section: a segment arrives as JSON with named keys, so
    // there is nothing to describe that the Context body does not already show.
    expect(screen.queryByTestId('ai-prompt-input_format-toggle')).not.toBeInTheDocument()
  })

  // One schema, no provider to pick, shown with the output format: OpenAI and
  // Claude are held to the same shape, and for a model that takes one the schema
  // IS the output format.
  it('shows the schema with the output format, with nothing to pick', async () => {
    previewAIPrompt.mockResolvedValue({ ...PREVIEW, schema_enforced: true, output_schema: { type: 'object', title: 'segment_results' } })
    renderForm(prompt({ is_default: false }))
    expect(screen.queryByTestId('prompt-output-schema')).not.toBeInTheDocument()
    await userEvent.click(screen.getByTestId('ai-prompt-output_format-toggle'))
    expect(await screen.findByTestId('prompt-output-schema')).toHaveTextContent('segment_results')
    // Captioned: bare JSON under prose reads as a sample reply, not as the rule.
    expect(screen.getByText(/JSON schema/)).toBeVisible()
    expect(screen.queryByTestId('prompt-schema-toggle')).not.toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: 'OpenAI' })).not.toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: 'Claude' })).not.toBeInTheDocument()
    expect(screen.queryByTestId('prompt-output-model')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Update' })).toBeDisabled()
  })

  it('shows the output format on its own, without the combined preview', async () => {
    renderForm(prompt({ is_default: false }))
    expect(screen.queryByTestId('prompt-output_format')).not.toBeInTheDocument()
    await userEvent.click(screen.getByTestId('ai-prompt-output_format-toggle'))
    expect(await screen.findByTestId('prompt-output_format')).toHaveTextContent(PREVIEW.output_format)
    expect(screen.queryByTestId('prompt-contract')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Update' })).toBeDisabled()
  })
})

// The per-segment body is chosen with checkboxes, not written by hand.
describe('PromptForm — segment context', () => {
  beforeEach(() => { previewAIPrompt.mockResolvedValue(PREVIEW) })

  it('checks the context the prompt stores, and leaves the rest off', async () => {
    renderForm(prompt({ is_default: false, segment_context: ['tm_match'] }))

    expect(screen.queryByTestId('segment-context')).not.toBeInTheDocument()
    await userEvent.click(screen.getByTestId('ai-prompt-context-toggle'))
    expect(screen.getByTestId('segment-context-tm_match')).toBeChecked()
    expect(screen.getByTestId('segment-context-terms')).not.toBeChecked()
  })

  it('keeps the context preview mounted while a checkbox change loads', async () => {
    renderForm(prompt({ is_default: false }))
    await userEvent.click(screen.getByTestId('ai-prompt-context-toggle'))
    const preview = await screen.findByTestId('context-json-preview')
    previewAIPrompt.mockImplementation(() => new Promise(() => {}))
    await userEvent.click(screen.getByTestId('segment-context-terms'))
    expect(screen.getByTestId('context-json-preview')).toBe(preview)
    expect(preview).toHaveTextContent('<source text>')
    expect(screen.queryByText('Loading…')).not.toBeInTheDocument()
    await userEvent.click(screen.getByTestId('segment-context-terms'))
    expect(screen.getByTestId('context-json-preview')).toBe(preview)
  })

  it('shows required source and target text as checked, disabled boxes', async () => {
    renderForm(prompt({ is_default: false }))
    await userEvent.click(screen.getByTestId('ai-prompt-context-toggle'))
    for (const field of ['source_text', 'target_text']) {
      expect(screen.getByTestId(`segment-context-${field}`)).toBeChecked()
      expect(screen.getByTestId(`segment-context-${field}`)).toBeDisabled()
    }
  })

  it('cannot be edited on a read-only built-in', async () => {
    renderForm(prompt({ is_default: true }))

    await userEvent.click(screen.getByTestId('ai-prompt-context-toggle'))
    expect(screen.getByTestId('segment-context-tm_match')).toBeDisabled()
  })

  // Both fields describe the source, so both suit every mode. A segment note
  // and the comments people leave are about a translation and are offered
  // nowhere (segmentprompt.Optional).
  it('offers the same source-side context on every mode', async () => {
    renderForm(prompt({
      name: 'Built-in MT', is_default: false,
      out_status: false, out_text: true, out_note: false, out_comments: false,
    }), 'mt')

    await userEvent.click(screen.getByTestId('ai-prompt-context-toggle'))
    expect(screen.getByTestId('segment-context-tm_match')).toBeInTheDocument()
    expect(screen.getByTestId('segment-context-terms')).toBeInTheDocument()
    expect(screen.queryByTestId('segment-context-note')).not.toBeInTheDocument()
    expect(screen.queryByTestId('segment-context-comments')).not.toBeInTheDocument()
    expect(screen.getByTestId('segment-context-source_text')).toBeChecked()
    expect(screen.queryByTestId('segment-context-target_text')).not.toBeInTheDocument()
  })
})

// A prompt serves every language pair its engine routes; an addition is the
// part that only holds for some of them.
describe('PromptForm — language additions', () => {
  // Behind a disclosure: most prompts carry none.
  const openLanguages = async () => userEvent.click(screen.getByTestId('ai-prompt-languages-toggle'))

  it('is closed until asked for, and says how many are set', async () => {
    renderForm(prompt({
      is_default: false,
      language_additions: [{ target_languages: ['de'], text: 'Use formal address.' }],
    }))

    expect(screen.queryByTestId('prompt-language-addition')).not.toBeInTheDocument()
    // LanguageCode uppercases in CSS, so the DOM text stays lowercase.
    expect(screen.getByTestId('ai-prompt-languages-toggle')).toHaveTextContent('1 · de')
  })

  it('loads the stored additions into the editor', async () => {
    renderForm(prompt({
      is_default: false,
      language_additions: [
        { target_languages: ['de'], text: 'Address the reader formally.' },
        { target_languages: ['fr'], text: 'Use the vouvoiement.' },
      ],
    }))
    await openLanguages()
    // Every rule is on screen at once — no tabs, nothing to click through to
    // see what a prompt already carries.
    expect(screen.getAllByTestId('prompt-language-addition')).toHaveLength(2)
    expect(screen.queryByRole('tab')).not.toBeInTheDocument()
    expect(screen.getByTestId('prompt-language-addition-0-text')).toHaveValue('Address the reader formally.')
    expect(screen.getByTestId('prompt-language-addition-1-text')).toHaveValue('Use the vouvoiement.')

    // Editing one leaves the other alone.
    await userEvent.type(screen.getByTestId('prompt-language-addition-0-text'), ' Please.')
    expect(screen.getByTestId('prompt-language-addition-0-text')).toHaveValue('Address the reader formally. Please.')
    expect(screen.getByTestId('prompt-language-addition-1-text')).toHaveValue('Use the vouvoiement.')

    await userEvent.click(screen.getByTestId('prompt-language-addition-add'))
    expect(screen.getByTestId('prompt-language-addition-2-text')).toHaveValue('')

    // Removing the first shifts the rest up rather than stranding a selection.
    await userEvent.click(screen.getAllByRole('button', { name: 'Remove addition' })[0])
    expect(screen.getAllByTestId('prompt-language-addition')).toHaveLength(2)
    expect(screen.getByTestId('prompt-language-addition-0-text')).toHaveValue('Use the vouvoiement.')
  })

  it('adds and removes rows', async () => {
    renderForm(prompt({ is_default: false }))
    await openLanguages()
    expect(screen.queryAllByTestId('prompt-language-addition')).toHaveLength(0)

    await userEvent.click(screen.getByTestId('prompt-language-addition-add'))
    expect(screen.getAllByTestId('prompt-language-addition')).toHaveLength(1)

    await userEvent.click(screen.getByRole('button', { name: 'Remove addition' }))
    expect(screen.queryAllByTestId('prompt-language-addition')).toHaveLength(0)
  })

  // An unfinished row is dropped on submit, so a stack of them is work the form
  // throws away without saying so. The button closes until the row is usable.
  it('will not add a second prompt while one is unfinished', async () => {
    renderForm(prompt({ is_default: false }))
    await openLanguages()
    const add = screen.getByTestId('prompt-language-addition-add')
    expect(add).toBeEnabled()

    await userEvent.click(add)
    expect(screen.getAllByTestId('prompt-language-addition')).toHaveLength(1)
    expect(add).toBeDisabled()

    // Text alone does not finish it: a row naming no language would apply to
    // every job, which is what the prompt's own text is for.
    await userEvent.type(screen.getByTestId('prompt-language-addition-0-text'), 'Be formal.')
    expect(add).toBeDisabled()
  })

  // The gate reads the live values, not useFieldArray's `fields` snapshot —
  // which does not change as the author types, and would leave the button open.
  it('closes again when a finished prompt is emptied', async () => {
    renderForm(prompt({
      is_default: false,
      language_additions: [{ target_languages: ['de'], text: 'Use formal address.' }],
    }))
    await openLanguages()
    const add = screen.getByTestId('prompt-language-addition-add')
    expect(add).toBeEnabled()

    await userEvent.clear(screen.getByTestId('prompt-language-addition-0-text'))
    expect(add).toBeDisabled()
  })

  it('offers no editing controls on a read-only built-in', async () => {
    renderForm(prompt({
      is_default: true,
      language_additions: [{ target_languages: ['de'], text: 'Address the reader formally.' }],
    }))
    await openLanguages()
    // Visible, so the author can read what the prompt sends…
    expect(screen.getByTestId('prompt-language-addition-0-text')).toHaveValue('Address the reader formally.')
    // …but not editable, like the rest of a built-in.
    expect(screen.getByTestId('prompt-language-addition-0-text')).toBeDisabled()
    expect(screen.queryByTestId('prompt-language-addition-add')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Remove addition' })).not.toBeInTheDocument()
  })
})

describe('PromptForm — guided tour', () => {
  afterEach(endReveal)

  it('opens the collapsed sections a tour step points at', () => {
    renderForm(prompt({ is_default: false }))
    expect(document.getElementById('ai-prompt-output_format')).toBeNull()
    expect(document.getElementById('ai-prompt-context')).toBeNull()
    expect(document.getElementById('ai-prompt-languages')).toBeNull()

    act(() => revealAnchor('ai_prompt.languages'))
    expect(document.getElementById('ai-prompt-languages')).not.toBeNull()

    act(() => revealAnchor('ai_prompt.context'))
    expect(document.getElementById('ai-prompt-context')).not.toBeNull()

    act(() => revealAnchor('ai_prompt.schema'))
    expect(document.getElementById('ai-prompt-output_format')).not.toBeNull()
  })

  it('ignores a step for another prompt type', () => {
    renderForm(prompt({ is_default: false }))
    act(() => revealAnchor('mt_prompt.schema'))
    expect(document.getElementById('ai-prompt-output_format')).toBeNull()
  })
})
