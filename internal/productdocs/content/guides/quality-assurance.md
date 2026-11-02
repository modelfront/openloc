---
title: "Quality assurance"
summary: "Set up QA profiles, understand each check, run QA on a job and read the results."
order: 50
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-30
sources:
  - web/src/pages/QAProfiles.tsx
  - web/src/components/QANumberFormatLanguages.tsx
  - web/src/lib/qa-number-format.ts
  - web/src/components/QARegexRules.tsx
  - web/src/components/QARegexRuleDialog.tsx
  - web/src/components/QARuleSeverityToggle.tsx
  - web/src/lib/qa-regex.ts
  - internal/placeable/*.go
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
  - numeric-recognition-scope
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

Clicking the active severity again disables the check. Every change saves at once. While **Number format** is on, its row also shows the format the target must use.

To rename a profile, click the pencil next to its name. The trash icon deletes it; a profile that is still assigned to a project **or a template** cannot be deleted, so remove it from those first.

![The QA profiles page with a profile expanded: every check on its own row, with Error, Warning, Info and Disabled to choose from, and the four tag sub-checks as checkboxes.](assets/qa-profile-rules.png)

## The checks

| Check | What it finds |
| --- | --- |
| **Empty target** | The target is empty or contains only spaces. The other checks are then skipped for that segment — except **Consistency**, which still compares the empty target with the other translations of the same source text, so an empty segment can carry both findings. |
| **Tag checks** | Tag problems between source and target. Four sub-checks can be switched on or off with checkboxes: **Missing tags** (a source tag is not in the target), **Extra tags** (the target has a tag the source does not), **Unclosed tags** (an opening tag without its closing tag) and **Unopened tags** (a closing tag without its opening tag). |
| **Number consistency** | A number in the source is missing from the target. Numbers are compared by value, each read the way its own language writes numbers (or, while **Number format** is on, the way [the profile sets for that language](#number-format-by-language)): "1,234.5" in an English source matches "1.234,5" in a German target, while "25.16" and "2516" are different numbers. A number copied unchanged always matches. While **Dates**, **Times** or **Units** is on, the numbers of a date, time or measurement are compared by that check instead. |
| **Extra number in the target** (part of **Number consistency**, not a separate row in the profile) | A number that is in the target but not in the source. It arrives as its own finding, always a **warning** whatever severity you set for Number consistency, because it is often correct — for example a number the source spells out in words. You can ignore it on its own. |
| **Number format** | A target number written with a decimal or thousands separator its language does not use. Under the row, choose **Source or target format** to accept the separators of the source number or of the target language, or **Target format only** to accept only the target language's: in German, "25,16" and "1.000". A number without a thousands separator is always fine. |
| **Dates** | A date with a different day, month or year in the target. Dates are read in the usual forms — "14.03.2026", "03/14/2026", "2026-03-14", "March 14, 2026", "14 mars 2026", "2026年3月14日" — with the day and month order of each side's language. A date copied unchanged matches, and so does "March 14" against "14/03". A year the source has and the target drops is a change. |
| **Times** | A time that is different in the target. "3:30 PM" and "15:30" are the same time, "13:30" is not. "15 h", "15 Uhr" and "午後3時" are read as times too. |
| **Units** | A measurement converted between imperial and metric units that does not match, or a unit changed without converting the number ("5 miles" → "5 km"). Rounding is fine: "5 miles" → "8 km" and "70 °F" → "21 °C" match. Covers length, weight, volume, temperature, area and speed. |
| **Placeables** | A web or email address, a software variable such as `{0}`, `%s`, `{{name}}` or `${x}`, or a product code such as "XJ-500B" that is in the source but missing from the target. Each is counted, so a variable the source has twice must be in the target twice. It also reports an address or variable that the target has and the source does not, which is usually one that was changed. Under the row, **URLs and emails**, **Alphanumerics** and **Variables** choose what it checks; all three are on by default. Numbers, dates, times, measurements and amounts are left to the checks above; for anything else, add a [custom regex rule](#custom-regex-rules). |
| **Terminology** | The source contains a term from an assigned term base, but none of its approved translations is in the target — or it is used fewer times than the source term appears. Any approved synonym satisfies the check. Pending terms are not enforced. |
| **Forbidden terms** | The target contains a term marked **Forbidden** in an assigned term base. All the forbidden terms found in one segment are listed in a single finding, so ignoring that finding ignores all of them. |
| **Consistency** | The same source sentence has a different translation elsewhere in the same file and language. |
| **Punctuation** | The sentence-ending punctuation differs between source and target — the check looks at `.` `!` `?` `:` `;` — or the target is missing a leading `•`, `–`, `—`, `-`, `¿` or `¡`. It also reports ending punctuation the target adds and the source does not have. |
| **Target identical to source** | The target text is the same as the source. This usually means the segment was confirmed without being translated. |

When a segment has several dates, times or measurements and the check cannot tell which target one stands for which source one, **Number consistency** reports the changed numbers instead.

### Number format by language

While **Number format** is on, **Number format by language** under its row lists the languages that write numbers their own way. Pick one or more languages and one of eight formats — 1,234.56, 1.234,56, 1 234,56, 1 234.56, 1'234.56, 1'234,56, 1234.56 or 1234,56 — and click **Add language** (with two picked it reads **Add 2 languages**, and so on); each language you picked gets that format. Change a listed language's format or remove it from its row.

The number checks then read that language's numbers and measurements with those separators, in the source and in the target, and **Target format only** expects the format you picked. While **Number format** is off, the list is hidden and not used, but it is kept for when you turn the check back on. A language without a region, such as Spanish, covers all its regional variants; Spanish (Mexico) covers only itself and wins over Spanish. A language not listed keeps its usual format. A profile holds up to 100 languages, and they are part of the profile's export.

The **Terminology** and **Forbidden terms** checks need a term base assigned to the project's language pair. See [Terminology](/docs/guides/terminology).

## Custom regex rules

A regex rule is a check you write yourself: a pattern, where to look for it, and the message its findings show. A profile can hold up to 50 of them. Their findings behave like those of the built-in checks: they block a confirm at **Error**, show in the dots and the **QA findings** section, and can be ignored.

1. Expand the profile and click **Add regex rule**.
2. Fill in the rule and click **Save**.

| Field | What it does |
| --- | --- |
| **Name** | How the rule is listed in the profile. |
| **Mode** | **Target must not match** flags a target that contains the pattern. **Target must match** flags a target that does not. **Source matches, target does not** adds a **Source pattern** field and flags the segment when the source contains the source pattern and the target does not contain the target pattern. **Source must not match** checks the source only. |
| **Pattern (target)** | Called **Pattern (source)** in **Source must not match** mode. A regular expression in [RE2 syntax](https://github.com/google/re2/wiki/Syntax), at most 1,000 characters. It has no lookahead and no backreferences. The pattern sees the segment as the editor shows it: each tag is its label, such as `<1>`, `</1>` or `{1}`, and a placeholder tag is the text it stands for. So `\d <1>kg</1>` finds a number followed by a tagged unit. **NBSP** and **NNBSP** insert a non-breaking space and a narrow non-breaking space; the line under the field shows where they are. The help icon next to the field lists example patterns. |
| **Ignore case** | Matches upper and lower case alike. |
| **Message** | The text every finding of this rule shows. |
| **Target languages** | The languages the rule runs on; empty means all. A language without a region, such as French, covers all its regional variants; French (Canada) covers only itself. |
| **Severity** | As for the built-in checks. |

For example, French puts a non-breaking space between a number and its unit. A rule in **Target must not match** mode with the pattern `\d (kg|cm|%)`, the message "Use a non-breaking space before the unit." and the target language French flags "5 kg" typed with a regular space, and leaves it alone when the space is non-breaking.

A pattern is checked when you save it. A syntax error, a pattern that matches empty text, or a pattern too slow on a long test text is refused, and the reason shows under the field. A target left empty is reported only by **Empty target**, so the target modes skip it.

## Export and import a profile

**Export** in an expanded profile downloads the profile and all its rules as a JSON file. The text is kept exactly, special spaces included.

- **Import** at the top of the page creates a new profile from such a file. If the name is taken, a number is added, as in "Strict QA (2)".
- **Import rules** in an expanded profile replaces all of its rules, custom regex rules included, with the rules from the file. The profile's name and description stay.

A file with an invalid rule is refused as a whole, and nothing changes.

## Assign a profile to a project or template

1. Open the project's **Settings** tab, or open the template.
2. In the **Quality assurance** block, find **QA profile**.
3. Pick a profile, or **None** to run no checks.

The external-link icon at the end of the line opens the picked profile in a new tab, on the **QA profiles** page with that profile highlighted and its rules expanded.

A project created from a template starts with the template's QA profile. Changing the profile on the **QA profiles** page changes every project that uses it, because projects follow the profile live. A project with no QA profile runs no checks and never blocks a confirm.

## Run QA on a job

1. Open a job in the CAT editor.
2. Click **Run QA** in the toolbar. Any unsaved edit in the active segment is saved first.
3. When the run finishes, a message reports the number of issues found — and how many findings are ignored, if any. The button label changes to **QA (N)**, where N is the count of open errors and warnings, and reads **Run QA** again when nothing is open.

The findings of the last run load together with the editor, so the dots and the **QA (N)** count are there before anyone clicks the button. Every toolbar run checks every segment again and keeps the findings you ignored. The button is disabled while the job has no segments. Running QA needs a QA profile on the project; without one **Run QA** is dimmed, in the toolbar and in the selection bar, and hovering it says why.

To check only some segments, select them with the checkboxes in the grid's first column and click **Run QA** in the selection bar. Only the selected segments are checked and only their findings are replaced; the rest of the job keeps what it has. The message reads, for example, "QA on 3 segments: 2 issue(s) found", counting open errors and warnings on those segments.

### Read the results

- **One dot on each segment.** A segment with findings shows one dot in the grid's last column, in the colour of its most severe open finding: red for an error, yellow for a warning, blue for info. When every finding is ignored, the dot is a hollow grey ring. Hover the dot to list all the segment's findings, with ignored ones marked.

  ![Hovering a QA dot lists each of the segment's findings with its severity and message.](assets/qa-finding-tooltip.png)
- **The QA findings section.** In the editor sidebar, the **QA findings** section lists every finding of the segment you are on, with the full message and an **Ignore** button on each. The section header counts the open findings and the ignored ones apart, for example "2 + 1 ignored".
- **Filter by result.** The segment filter bar has **Has errors**, **Has warnings**, **No issues** and **Ignored** buttons, so you can step through only the segments that need attention. Pick several to list the segments that match any of them. An ignored finding does not count as an issue, so a segment whose only finding is ignored is listed under **No issues** as well as under **Ignored**. If the findings cannot be loaded, the buttons are greyed out rather than matching every row.
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
- Split a segment or join segments in the editor and the findings of the rows that change are cleared, ignores included, because they were found on the old source. QA runs again on the next save of each row.
- Turn the check off in the QA profile and the next run drops the finding together with its ignore. Turning the check back on reports it again, not ignored.

Changing a profile does not re-check anything by itself. The findings already on the segments stay as they are until the next **Run QA**, or until somebody saves that segment — so after editing a profile, run QA on the job to see the new picture.

## Why "Confirm" can be refused

When a translator confirms a segment (Cmd/Ctrl+Enter), the server runs the enabled checks on the new text. If any check at **Error** severity fails, the confirm is refused and a message appears, for example "Fix QA error before confirming: Missing number(s) in target: 2026". With several errors the message shows the count and one example. The segment stays editable and nothing is lost.

In JSON, PO, plain text and Excel files whose strings are HTML (the **Strings contain HTML** setting, on by default; see [Settings](/docs/reference/settings)), a confirm is also refused when the translation has a tag the source does not have, for example "Fix QA error before confirming: Tag <b> is not in the source". Tags are compared as a browser reads them, so attribute order and quoting do not matter. This check runs with or without a QA profile and cannot be ignored; remove the tag to confirm. A file uploaded with the setting off has no tag check.

Only an open **Error** blocks a confirm. **Warning** and **Info** findings are shown but never stop the translator, and an ignored error does not stop them either. Drafts saved while typing are never blocked — only the confirm is.

What to do:

1. Read the message and fix the text. For a missing tag, use the missing-tags bar above the editor or press F8 to insert the next tag; for a missing placeable, press F8 or click it in the source. For a terminology error, click the approved term in the sidebar, or press F8 to insert it.
2. Confirm again. The check runs on the corrected text.
3. If the finding is wrong for this segment, click **Ignore** on it in the sidebar's **QA findings** section and confirm again.
4. If a check is too strict for the whole project, a project manager can change its severity to **Warning** in the QA profile, or assign a different profile. The change applies immediately.

A rule that changes while someone is editing is applied on their next confirm.

**Confirm** in the selection bar runs the same check on every selected segment. A segment with an open error is skipped and stays as it is; its findings are stored, so its dot shows why. A segment with a tag the source does not have is skipped too, without a dot: the message says "with a tag the source does not have", and confirming that segment on its own names the tag. The other selected segments are confirmed, apart from the ones the [selection bar always leaves alone](/docs/guides/cat-editor#select-several-segments-and-act-on-them), such as segments someone else is editing.
