import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import ConflictBanner, { getBannerCopy } from './ConflictBanner'

afterEach(() => cleanup())

describe('getBannerCopy', () => {
  // Pins the heading for each editor identity: "Saved by X while you were
  // editing" must not appear when X is the current user.

  it('same user (different tab/session) — heading reframes', () => {
    const copy = getBannerCopy({ editedByUserId: 7, editedByName: 'Olivia', currentUserId: 7 })
    expect(copy.title).toBe('Saved from another tab or session')
    expect(copy.subtitle).toMatch(/another tab|somewhere else/i)
  })

  it('different user with name — names them', () => {
    const copy = getBannerCopy({ editedByUserId: 7, editedByName: 'Alice Smith', currentUserId: 99 })
    expect(copy.title).toBe('Alice Smith saved a different version')
  })

  it('different user, name is blank — falls back to neutral wording', () => {
    const copy = getBannerCopy({ editedByUserId: 7, editedByName: '', currentUserId: 99 })
    expect(copy.title).toBe('Server has a different version')
  })

  it('different user, name is whitespace — also neutral wording', () => {
    const copy = getBannerCopy({ editedByUserId: 7, editedByName: '   ', currentUserId: 99 })
    expect(copy.title).toBe('Server has a different version')
  })

  it('editedByUserId null on server row — neutral wording even with same currentUserId', () => {
    // Edge case: older rows or some pipeline-side write may leave
    // edited_by NULL. Treat as unknown editor, NOT as same user. Same shape
    // applies for TM rows where `updated_by` doesn't JOIN to a user record
    // (e.g. "tmx-import" label) and `updated_by_user_id` resolves to null.
    const copy = getBannerCopy({ editedByUserId: null, editedByName: '', currentUserId: 7 })
    expect(copy.title).toBe('Server has a different version')
  })

  it('currentUserId null (auth/me still loading) — uses name if available', () => {
    const copy = getBannerCopy({ editedByUserId: 7, editedByName: 'Alice Smith', currentUserId: null })
    expect(copy.title).toBe('Alice Smith saved a different version')
  })

  it('currentUserId undefined — uses name if available', () => {
    const copy = getBannerCopy({ editedByUserId: 7, editedByName: 'Alice Smith' })
    expect(copy.title).toBe('Alice Smith saved a different version')
  })
})

