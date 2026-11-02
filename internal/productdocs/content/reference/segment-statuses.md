---
title: "Segment statuses and origins"
summary: "What each status icon and origin chip means, and how a segment moves between them."
order: 40
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-29
sources:
  - internal/translation/**
  - web/src/components/QASummaryDot.tsx
  - web/src/components/SegmentHoldIndicators.tsx
  - web/src/components/SegmentRow.shared.tsx
  - web/src/components/SegmentRow.tsx
  - web/src/components/SegmentStatusIcon.tsx
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
Every segment in the CAT editor has a **status** (how far along it is) and, when the text did not come from a person, an **origin** (where it came from). The status column, right after **Match** and without a header, shows the status as an icon, and the **Match** column the origin chip. Hover an icon to read its name. The last column, which has no header, shows the padlock, the AI score, the QA dot and the comment count. The same names appear as filter pills above the grid, which are coloured chips: **Draft** red, **Approved** blue, **Auto-approved** green. The History section uses the same chips.

![How a segment moves between the five statuses. Editing always drops a segment back to Draft, and there are two ways into Approved: you confirm it, or an exact memory match is confirmed for you.](assets/segment-status-flow.svg)

## Statuses

| Status | Icon in the status column | Meaning | How a segment gets here | How it leaves |
|---|---|---|---|---|
| Untranslated | none | No translation yet. | New file without pre-translation, **Clear** was used, or it is the second piece of a [split](/docs/guides/cat-editor#split-and-join-segments). | Typing, pre-translation, or inserting a TM or MT hit. |
| Pretranslated | none | Filled by TM, MT or a bilingual file import. Not edited, not yet judged by a quality check. The **Match** chip says which. | Pre-translation on upload, **Apply TM** or **Apply MT**. | Edit makes it **Draft**; Ctrl+Enter makes it **Approved**; a quality check makes it **Auto-approved** or **Draft**. |
| Draft | yellow pencil | Work in progress: edited by a person without confirming, or rejected by the automated quality check. | Typing, a spell-check correction, a find and replace that changes it, a rejected quality check, inserting a fuzzy TM match or an MT suggestion from the **Resources** panel, editing an approved segment, the first piece of a split, or a joined row. | Ctrl+Enter or **Confirm** in the selection bar makes it **Approved**. |
| Auto-approved | green check with two sparkles | An AI step accepted the machine translation (or its automatic post-edit). Counts as done for completing a step. | A QE+APE workflow step. | Edit makes it **Draft**; Ctrl+Enter makes it **Approved**. The next workflow step starts it unconfirmed, with a grey check with sparkles (hover: "Auto-approved in the previous step.") until someone edits or confirms it there. |
| Approved | green check in an open circle | Confirmed as final. Written to the translation memory and used in the delivered file. A row the assistant confirmed shows the green check with two sparkles, named **Confirmed by assistant**; it is not written to the memory until a person confirms it ([why](/docs/guides/ai-assistant#segments-the-assistant-confirms)). | Ctrl+Enter (Cmd+Enter on Mac), **Confirm** in the selection bar, a confirm sent through the API (it counts as yours), [locking](/docs/guides/cat-editor#lock-and-unlock-segments) the segment, or the AI assistant on your request, choosing an exact match in the **Resources** panel — **or** a 100% or 101% memory match, which pre-translation confirms for you, because a person already approved that exact sentence. No editor is recorded for those pre-translated rows; the segment's history shows a TM event. | Edit makes it **Draft** again. |

Each status filter pill above the grid shows in brackets how many segments have that status, for example **Pretranslated (28)**. The progress bar above the pills shows the share of **Approved** and **Auto-approved** segments.

## Origins and other marks

![Eight rows of the grid: the Match column shows 100%, a 101% context match and an amber 92% fuzzy match, the status column shows a green check for Approved and a yellow pencil for Draft (and nothing at all for a Pretranslated row), and the last column shows QA dots.](assets/segment-badges.png)


A row shows at most one origin chip. When several apply, the order is: repetition source, repetition, TM, APE, MT, File. The one exception is a repetition source whose translation came from the TM: it keeps its percentage and adds an arrow.

| Chip | Meaning |
|---|---|
| →R | Repetition source whose translation did not come from the TM. The same source text appears later in the file. Confirming this row copies your translation to those rows. |
| 98%→ (amber below 100%, green from 100%) | Repetition source whose translation came from the TM: the match percentage, then an arrow. Confirming the row copies your translation to the later rows. Hover to read both. |
| ⇢R | Repetition. The same source text appears earlier in the file. It never shows a TM percentage. |
| 100% or 101% (green) | Exact TM match. 101% means the neighbouring segments also match (a context match). |
| below 100% (amber) | Fuzzy TM match. The number is the similarity to the TM entry after penalties. |
| MT | Machine translation from the engine the project's MT router picked for the language. |
| APE | Machine translation that a QE+APE step post-edited automatically. |
| File | Translation imported from a bilingual file. |
| QE 87 (blue) | The score an AI step gave the translation. Hover for the verdict. When a person changes the text, the chip goes away: the score is about text that is gone. |
| grey padlock | The segment is locked. See [The CAT editor](/docs/guides/cat-editor). |
| red, yellow or blue dot | The segment has quality check findings. One dot per segment, in the colour of the most severe open finding: error, warning, then information. Hover to read every finding on the segment. To ignore one, use the sidebar's **QA findings** section — see [Ignore a false positive](/docs/guides/quality-assurance#ignore-a-false-positive). |
| hollow grey ring | Every finding on the segment is ignored. An ignored finding does not block confirming. |
| speech bubble with a number | The segment has comments. Open it to read them. |
| person with a pen (amber), beside the segment number | Someone else has the segment open. Hover to read their name. You can read the segment, its matches and its comments, but not change it. |

The origin filter pills are **TM**, **MT**, **APE** and **File import**. A segment typed by a person from scratch has no origin chip.

A TM match or MT suggestion inserted from the **Resources** panel (a click, or Ctrl+1 to Ctrl+5) is saved as a **Draft** at once and shows the same chip as after pre-translation: the match's percentage, or **MT**. An exact TM match (the same source text, with no penalty) is then **Approved**, as after pre-translation. Later edits keep the chip. A concordance insert is an ordinary edit and changes no chip.

## Rules to remember

- **Editing downgrades.** Any change to an **Approved** or **Auto-approved** segment makes it **Draft**. The yellow pencil is your signal to confirm again.
- **Only a confirm confirms.** Ctrl+Enter confirms one segment; **Confirm** in the selection bar confirms the selected ones the same way, including the write to the translation memory. Drafts are saved automatically, but saving is not confirming. Moving to another row, clicking away, pressing Escape or running a find and replace never raises a status.
- **Bulk Confirm skips what a person should look at.** It leaves locked and empty segments, segments that are already **Approved**, **Auto-approved** ones (confirm those one by one, so machine-approved text never reaches the memory in bulk), segments with an open quality check error, segments with a tag the source does not have in a JSON, PO, TXT or XLSX file ([why](/docs/reference/file-formats)), segments someone else has open, and segments edited by someone else while it ran.
- **A locked segment is final.** Locking approves the segment (an **Auto-approved** one, or one auto-approved in an earlier step that nobody has edited, is **Auto-approved**), so every locked segment is **Approved** or **Auto-approved**. Nothing changes it while it is locked — pre-translation, bulk **Confirm**, copying to repetitions, split or join — except find and replace with **Include locked** or spell check's option to include locked segments, which only owners and admins can use: they save a **Draft** and unlock the segment, since a draft is never locked. A locked segment keeps its status from one workflow step to the next. Unlocking keeps the status until someone edits the segment.
- **Confirming promotes and propagates.** Ctrl+Enter runs QA on the text you are confirming and refuses if an open **Error** remains ([why](/docs/guides/quality-assurance#why-confirm-can-be-refused)). Otherwise it sets **Approved** and copies your translation to every unlocked segment with the same source text in the same file and target language. Those rows become **Approved** too, and the copy replaces whatever was there, including rows that were already approved. They lose the QA findings of their old text, ignored ones included; the next quality check looks at them again. A locked repetition keeps its text, status and findings, and so does one someone else has open. A message tells you how many rows were updated, and how many repetitions were left out because someone else is editing them.
- **A segment someone else has open is read-only for you**, and bulk actions skip it ([how it works](/docs/guides/cat-editor#when-someone-else-is-editing-a-segment)).
- **Corrections are drafts.** A spell-check **Change** or **Change all**, and a find and replace, save a draft, so an approved segment becomes **Draft**. Find and replace keeps the segment's inline tags.
- **Split and join reset the status.** The source changed, so the approval, auto-approval, AI score and match percentage of the rows involved are cleared, even when the old row was **Approved**. A split keeps the whole translation on the first piece as a **Draft**, or **Untranslated** if it had no text, and the second piece starts **Untranslated**. A join leaves one **Draft** row with the translations joined in order. Rolling the step back afterwards does not bring an earlier approval back. See [Split and join segments](/docs/guides/cat-editor#split-and-join-segments).
- **Done means Approved or Auto-approved.** When you click **Complete step**, every other status counts as left to do: the dialog counts drafts and untranslated segments, and **Approve all and complete** confirms the drafts first. A segment auto-approved in an earlier step that nobody has edited becomes **Auto-approved** again, not **Approved**. It does not block you.
- **The translation memory learns from confirmed work.** A translation goes to the TM when you press Ctrl+Enter or confirm it through the API, and every **Approved** segment is saved again when a workflow step or the job is completed — except one named **Confirmed by assistant**, which waits for a person's Ctrl+Enter. **Auto-approved** segments are never written ([why](/docs/concepts/quality-estimation#why-auto-approved-text-never-enters-the-translation-memory)). An exact match that pre-translation confirmed for you *is* written — it came out of a memory in the first place.
- **Clear resets everything.** **Clear** makes the segment **Untranslated**, whatever its status; **Clear translation** in the selection bar does the same for the selected segments that have a translation and are not locked. Either way it removes the origin, match percentage, AI score, QA findings and any comment written by **AI** — those described the text that just went away. Comments you wrote stay, and TM entries are not affected. On a segment with nothing saved, **Clear** only empties the editor: no dialog, and the status does not change. Every Clear works up to and including the workflow's first step that a person does (a human or hybrid step); automated steps before it, such as **Pre-translation**, do not count. A workflow with no such step allows Clear in its first step only.

See also [How a translation flows](/docs/concepts/how-a-translation-flows) and [Quality estimation](/docs/concepts/quality-estimation).
