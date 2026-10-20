# Regression Checklist

Run through these when doing a QA pass. Items marked "No E2E" are the highest priority — they can silently regress.

## UI / Frontend

| # | Bug | How to verify | E2E? |
|---|-----|---------------|------|
| 1 | **Popover scroll jump** — opening any LangCombobox (or popover with autoFocus) scrolled the page | Open /projects/new, scroll down, click Source language dropdown — page should not jump | `project-crud: language combobox does not cause page scroll jump` |
| 2 | **CAT editor blank page (localStorage blocked)** — `localStorage.getItem()` threw SecurityError crashing CATEditor | Open a job in Chrome with storage blocked (or private mode with strict settings) — should show editor, not blank page | `cat-editor: no console errors on page load` |
| 3 | **CAT editor blank page (nested button)** — `<TooltipTrigger><Button>` created `<button><button>` causing React crash | Open any job, click a segment — toolbar buttons should render without crash | `cat-editor: CAT editor toolbar has all buttons` |
| 4 | **Stale Vite on :5173** — old Vite process squatting on port, new one starts on :5174 silently | Run `make watch`, verify Vite logs show `:5173` not another port | No E2E — `make watch` kills orphans |
| 5 | **Template section names mismatch** — E2E looked for "MT Engine Assignments" but UI says "Machine translation" | Open /templates/1 — verify section says "Machine translation" not "MT Engine Assignments" | `templates: template detail shows TM and MT assignments` |
| 6 | **Create project fails** — "failed to create settings profile" when QaProfileID not passed | Click New project, fill name + source + target, click Create — should succeed | `project-crud: can create and delete a project` |

## CAT Editor

| # | Bug | How to verify | E2E? |
|---|-----|---------------|------|
| 7 | **EditorSidebar null crash** — TM matches API returned null instead of empty array, `.length` crashed | Open a job, click a segment that has NO TM matches — sidebar should show "No matches" not crash | No E2E — need to test with segment that has no matches |
| 8 | **Hidden chars toggle persisted across sessions** — localStorage state from previous session caused confusion | Open job, toggle ¶ on, close tab, reopen — toggle state should persist correctly | `cat-editor: hidden characters toggle` |
| 9 | **Term panel applied term not greyed** — terms already in translation weren't visually distinguished | Open a job with glossary terms, confirm a segment with a term — term should show greyed with checkmark | `glossary: already-applied term shows checkmark` |
| 10 | **Long segments overlap in CAT editor** — MQXLIFF segments (200+ chars) overflowed between rows | Upload a long-segment file, check inactive rows — text truncated to 2 lines, equal padding top/bottom, no overlap | No E2E — visual check |
| 11 | **line-clamp clips bottom padding** — `line-clamp` on a padded div eats the bottom padding | Check any truncated row — spacing below text should match spacing above | No E2E — visual check. Convention: always put `line-clamp` on inner div, padding on outer |
| 12 | **Scroll jumps back to active row** — `measure()` firing on every render caused scroll to snap back | Select row 90, scroll to top, scroll down slowly — should NOT jump back to row 90 | No E2E — manual scroll test |
| 13 | **Bottom-of-screen click causes scroll jump** — `scrollToIndex` fired even when clicked row was visible | Click a row at bottom of screen — row should expand in place, not jump to top | No E2E — manual click test |
| 14 | **Gap between rows deselects active segment** — click-outside handler fired on virtual scroll gaps | Click between two rows in the segment list — should NOT deselect the active segment | No E2E — manual click test |
| 31 | **Double vertical scroll on TM/Glossary browser** — page scroll + table scroll both visible | Open a TM with many entries — only the table should scroll, not the whole page | No E2E — visual check |
| 32 | **Table columns misaligned with header** — separate header/body tables had scrollbar width mismatch | Open TM browser — header columns should align perfectly with data columns | `tm: TM browser shows entries` |

## Pipeline / Backend

| # | Bug | How to verify | E2E? |
|---|-----|---------------|------|
| 15 | **TMX UTF-16 import fails** — UTF-16 encoded TMX files caused XML parse error | Upload a UTF-16 LE/BE TMX file — should import without errors | Go test: `TestParseUTF16LE`, `TestParseUTF16BE` |
| 16 | **Pre-translate not running on upload** — jobs show "untranslated" after upload despite pre-translate checkbox | Create project with pre-translate checked + TM/MT configured, upload file — jobs should show pre_translated status | `pipeline: pre-translate on upload` |
| 17 | **Prod migrations silently failed** — goose recorded migrations as applied but DDL didn't execute | After deploy: `goose status` should match local migration count, verify key tables exist | No E2E — need startup health check |
| 27 | **TM import drops valid translations** — old unique constraint on source_text overwrote different targets for same source | Import TMX with same source/different targets → both should appear in TM browser | Go test: `TestTMMultipleTranslations` |
| 28 | **TM import flaky entry count** — batch ON CONFLICT error caused fallback to lose entries silently | Import large TMX twice → entry count should be identical both times | No E2E — check pod logs for `event.action` `tm-import-completed`, `tm-import-count-mismatch`, `tm-import-batch-failed` and `tm-import-entry-skipped` |
| 29 | **TMX metadata lost on round-trip** — creationdate, creator, tuid discarded during import | Import TMX → export TMX → check `<tu>` has creationdate/creationid attributes | Go test: `TestTMXMetadataRoundTrip` |
| 30 | **Settings scalar fields resetting each other** — toggling QA profile unchecked pre-translate | In New project, check pre-translate, select QA profile → pre-translate should stay checked | No E2E — manual check |

