---
title: "Analysis bands and word counts"
summary: "How words are counted and why they land in repetitions, 100%, fuzzy or new."
order: 30
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-17
sources:
  - internal/analysis/**
  - internal/wordcount/**
  - internal/analytics/handler.go
  - web/src/api/analyses.ts
  - web/src/components/org/AnalysisWeights.tsx
  - internal/translation/service_draft.go
  - web/src/components/project/UploadConfirmDialog.tsx
limitations:
  - analysis-weights-unapplied
related:
  - concepts/tm-matching
  - concepts/quality-estimation
  - guides/projects-and-files
  - guides/analytics
screenshots:
  - project-analysis.png
---
An analysis is a snapshot of how much work a project holds: how many words there are, and how many of them the translation memory already covers. It is the basis for quotes and planning.

## How words are counted

Word counts follow the industry-standard rules. A word is a run of characters between spaces, with a few exceptions:

| Text | Counts as |
|---|---|
| "Hello world" | 2 words |
| "well-known", "it's", "l'homme" | 1 word each |
| "B2B", "COVID19" | 1 word each |
| "12345", "1,000.50", "$100", "100%" | 0 — standalone numbers are not translated, so they are not billed |
| "...", "—" | 0 — punctuation on its own |
| A web address or an email address | 1 word |
| Formatting tags inside the text | 0 — removed before counting |
| Chinese, Japanese and Korean text | 1 word per character |

Words are counted once, when a file is uploaded, and stored with each segment. Every later number — job size, analysis, productivity — adds up those stored counts, so figures never drift between screens.

Other tools use other rules, so their totals for the same document can differ by a few percent. Counting numbers as words or splitting hyphenated words are the usual reasons. See [File formats](/docs/reference/file-formats) for how each format is split into segments.

## Running an analysis

An analysis is a saved, unchanging record. Run one from the project's **Analyses** tab with **New analysis**, or let the upload do it: the upload dialog has an **Auto-analyze project files** checkbox, on by default. (The **New project** form has a similar checkbox of its own, **Run analysis on upload**.) An automatic analysis waits until pre-translation for all the project's files has finished, then covers the whole project in one snapshot. Several files uploaded together produce one analysis, not one per file.

An analysis can only run once pre-translation has finished, because it measures what pre-translation produced. Start one too early and it is refused with a message naming the jobs that are not ready.

![The Analyses tab of a project](assets/project-analysis.png)

## The bands

Each segment lands in exactly one band. The band comes from the translation memory match recorded on the segment: the one pre-translation filled in, or one a linguist inserted from the editor's **Resources** panel. See [How TM matching works](/docs/concepts/tm-matching) for how that percentage is made.

| Band | What it holds |
|---|---|
| **Repetitions** | The second and later occurrences of an identical source sentence, per target language, across all files in the analysis |
| **101%** | Exact matches whose surrounding sentences also matched |
| **100%** | Exact matches |
| **85–99%** | Good fuzzy matches |
| **65–84%** | Weaker fuzzy matches |
| **New** | Everything else: no match, or a match below 65% |

Each band shows segments, words and characters, and you can switch between the three. Under each figure a smaller number reports how many of those were **auto-approved** by quality estimation. See [Quality estimation](/docs/concepts/quality-estimation).

Three rules decide the band:

- **First occurrence keeps its band; repeats move out.** If the sentence "See page 4." appears in three files, the first copy is counted in its real band and the other two are counted as Repetitions. Nothing is counted twice, so the bands of a file always add up to its total. Repetition is exact: a sentence that differs by one comma is not a repetition.
- **Only the memory match counts.** The band reflects the memory match recorded on the segment. Inserting a TM match from the **Resources** panel records that match, and inserting an MT suggestion there counts the segment as **New**. Editing the text does not move the segment; a fuzzy match a linguist rewrote entirely still sits in its fuzzy band, because the analysis measures the starting point, not the effort spent. Automatic post-editing is the one thing that does move a segment: when an AI step rewrites a fuzzy match, the segment counts as **New** in the next analysis, because its text no longer comes from the memory.
- **Machine translation is not a band.** A segment with no memory match is New whether or not machine translation filled it in. New means "no translation memory leverage", which is what every major tool means by it. The auto-approved sub-count is where the machine's contribution shows.

A fuzzy match below 65% but above your project's threshold is still filled in for the linguist, but the analysis counts it as New. Weak matches save little time, so they are priced as new words.

Three other kinds of segment land in **New**, for the same reason — no memory helped: segments with no translation at all, segments the machine translated, and segments whose translation came in with the file (a pre-filled XLIFF, for example). Locked segments are not treated differently; they count in whichever band their match puts them.

## Analysis weights

The **Settings** tab of the **Organization** page has an **Analysis weights** section. A weight is the share of full translation effort a band is expected to take: 0% means free, 100% means a new word. The defaults are the usual industry figures:

| Band | Default weight |
|---|---|
| 100% (exact) | 0% |
| 95–99% | 25% |
| 85–94% | 50% |
| 75–84% | 75% |
| 50–74% | 100% |
| New (no match) | 100% |
| Repetitions | 0% |
| QE approved | 0% |

These bands are cut more finely than the analysis table above, because price lists usually are. The saved analysis itself shows plain counts — segments, words and characters. See [Analytics](/docs/guides/analytics) and [Settings](/docs/reference/settings).

## Why the analysis and the editor can disagree

The analysis is frozen at the moment it runs. The project is not.

- **The memory grows.** Every segment a linguist confirms is written to the memory. A sentence that was New in the analysis can be a 100% match in the next file.
- **Pre-translation can be rerun.** Applying the memory or machine translation again changes what the segments hold; the analysis you already took does not change with them.
- **Files can be added or deleted.** A row marked "(deleted)" belongs to a file removed after the snapshot.

When the numbers matter — a quote, a hand-off — take a fresh analysis. Older ones stay in the list so you can compare.

## A worked example

A project has two files. File A contains "Save your changes." once and file B contains it twice. The memory already holds "Save your changes" without the full stop.

- The copy in file A matches the memory at 99% (only punctuation differs), so it lands in **85–99%**.
- Both copies in file B are later occurrences of the same sentence and land in **Repetitions**.
- Each copy keeps its 3 words, so the file totals hold: 3 words in A, 6 in B.

After the linguist confirms the segment in file A, the memory holds the sentence with the full stop, and a new analysis of a third file with that sentence shows 100%.
