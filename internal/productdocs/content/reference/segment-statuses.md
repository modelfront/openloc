---
title: "Segment statuses and origins"
summary: "What each status badge and origin badge means, and how a segment moves between them."
order: 40
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-19
sources:
  - internal/translation/**
  - web/src/components/QASummaryDot.tsx
  - web/src/components/SegmentHoldIndicators.tsx
  - web/src/components/SegmentRow.shared.tsx
  - web/src/components/SegmentRow.tsx
  - web/src/components/SegmentSelectionBar.tsx
  - web/src/components/SegmentFilterBar.tsx
  - web/src/lib/qa-findings.ts
  - web/src/lib/segment-bulk.ts
  - web/src/lib/segment-filters.ts
  - web/src/lib/segment-status.ts
related:
  - guides/cat-editor
  - reference/keyboard-shortcuts
  - concepts/how-a-translation-flows
  - concepts/quality-estimation
screenshots:
  - segment-badges.png
---
Every segment in the CAT editor has a **status** (how far along it is) and, when the text did not come from a person, an **origin** (where it came from). The **Status** column shows the status badge and the **Match** column the origin chip. The last column, which has no header, shows the lock icon, the AI score, the QA dot and the comment count. The same names appear as filter pills above the grid.

![How a segment moves between the five statuses. Editing always drops a segment back to Draft, and there are two ways into Approved: you confirm it, or an exact memory match is confirmed for you.](assets/segment-status-flow.svg)

## Statuses

| Badge | Color | Meaning | How a segment gets here | How it leaves |
|---|---|---|---|---|
| Untranslated | no badge | No translation yet. | New file without pre-translation, or **Clear** was used. | Typing, pre-translation, or inserting a TM or MT hit. |
| Pretranslated | no badge | Filled by TM, MT or a bilingual file import. Not edited, not yet judged by a quality check. The **Match** chip says which. | Pre-translation on upload, **Apply TM** or **Apply MT**. | Edit makes it **Draft**; Ctrl+Enter makes it **Approved**; a quality check makes it **Auto-approved** or **Draft**. |
| Draft | red | Work in progress: edited by a person without confirming, or rejected by the automated quality check. | Typing, a spell-check correction, a find and replace that changes it, a rejected quality check, inserting a TM match or MT suggestion from the **Resources** panel, or editing an approved segment. | Ctrl+Enter or **Confirm** in the selection bar makes it **Approved**. |
| Auto-approved | green | An AI step accepted the machine translation (or its automatic post-edit). Counts as done for completing a step. | A QE+APE workflow step. | Edit makes it **Draft**; Ctrl+Enter makes it **Approved**. The next workflow step starts it unconfirmed, with a grey **Auto-approved** chip in its place. |
| Approved | blue | Confirmed as final. Written to the translation memory and used in the delivered file. A row marked **Confirmed by assistant** is not written to the memory until a person confirms it ([why](/docs/guides/ai-assistant#segments-the-assistant-confirms)). | Ctrl+Enter (Cmd+Enter on Mac), **Confirm** in the selection bar, a confirm sent through the API (it counts as yours), [locking](/docs/guides/cat-editor#lock-and-unlock-segments) the segment, or the AI assistant on your request — **or** a 100% or 101% memory match, which pre-translation confirms for you, because a person already approved that exact sentence. No editor is recorded for those; the segment's history shows a TM event. | Edit makes it **Draft** again. |

Each status filter pill above the grid shows in brackets how many segments have that status, for example **Pretranslated (28)**. The progress bar above the pills shows the share of **Approved** and **Auto-approved** segments.

## Origins and other marks

![Eight rows of the grid: the Match column shows 100%, a 101% context match and an amber 92% fuzzy match, the Status column shows Approved and Draft badges (and nothing at all for a Pretranslated row), and the last column shows QA dots.](assets/segment-badges.png)

A row shows at most one origin chip. When several apply, the order is: repetition, TM, APE, MT, File.

| Chip | Meaning |
|---|---|
| →R | Repetition source. The same source text appears later in the file. Confirming this row copies your translation to those rows. |
| ⇢R | Repetition. The same source text appears earlier in the file. |
| 100% or 101% (green) | Exact TM match. 101% means the neighbouring segments also match (a context match). |
| below 100% (amber) | Fuzzy TM match. The number is the similarity to the TM entry after penalties. |
| MT | Machine translation from the engine the project's MT router picked for the language. |
| APE | Machine translation that a QE+APE step post-edited automatically. |
| File | Translation imported from a bilingual file. |
| QE 87 (blue) | The score an AI step gave the translation. Hover for the verdict. When a person changes the text, the chip goes away: the score is about text that is gone. |
| 🔒 | The segment is locked. See [The CAT editor](/docs/guides/cat-editor). |
| red, yellow or blue dot | The segment has quality check findings. One dot per segment, in the colour of the most severe open finding: error, warning, then information. Hover to read every finding on the segment. To ignore one, use the sidebar's **QA findings** section — see [Ignore a false positive](/docs/guides/quality-assurance#ignore-a-false-positive). |
| hollow grey ring | Every finding on the segment is ignored. An ignored finding does not block confirming. |
| speech bubble with a number | The segment has comments. Open it to read them. |
| person with a pen (amber), beside the segment number | Someone else has the segment open. Hover to read their name. You can read the segment, its matches and its comments, but not change it. |

The origin filter pills are **TM**, **MT**, **APE** and **File import**. A segment typed by a person from scratch has no origin chip.

A TM match or MT suggestion inserted from the **Resources** panel (a click, or Ctrl+1 to Ctrl+5) is saved as a **Draft** at once and shows the same chip as after pre-translation: the match's percentage, or **MT**. Later edits keep the chip. A concordance insert is an ordinary edit and changes no chip.

## Rules to remember

- **Editing downgrades.** Any change to an **Approved** or **Auto-approved** segment makes it **Draft**. The red badge is your signal to confirm again.
- **Only a confirm confirms.** Ctrl+Enter confirms one segment; **Confirm** in the selection bar confirms the selected ones the same way, including the write to the translation memory. Drafts are saved automatically, but saving is not confirming. Moving to another row, clicking away, pressing Escape or running a find and replace never raises a status.
- **Bulk Confirm skips what a person should look at.** It leaves locked and empty segments, segments that are already **Approved**, **Auto-approved** ones (confirm those one by one, so machine-approved text never reaches the memory in bulk), segments with an open quality check error, segments someone else has open, and segments edited by someone else while it ran.
- **A locked segment is final.** Locking approves the segment (an **Auto-approved** one, or one auto-approved in an earlier step that nobody has edited, is **Auto-approved**), so every locked segment is **Approved** or **Auto-approved**. Nothing changes it while it is locked — pre-translation, bulk **Confirm** or copying to repetitions — except find and replace with **Include locked** or spell check's option to include locked segments, which only owners and admins can use: they save a **Draft** and unlock the segment, since a draft is never locked. A locked segment keeps its status from one workflow step to the next. Unlocking keeps the status until someone edits the segment.
- **Confirming promotes and propagates.** Ctrl+Enter runs QA on the text you are confirming and refuses if an open **Error** remains ([why](/docs/guides/quality-assurance#why-confirm-can-be-refused)). Otherwise it sets **Approved** and copies your translation to every unlocked segment with the same source text in the same file and target language. Those rows become **Approved** too, and the copy replaces whatever was there, including rows that were already approved. They lose the QA findings of their old text, ignored ones included; the next quality check looks at them again. A locked repetition keeps its text, status and findings, and so does one someone else has open. A message tells you how many rows were updated, and how many repetitions were left out because someone else is editing them.
- **A segment someone else has open is read-only for you**, and bulk actions skip it ([how it works](/docs/guides/cat-editor#when-someone-else-is-editing-a-segment)).
- **Corrections are drafts.** A spell-check **Change** or **Change all**, and a find and replace, save a draft, so an approved segment becomes **Draft**. Find and replace keeps the segment's inline tags.
- **Done means Approved or Auto-approved.** When you click **Complete step**, every other status counts as left to do: the dialog counts drafts and untranslated segments, and **Approve all and complete** confirms the drafts first. A segment auto-approved in an earlier step that nobody has edited becomes **Auto-approved** again, not **Approved**. It does not block you.
- **The translation memory learns from confirmed work.** A translation goes to the TM when you press Ctrl+Enter or confirm it through the API, and every **Approved** segment is saved again when a workflow step or the job is completed — except one marked **Confirmed by assistant**, which waits for a person's Ctrl+Enter. **Auto-approved** segments are never written ([why](/docs/concepts/quality-estimation#why-auto-approved-text-never-enters-the-translation-memory)). An exact match that pre-translation confirmed for you *is* written — it came out of a memory in the first place.
- **Clear resets everything.** **Clear** (owners and admins, first workflow step only) makes the segment **Untranslated**, whatever its status; **Clear translation** in the selection bar (managers, first step) does the same for the selected segments that have a translation and are not locked. Either way it removes the origin, match percentage, AI score, QA findings and any comment written by **AI** — those described the text that just went away. Comments you wrote stay, and TM entries are not affected. On a segment with nothing saved, **Clear** only empties the editor: no dialog, and the status does not change.

See also [How a translation flows](/docs/concepts/how-a-translation-flows) and [Quality estimation](/docs/concepts/quality-estimation).