## Forms / Validation

| # | Bug | How to verify | E2E? |
|---|-----|---------------|------|
| 18 | **No inline validation errors** — Create button disabled with no hint which field is missing | Open /projects/new, leave name empty, try to create — should show field-level error messages | No E2E yet |
| 19 | **Glossary CRUD language search** — typing "english" in LangCombobox found nothing (searches by code not name) | Open glossary create, type "en" in language field — should find English | `glossary: glossary CRUD` |
| 20 | **MT Engine conditional API key** — DeepL/Google require API key but no validation error shown | Go to MT engines, Add, select "deepl", leave API key empty, submit — should show "API key is required" | `engines: engine CRUD` |

## Editor Behavior

| # | Bug | How to verify | E2E? |
|---|-----|---------------|------|
| 21 | **Cmd+Enter doesn't confirm unedited segments** — status stayed "pre_translated" unless text was edited | Click a pre-translated segment, Cmd+Enter without editing — status should change to "approved" | No E2E — manual check |
| 33 | **Concordance didn't use selected text** — Ctrl+K always pre-filled with full target text, not selection | Select a word in source or target, press Ctrl+K — modal should show selected word as query | No E2E — manual check |
| 34 | **Concordance didn't search terminology** — only searched TM, not term bases | Ctrl+K and search for a glossary term — should appear in "Terminology" tab | No E2E — manual check |
| 35 | **Concordance inserted into locked segment** — clicking result modified locked text, froze editor | Open concordance on locked segment — results should show 🔒, insert disabled | No E2E — manual check |
| 36 | **Concordance appended instead of replacing** — clicking result added text at end of target | Click a concordance result — should replace entire target, not append | No E2E — manual check |
| 37 | **No way to re-lock a segment** — after unlocking, lock button disappeared | Click a segment — should always show 🔒 Unlock / 🔓 Lock toggle inline with B/I buttons | No E2E — visual check |
| 22 | **Lock icon inconsistent after Cmd+Enter** — lock sometimes appeared, sometimes not | Cmd+Enter through 5 segments — all should show lock icon in Outcome column | No E2E — manual check |
| 23 | **Formatting toolbar misaligns source and target** — toolbar pushed editor down, source text was higher | Click a segment — source text and editor textarea should start at same vertical position | No E2E — visual check |
| 24 | **Whitespace markers disappear when editing** — `WhitespaceText` showed dots in source but TipTap editor had none | Toggle ¶ on, click a segment — red dots should appear on spaces in both source AND editor | `cat-editor: hidden characters toggle` |
| 25 | **Whitespace markers broke column layout** — replacing spaces with `·` removed word-wrap opportunities, text overflowed columns | Toggle ¶ on, resize window narrower — columns should stay aligned, text should wrap normally | No E2E — manual resize test |
| 26 | **Newlines collapsed in TipTap editor** — `\n` in segment text became one long line when editing | Click a segment with newlines — editor should show multiple lines, not one long line | `cat-editor: multiline` |
| 27 | **Sidebar flicker on segment switch** — TM matches/terms flash empty then reload | Click between segments 15→16→17 — sidebar content should not flash/flicker | No E2E — manual check |
| 28 | **Comment textarea deselects segment** — clicking comment input triggered click-outside handler | Click comment textarea in sidebar — segment should stay active | No E2E — manual check |
| 29 | **QA false positive on quotes** — `'` at end of quoted text flagged as "unexpected trailing punctuation" | Run QA on segment with quoted text ending in `'` — no punctuation warning | Go test: `TestCheckPunctuation` |
| 30 | **Section labels say "Assignments"** — should be just "Translation memory", "Terminology" etc. | Check Settings tab — section headings should NOT say "Assignments" | `project-crud: settings sections` |
| 31 | **Read/Write mode labels** — should say "Search/Update" not "Read/Write" | Check TM/Terminology assignment dropdowns — labels should be Search, Update, Search/Update | No E2E — visual check |

## Deployment

| # | Check | How to verify |
|---|-------|---------------|
| D1 | **Run migrations AFTER deploy** — old binary doesn't know new columns | `goose status` should match local count. Run `goose up` only after new image is deployed |
| D2 | **Verify goose didn't silently fail** — check key tables exist | `\dt settings_profiles` on prod DB should return a table, not "Did not find any relation" |
| D3 | **Single Vite process** — no orphan dev servers | `lsof -i :5173` should show exactly one process |

## How to Run

```bash
# Full E2E suite (covers most items above)
cd e2e && npx playwright test

# Targeted checks
npx playwright test tests/project-crud.spec.ts    # Items 1, 6, 18
npx playwright test tests/cat-editor.spec.ts      # Items 2, 3, 8, 10-14
npx playwright test tests/workflow.spec.ts         # Item 16
npx playwright test tests/engines.spec.ts          # Item 20
npx playwright test tests/glossary.spec.ts         # Items 9, 19
```
