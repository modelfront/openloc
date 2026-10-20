---
title: "Quality assurance"
summary: "Set up QA profiles, understand each check, run QA on a job and read the results."
order: 50
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-17
sources:
  - web/src/pages/QAProfiles.tsx
  - web/src/components/SegmentRow.tsx
  - web/src/components/SegmentFilterBar.tsx
  - web/src/components/SegmentSelectionBar.tsx
  - web/src/components/SettingsEditor.tsx
  - web/src/components/QASummaryDot.tsx
  - web/src/components/QAFindingsSection.tsx
  - web/src/hooks/use-qa-finding-ignore.ts
  - web/src/lib/qa-block.ts
  - web/src/lib/qa-findings.ts
  - internal/qa/**
  - internal/translation/handler_qa.go
  - internal/translation/source_tags.go
limitations:
  - punctuation-cjk
related:
  - guides/terminology
  - guides/cat-editor
  - guides/projects-and-files
  - reference/segment-statuses
  - concepts/quality-estimation
screenshots:
  - qa-profile-rules.png
  - qa-finding-tooltip.png
  - cat-editor.png
---
Quality assurance (QA) checks look for mechanical mistakes in a translation: a missing tag, a number that changed, an empty target, a forbidden term. They do not judge whether the translation reads well — that is what [quality estimation](/docs/concepts/quality-estimation) is for. A *QA profile* decides which checks run on a project and how serious each finding is.

## Create a QA profile

1. Open **QA profiles** in the left menu.
2. Click **New QA profile**.
3. Enter a **Name** (for example "Strict QA") and an optional **Description**, then click **Create**.
4. Click the new row to expand it. The rules editor lists every check.

For each check, click a severity badge to turn it on at that level:

| Severity | Effect |
| --- | --- |
| **Error** | Shown as a red dot. A segment with an error cannot be confirmed. |
| **Warning** | Shown as a yellow dot. The segment can still be confirmed. |
| **Info** | Shown as a blue dot. For information only. |
| **Disabled** | The check does not run. |

Clicking the active severity again disables the check. Every change saves at once.

To rename a profile, click the pencil next to its name. The trash icon deletes it; a profile that is still assigned to a project **or a template** cannot be deleted, so remove it from those first.

![The QA profiles page with a profile expanded: every check on its own row, with Error, Warning, Info and Disabled to choose from, and the four tag sub-checks as checkboxes.](assets/qa-profile-rules.png)

## The checks

| Check | What it finds |
| --- | --- |
| **Empty target** | The target is empty or contains only spaces. The other checks are then skipped for that segment — except **Consistency**, which still compares the empty target with the other translations of the same source text, so an empty segment can carry both findings. |
| **Tag checks** | Tag problems between source and target. Four sub-checks can be switched on or off with checkboxes: **Missing tags** (a source tag is not in the target), **Extra tags** (the target has a tag the source does not), **Unclosed tags** (an opening tag without its closing tag) and **Unopened tags** (a closing tag without its opening tag). |
| **Number consistency** | A number in the source is missing from the target. Separators are ignored, so "1,000" and "1.000" count as the same number — and because the decimal point is ignored too, so do "1.5" and "15". |
| **Extra number in the target** (part of **Number consistency**, not a separate row in the profile) | A number that is in the target but not in the source. It arrives as its own finding, always a **warning** whatever severity you set for Number consistency, because it is often correct — for example a number the source spells out in words. You can ignore it on its own. |
| **Terminology** | The source contains a term from an assigned term base, but none of its approved translations is in the target — or it is used fewer times than the source term appears. Any approved synonym satisfies the check. Pending terms are not enforced. |
| **Forbidden terms** | The target contains a term marked **Forbidden** in an assigned term base. All the forbidden terms found in one segment are listed in a single finding, so ignoring that finding ignores all of them. |
| **Consistency** | The same source sentence has a different translation elsewhere in the same file and language. |
| **Punctuation** | The sentence-ending punctuation differs between source and target — the check looks at `.` `!` `?` `:` `;` — or the target is missing a leading `•`, `–`, `—`, `-`, `¿` or `¡`. It also reports ending punctuation the target adds and the source does not have. |
| **Target identical to source** | The target text is the same as the source. This usually means the segment was confirmed without being translated. |

The **Terminology** and **Forbidden terms** checks need a term base assigned to the project's language pair. See [Terminology](/docs/guides/terminology).

## Assign a profile to a project or template

1. Open the project's **Settings** tab, or open the template.
2. In the **Project settings** card, find the **QA Profile** dropdown.
3. Pick a profile, or **None** to run no checks.

A project created from a template starts with the template's QA profile. Changing the profile on the **QA profiles** page changes every project that uses it, because projects follow the profile live. A project with no QA profile runs no checks and never blocks a confirm.

## Run QA on a job

1. Open a job in the CAT editor.
2. Click **Run QA** in the toolbar. Any unsaved edit in the active segment is saved first.
3. When the run finishes, a message reports the number of issues found — and how many findings are ignored, if any. The button label changes to **QA (N)**, where N is the count of open errors and warnings, and reads **Run QA** again when nothing is open.

The findings of the last run load together with the editor, so the dots and the **QA (N)** count are there before anyone clicks the button. Every toolbar run checks every segment again and keeps the findings you ignored. The button is disabled while the job has no segments. Running QA needs a QA profile on the project; without one the run is refused.

To check only some segments, select them with the checkboxes in the grid's first column and click **Run QA** in the selection bar. Only the selected segments are checked and only their findings are replaced; the rest of the job keeps what it has. The message reads, for example, "QA on 3 segments: 2 issue(s) found", counting open errors and warnings on those segments.

### Read the results

- **One dot on each segment.** A segment with findings shows one dot in the grid's last column, in the colour of its most severe open finding: red for an error, yellow for a warning, blue for info. When every finding is ignored, the dot is a hollow grey ring. Hover the dot to list all the segment's findings, with ignored ones marked.

  ![Hovering a QA dot lists each of the segment's findings with its severity and message.](assets/qa-finding-tooltip.png)
- **The QA findings section.** In the editor sidebar, the **QA findings** section lists every finding of the segment you are on, with the full message and an **Ignore** button on each. The section header counts the open findings and the ignored ones apart, for example "2 + 1 ignored".
- **Filter by result.** The segment filter bar has **Has errors**, **Has warnings**, **No issues** and **Ignored** buttons, so you can step through only the segments that need attention. An ignored finding does not count as an issue, so a segment whose only finding is ignored is listed under **No issues** as well as under **Ignored**. If the findings cannot be loaded, the buttons are greyed out rather than matching every row.
- **Results stay up to date while you edit.** After every save and every confirm, QA runs again for that one segment and its dots are refreshed. You do not need to click **Run QA** again to see whether a fix worked.

There is no separate QA report page; the results live in the editor next to the segments.

![The CAT editor](assets/cat-editor.png)

## Ignore a false positive

Some findings are a correct rule on the wrong text: a brand name the terminology check wants translated, a punctuation convention this language writes differently. Instead of weakening a profile that other projects share, ignore that one finding.

1. Click the segment and open the **QA findings** section in the sidebar.
2. Click **Ignore** on the finding.

The finding stays in the list, drawn as a hollow grey ring with greyed-out text and a line saying who ignored it and when. The segment's dot takes the colour of its most severe finding that is still open. It stops counting in the **QA (N)** toolbar count and in the run message, and it stops blocking a confirm. **Un-ignore** on the same finding brings it back.

Anyone who can open the segment can ignore a finding: a manager on any job, a linguist on a job assigned to them.

An ignore belongs to one finding on one segment — not to the check, the file or the project. It lasts as long as the finding says the same thing:

- Edit the target so the same check reports something else (a different missing tag, another missing number) and the finding comes back as new, not ignored.
- Fix the text so the check passes and the finding disappears. If the same problem appears again later, it is a new finding.
- Replace the target outside a QA run — **Clear**, pre-translation from translation memory or machine translation, an AI post-editing step, propagation to identical segments, re-importing a bilingual file — and the segment's findings go with the text they described, ignores included.
- Turn the check off in the QA profile and the next run drops the finding together with its ignore. Turning the check back on reports it again, not ignored.

Changing a profile does not re-check anything by itself. The findings already on the segments stay as they are until the next **Run QA**, or until somebody saves that segment — so after editing a profile, run QA on the job to see the new picture.

## Why "Confirm" can be refused

When a translator confirms a segment (Cmd/Ctrl+Enter), the server runs the enabled checks on the new text. If any check at **Error** severity fails, the confirm is refused and a message appears, for example "Fix QA error before confirming: Missing number(s) in target: 2026". With several errors the message shows the count and one example. The segment stays editable and nothing is lost.

In JSON, PO, plain text and Excel files whose strings are HTML (the **Strings contain HTML** setting, on by default; see [Settings](/docs/reference/settings)), a confirm is also refused when the translation has a tag the source does not have, for example "Fix QA error before confirming: Tag <b> is not in the source". Tags are compared as a browser reads them, so attribute order and quoting do not matter. This check runs with or without a QA profile and cannot be ignored; remove the tag to confirm. A file uploaded with the setting off has no tag check.

Only an open **Error** blocks a confirm. **Warning** and **Info** findings are shown but never stop the translator, and an ignored error does not stop them either. Drafts saved while typing are never blocked — only the confirm is.

What to do:

1. Read the message and fix the text. For a missing tag, use the missing-tags bar above the editor or press F8 to insert the next tag. For a terminology error, click the approved term in the sidebar.
2. Confirm again. The check runs on the corrected text.
3. If the finding is wrong for this segment, click **Ignore** on it in the sidebar's **QA findings** section and confirm again.
4. If a check is too strict for the whole project, a project manager can change its severity to **Warning** in the QA profile, or assign a different profile. The change applies immediately.

A rule that changes while someone is editing is applied on their next confirm.

**Confirm** in the selection bar runs the same check on every selected segment. A segment with an open error is skipped and stays as it is; its findings are stored, so its dot shows why. A segment with a tag the source does not have is skipped too, without a dot: the message says "with a tag the source does not have", and confirming that segment on its own names the tag. The other selected segments are confirmed, apart from the ones the [selection bar always leaves alone](/docs/guides/cat-editor#select-several-segments-and-act-on-them), such as segments someone else is editing.