describe('ConflictBanner', () => {
  function setup(props: Partial<React.ComponentProps<typeof ConflictBanner>> = {}) {
    const onUseTheirs = vi.fn()
    const onUseMine = vi.fn()
    render(
      <ConflictBanner
        theirText="their saved version"
        myEditText="my live edit"
        editedByUserId={7}
        editedByName="Alice Smith"
        currentUserId={99}
        onUseTheirs={onUseTheirs}
        onUseMine={onUseMine}
        {...props}
      />,
    )
    return { onUseTheirs, onUseMine }
  }

  it('renders both versions side-by-side and both action buttons', () => {
    setup()
    const theirs = screen.getByTestId('conflict-banner-theirs')
    const mine = screen.getByTestId('conflict-banner-mine')
    expect(within(theirs).getByText(/their saved version/)).toBeInTheDocument()
    expect(within(mine).getByText(/my live edit/)).toBeInTheDocument()
    expect(screen.getByTestId('conflict-banner-use-theirs')).toBeInTheDocument()
    expect(screen.getByTestId('conflict-banner-use-mine')).toBeInTheDocument()
  })

  it('"Use theirs" fires onUseTheirs but not onUseMine', async () => {
    const { onUseTheirs, onUseMine } = setup()
    const user = userEvent.setup()
    await user.click(screen.getByTestId('conflict-banner-use-theirs'))
    expect(onUseTheirs).toHaveBeenCalledTimes(1)
    expect(onUseMine).not.toHaveBeenCalled()
  })

  it('"Use mine" fires onUseMine but not onUseTheirs', async () => {
    const { onUseTheirs, onUseMine } = setup()
    const user = userEvent.setup()
    await user.click(screen.getByTestId('conflict-banner-use-mine'))
    expect(onUseMine).toHaveBeenCalledTimes(1)
    expect(onUseTheirs).not.toHaveBeenCalled()
  })

  it('field label defaults to TRANSLATION (CAT case)', () => {
    setup()
    const theirs = screen.getByTestId('conflict-banner-theirs')
    expect(within(theirs).getByText(/their translation/i)).toBeInTheDocument()
  })

  it('field label can be overridden (TM case: SOURCE / TARGET)', () => {
    setup({ fieldLabel: 'TARGET' })
    const theirs = screen.getByTestId('conflict-banner-theirs')
    expect(within(theirs).getByText(/their target/i)).toBeInTheDocument()
  })

  // Pins that both columns render tag chips when rich text is present.
  describe('rich-text symmetry between Their version and My edit', () => {
    it('renders Their version with chips when theirRichText has XLIFF tags', () => {
      setup({
        theirText: 'their bold version',
        theirRichText: 'their <bpt id="1"><b></bpt>bold<ept id="1"></b></ept> version',
      })
      const theirs = screen.getByTestId('conflict-banner-theirs')
      // Tag chips render as <span class="xliff-tag">. Pin the structural
      // presence (not the exact display text — that's tested elsewhere).
      expect(theirs.querySelectorAll('.xliff-tag').length).toBeGreaterThan(0)
    })

    it('renders My edit with chips when myEditRichText has XLIFF tags', () => {
      setup({
        myEditText: 'my bold edit',
        myEditRichText: 'my <bpt id="1"><b></bpt>bold<ept id="1"></b></ept> edit',
      })
      const mine = screen.getByTestId('conflict-banner-mine')
      expect(mine.querySelectorAll('.xliff-tag').length).toBeGreaterThan(0)
    })

    it('My edit falls back to plain text when myEditRichText has no XLIFF tags', () => {
      setup({
        myEditText: 'just plain text',
        myEditRichText: 'just plain text', // no tags
      })
      const mine = screen.getByTestId('conflict-banner-mine')
      expect(mine.querySelectorAll('.xliff-tag').length).toBe(0)
      expect(within(mine).getByText('just plain text')).toBeInTheDocument()
    })

    it('My edit falls back to plain text when myEditRichText is omitted', () => {
      setup({ myEditText: 'no rich text supplied' })
      const mine = screen.getByTestId('conflict-banner-mine')
      expect(mine.querySelectorAll('.xliff-tag').length).toBe(0)
      expect(within(mine).getByText('no rich text supplied')).toBeInTheDocument()
    })
  })

  // Pins the user-identity branches at the rendered banner level (getBannerCopy is unit-tested above; this verifies the
  // wiring through the component).
  describe('heading reflects user identity', () => {
    it('same user — uses "another tab or session" heading', () => {
      setup({ editedByUserId: 7, editedByName: 'Olivia', currentUserId: 7 })
      expect(screen.getByTestId('conflict-banner-title')).toHaveTextContent('Saved from another tab or session')
    })

    it('different user — names them in heading', () => {
      setup({ editedByUserId: 7, editedByName: 'Alice', currentUserId: 99 })
      expect(screen.getByTestId('conflict-banner-title')).toHaveTextContent('Alice saved a different version')
    })

    it('unresolved editor (TM-side: label did not JOIN to users) — neutral wording', () => {
      // TM rows can have updated_by="tmx-import" which won't match any
      // users.email, so updated_by_user_id resolves to null. Same shape
      // exercises the third branch as a CAT row with edited_by=null.
      setup({ editedByUserId: null, editedByName: '', currentUserId: 99 })
      expect(screen.getByTestId('conflict-banner-title')).toHaveTextContent('Server has a different version')
    })
  })
})
