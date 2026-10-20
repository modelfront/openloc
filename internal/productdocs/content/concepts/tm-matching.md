---
title: "How TM matching works"
summary: "Context matches, exact matches, fuzzy bands, penalties and the lookup order of several memories."
order: 20
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-16
sources:
  - internal/settings/tm_lookup_order.go
  - internal/translationmemory/**
  - internal/tmprofile/**
  - internal/strdist/**
  - internal/pipeline/translate.go
  - internal/pipeline/tm_write.go
  - web/src/components/settings/TMScoringFields.tsx
  - web/src/lib/tm-diff.ts
  - web/src/components/project/TMAssignmentSection.tsx
limitations:
  - android-tm-generations
related:
  - guides/translation-memory
  - concepts/analysis-bands
  - concepts/how-a-translation-flows
  - reference/segment-statuses
screenshots:
  - tm-assignments.png
---
A translation memory (TM) is a store of sentences that humans already translated. When a project is pre-translated, and again while a linguist works in the editor, every segment is looked up in the project's memories and comes back with a percentage.

## Three kinds of match

**Exact match (100%).** The source text of the segment is identical, character for character, to a stored entry, and nothing else counts against it (see penalties below). The stored translation is filled in and the segment becomes **Approved** on its own, because a person already approved that exact sentence. No editor is recorded for it; the segment's history shows a TM event instead of a name.

**Context match (101%).** An exact match where the surrounding text also matches. When a human confirms a segment, the memory remembers the sentence before it and the sentence after it. If a later document has the same sentence with the same neighbours, the match is shown as 101%. This matters for short sentences such as "Open" where the right translation depends on where the sentence sits. A 101% match behaves like a 100% match: filled in and approved.

Both neighbours have to match — one is not enough. For the first and last segment of a document, the missing neighbour counts as a match, so they can reach 101% too.

Entries imported from a TMX file carry no context, so they can only reach 100%. A memory remembers only the most recent context for each sentence.

**Fuzzy match (below 100%).** The stored sentence is similar but not the same. The percentage says how much of the text is the same. The comparison is exact: capital letters, spaces and punctuation all count as differences, and the result is rounded to a whole percent. A fuzzy match is filled in for the linguist to fix, but it is never approved automatically — it stays at **Pretranslated**. In the editor, the sidebar highlights what differs between the hit and the segment, in both directions — see [the CAT editor](/docs/guides/cat-editor).

## The fuzzy match threshold

Matches below the threshold are not offered at all. The default is **75%**. You change it in project settings, under Translation memory: the field is **Fuzzy match threshold**. Set it to 100% to accept exact matches only. Templates carry the same setting, so new projects start with your preferred value.

![The match scoring fields and the numbered list of translation memories in project settings](assets/tm-assignments.png)

A lower threshold gives more suggestions, but weak ones cost the linguist time to read and discard.

## Penalties

A penalty is a number of points taken off a match. Penalties add up, and they change two things at once: the percentage you see, and whether the match may be approved automatically. Any match that loses even one point is filled in for review instead of approved. This is deliberate: a penalty means something about the match is not fully trusted. Only a clean match is approved without a human — no penalty, same language on both sides, identical text.

| Penalty | When it applies | Default | Where to change |
|---|---|---|---|
| Language-variant mismatch | The entry is in a different region of the same language, for example a Brazilian Portuguese entry on a European Portuguese project. Charged once for the source side and once for the target side. | 1% per side | Translation memory, **Language-variant mismatch penalty** |
| Near-exact | The stored sentence differs only in punctuation, capital letters or spacing, like "Hello." for "Hello". | 1% | Translation memory, **Near-exact penalty** |
| Per-memory | Every match from one specific memory, for example a client-supplied or legacy memory you trust less. | 0% | The **Penalty** column on that memory's row |

A near-exact match starts from 100 and loses the penalty, so "Hello." against "Hello" reads 99%, not the 83% that plain character similarity would give. Three kinds of difference count as near-exact: capital letters anywhere in the sentence, extra spaces anywhere in the sentence, and punctuation at the very start or end. Punctuation *inside* a sentence, such as a comma that changes the meaning, is a normal fuzzy match.

Setting a penalty to 0% tells the system that the difference does not matter — but the two penalties behave differently here, and the difference matters:

- **Language-variant mismatch at 0%.** A Brazilian entry on a European project counts as a true exact match again and is approved automatically. Use this on purpose, not by accident.
- **Near-exact at 0%.** The match reads 100%, but the segment is still filled in for review. Only text that is identical, character for character, is ever approved without a person — "Hello." and "Hello" are not the same text, whatever the penalty says.

## Several memories

Each project has a numbered list of memories; the top row is the **Master**. The lookup goes down the list in order. A memory is used when its languages share a base language with the project's, so an `en` memory serves an `en-US` project and the other way round.

When more than one memory has a match, **TM lookup order** decides which one fills the segment. It sits under the list of memories and appears once the list has two or more:

- **By match percentage** (the default). Every memory is searched for an exact match first, so a lower memory only fills segments the memories above it did not match exactly. The segments still empty are then searched for a fuzzy match, and the first memory with a match above the threshold wins, even if a memory further down holds a higher percentage.
- **By TM order.** The first memory is searched for an exact and then a fuzzy match before the next memory is asked. A fuzzy match from a higher memory is kept even if a lower memory has an exact one.

Inside one memory, the best percentage wins. A penalty lowers the percentage, so a clean entry outranks an equally similar penalised one.

Each row has two checkboxes: **Read** means the memory is used for matches, **Write** means confirmed translations are saved into it.

![By match percentage: exact matches let every memory fill what the ones above it missed. For fuzzy matches the search stops at the first memory above the threshold, so a better percentage further down the list is never reached.](assets/tm-lookup-order.svg)

The **TM matches** list in the editor follows the same setting. By match percentage, it merges every Read memory and sorts by percentage, so its top hit is not always the one pre-translation filled in. By TM order, it lists the matches memory by memory in list order, best percentage first within each memory.

The list scores each hit as pre-translation does, a 101% context match included, and a hit inserted from it is recorded with the percentage its card shows. See [Origins and other marks](/docs/reference/segment-statuses#origins-and-other-marks).

## What is written back, and when

Every **Approved** segment is saved to the write memories: when a linguist confirms one in the editor, and again when a workflow step or the job is completed. Segments an AI step approved — the **Auto-approved** status — are never written, and neither is a fuzzy fill nobody touched. An **Approved** segment marked **Confirmed by assistant** is written only once a person confirms it ([why](/docs/guides/ai-assistant#segments-the-assistant-confirms)). See [Why auto-approved text never enters the translation memory](/docs/concepts/quality-estimation#why-auto-approved-text-never-enters-the-translation-memory).

One case follows from the status rather than from the rule: an exact match that pre-translation confirmed for you is **Approved**, so completing the step copies it into the write memories like any other approved segment. That does no harm — the text came out of a memory to begin with — but it does mean a write memory can gain entries nobody typed.

Each memory keeps one entry per source sentence **per language pair**. Confirming the same sentence again replaces the stored translation; the old one is kept in the entry's history. Entries are stored with the project's own language codes. Imported entries are the exception: a TMX file may bring several translations of the same sentence, and all of them are kept.

## TM profiles

A TM profile is a named bundle of a memory list plus its match scoring. Several projects and templates can follow one profile, and editing it changes all of them at once. While a project follows a profile, its own scoring fields are read-only; "Detach and customise" copies the profile's values onto the project so you can edit them there. See [Translation memory](/docs/guides/translation-memory).

## A worked example

The memory holds "Press the Start button." and the new segment is "Press the Stop button." Turning one into the other takes three edits across 23 characters, so the similarity is about 87%. With the default 75% threshold the match is filled in and left for review. From a memory with a 5% penalty the linguist sees 82% instead; from a memory in a different region on both sides, 85%.

A percentage tells the linguist how much work to expect, not whether the work can be skipped: a 99% match is not "almost approved".
