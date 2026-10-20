---
title: "Supported file formats"
summary: "Every file type you can upload, what comes back, and format-specific notes."
order: 20
roles: [owner, admin, project_manager, linguist]
updated_at: 2026-10-19
sources:
  - internal/parser/**
  - sidecars/parser/**
  - web/src/lib/source-file-formats.ts
  - internal/pipeline/export.go
limitations:
  - android-tm-generations
  - text-format-prose-angle-brackets
  - pptx-charts
  - pptx-masters-layouts
  - pptx-rtl-direction
related:
  - guides/projects-and-files
  - guides/cat-editor
  - guides/translation-memory
---
You can upload the file types below to a project. The system splits each file into segments, and after translation it rebuilds the file in its original format. For the upload and download steps themselves see [Projects and files](/docs/guides/projects-and-files).

## Formats

| Format | Extensions | What is translated | Notes |
|---|---|---|---|
| Word document | `.docx` | Paragraphs in the body, tables, headers and footers, split into sentences. | Bold, italic and links inside a paragraph become tags in the editor; a link is a tag pair around its text. The translated Word file puts bold, italic and links back where the tags are in the translation, and a link keeps its target. Text outside any tag gets the paragraph's normal formatting, and a link tag you remove leaves that text without a link. Other formatting, such as a colour change inside a paragraph, is not a tag. |
| PowerPoint | `.pptx` | Text on every slide, hidden slides included: text boxes, placeholders, tables, grouped shapes and SmartArt diagrams. Also the speaker notes and the alt text of pictures and shapes. Long text is split into sentences. Text without letters or digits, such as a lone bullet or arrow, stays as it is. | Text formatted differently from the rest of its paragraph (bold, italic, a link, another colour or size), line breaks and fields such as the slide number become tags. Text boxes keep their size and fonts, so check long translations in PowerPoint. Translated text that PowerPoint has marked as the source language is marked as the target language, so spell-check uses the right one. A PowerPoint 97–2003 file (`.ppt`) is refused: save it as `.pptx` and upload it again. |
| PDF | `.pdf` | Text blocks from the text layer, split into sentences; each table cell is split on its own. A scanned PDF without a text layer is read with OCR (text recognition from the image). | Import only. There is no translated PDF; download the **Bilingual XLIFF** instead. |
| XLIFF | `.xliff`, `.xlf`, `.sdlxliff`, `.mxliff`, `.mqxliff` | Every translation unit that is not marked "do not translate". Trados files are split at their own segment marks. | XLIFF 1.2 and the Trados, Phrase and memoQ variants. Inline tags become tags in the editor and are written back on export. Existing target text is imported and shows the **File** origin, into the project target language the file names in its `target-language`; a file that names none goes to a project with one target language. In any other case the text is not imported and the upload shows a warning. Edited files can be brought back with [Import XLIFF](/docs/guides/projects-and-files#import-an-edited-xliff). |
| Excel | `.xlsx` | Without language codes in row 1 of the first sheet: the sheets you choose at upload, and on each the columns you choose or every text cell, row by row (see [Choose the columns of an Excel file](/docs/guides/projects-and-files#choose-the-columns-of-an-excel-file)); uploaded without that choice, through the API for example, every text cell of every visible sheet, sheet by sheet and row by row. Either way, numbers, dates, true/false values, formulas and text without a letter are left out. With language codes in row 1, such as `en`, `fr` or `pt-BR`, where a heading that names no language, such as `SKU`, is not a code: the rows of the first sheet, where the first language column is the source and the rest are targets. A header that starts with "Source" also marks the source column. | In a sheet with language codes, text already in a target column is imported with the **File** origin into each target language's job, from the column the download writes for that language (see [What you download](#what-you-download)): a `fr` column fills a `fr-CA` job. The columns do not decide how many jobs there are — the project's target languages do. An Excel 97–2003 file (`.xls`) is refused: save it as `.xlsx` and upload it again. |
| JSON | `.json` | Every non-empty string value, including nested objects, in file order, split into sentences. The key path becomes the segment key; the sentences of one value get `#1`, `#2`… after it. | Lists (arrays) are not translated. A key that appears twice is one segment. HTML inside a value becomes tags unless **Strings contain HTML** is off. The translated file differs from the upload only in the translated values; key order, indentation and numbers are untouched. |
| Gettext | `.po`, `.pot` | Every entry's source string. Each `msgstr[N]` of a plural entry is its own segment: form 0 shows the singular source, every other form the plural source. | Entries marked "fuzzy" are skipped and kept as they are. Existing translations are imported with the **File** origin into the project target language named in the file's `Language` header; a file that names none goes to a project with one target language. In any other case they are not imported and the upload shows a warning. |
| HTML | `.html`, `.htm` | Text in paragraphs, headings, list items, table cells, captions, quotes and the page title, wherever it sits in a block. Also the `alt` and `title` of images, the `title` of links and abbreviations, the `alt`, `value`, `title` and `placeholder` of inputs (not file, hidden, image or password inputs) and the `abbr` of table cells, each as its own segment. Each of these is split into sentences. | Scripts, styles and code blocks are skipped. Every inline element inside a block (links, bold, italic, spans, line breaks and any other) becomes a tag. A block inside another block ends the outer segment: the text before it, the nested block and the text after it are separate segments. On export a tag is written as an element only when the source block has it; links, spans and other elements come back exactly as in the source. Formatting you add in the editor (bold, italic, subscript, superscript, underline, strikethrough, code, line breaks) and links with an `http`, `https` or `mailto` address are also written as elements. Any other markup typed into a translation is written as text. |
| Markdown | `.md` | Headings, paragraphs and list items, split into sentences. | Code blocks and horizontal rules are skipped. Inline markup such as `**bold**`, links and images stays inside the segment text. |
| Plain text | `.txt` | Every non-empty line, split into sentences. | HTML inside a line becomes tags unless **Strings contain HTML** is off. |
| Apple strings | `.strings` | The value of every `"key" = "value";` line. | The translated file keeps the keys and comments. |
| Android resources | `.xml` with a `<resources>` root | Strings, string arrays and plurals. | Items, string arrays and plurals marked as not translatable are skipped. The editor shows the text as the app shows it: `&`, `<`, `'` and `"` appear as characters, not as entities, CDATA or backslash escapes, and quotes wrapping a whole string are not shown. Export writes them back escaped the Android way. HTML in a string, as real tags or as escaped markup (`&lt;b&gt;`), becomes tags. On export a tag is written as an element only when the source string has that exact tag; any other tag, such as bold added in the editor or a changed link, is written as escaped text. The rest of the file is unchanged. |
| Generic XML | any other `.xml` | Text inside elements. Long text is split into sentences. | Works for DocBook, DITA, SCHEMA ST4 and in-house schemas. Inline elements become tags. The translated file is the original document with only the text replaced; comments, declarations and attributes are untouched. |

## How text is split into sentences

Most formats cut long text into one segment per sentence, so the editor and the translation memory work sentence by sentence. A sentence ends at `.`, `!`, `?` or `:` followed by a space and a capital letter, a digit or an opening quote or bracket. Common abbreviations such as "e.g.", "etc." and "Dr." do not end a sentence, and neither does a period inside a number or a name. A sentence never ends inside a tag: when bold text or a link runs across two sentences, they stay one segment. In the translated file the sentences are put back together with the spacing of the original.

## How the format is chosen

The file extension decides the format. The only exception is `.xml`: a file whose first element is `<resources>` is read as Android resources, everything else as generic XML. A file with an extension that is not in the table is refused at upload.

One limit worth knowing before you export from another tool: only **XLIFF 1.2** and the Trados, Phrase and memoQ variants of it are read. An XLIFF 2.x file is not, even though the extension is the same.

## Tags and formatting

A **tag** is a placeholder in a segment for something that is not text: a link, a line break, a bold span. The editor shows tags as chips you can move but not edit. DOCX, PPTX, XLIFF, HTML, generic XML and Android resources produce tags. JSON, PO, TXT and XLSX produce them when a value contains simple HTML (`<b>`, `<i>`, `<u>`, `<a>`, `<span>`, `<br>`) and the **Strings contain HTML** setting is on (the default; see [Settings](/docs/reference/settings)). Every tag in the source should also be in the target; the QA tag checks report when one is missing. See [Work with tags](/docs/guides/cat-editor#work-with-tags).

With the setting on, a translation in a JSON, PO, TXT or XLSX file may hold only the tags its source has. A tag the source does not have cannot be confirmed (see [Why "Confirm" can be refused](/docs/guides/quality-assurance#why-confirm-can-be-refused)), and the translated file writes it as text, not as markup.

With the setting off, strings are plain text: no tags, no tag check, and the translation is exported exactly as typed.

Bold, italic, superscript and subscript that you add yourself in the editor (see [Format text](/docs/guides/cat-editor#format-text)) are kept only in HTML, the one format that can hold them. Every other format receives the plain text; in generic XML and PowerPoint the editor formatting is dropped, while the document's own tags still come back.

In HTML and Android resources the editor shows `&`, `<` and `>` as the characters themselves, and the translated file escapes them again. Type `&`, not `&amp;`.

## What you download

The **Download** menu offers **Translated file**, **Bilingual XLIFF** and **Source file**. See [Download translated files](/docs/guides/projects-and-files#download-translated-files).

| Format | Translated file |
|---|---|
| DOCX, PPTX, HTML, Markdown, TXT, JSON, PO/POT, Apple strings, Android resources, generic XML | The original file with the translated text in place. Untranslated segments keep their source text. |
| XLIFF | The same XLIFF with the target elements filled in. |
| XLSX | The workbook with the translations written into the column for the job's language, on the first sheet. The column is the one headed with the job's exact language code, or with the bare base code of a regional variant: a `fr` column takes a `fr-CA` job, and a `fr-CA` column takes a `fr` job. Anything else stops the download with a message that names the columns: no column for the language, a column of a different language, a sibling variant (a `zh-Hans` column for a `zh-Hant` job), or two columns that both fit. Add the column to the workbook, or download the **Bilingual XLIFF**. A workbook without language codes in row 1 gets each translation written into the cell its source came from, which keeps its formatting; untranslated cells keep their source text. For a workbook uploaded without a column choice, a row 1 with one language code among other headings stops the download: head every language column with a code written like `fr` or `pt-BR`. |
| PDF | Not available. Use **Bilingual XLIFF**. |

**Translated file** is offered once the job has at least one **Approved** or **Auto-approved** segment. **Bilingual XLIFF** works as soon as the file has segments.

## Limits

- One upload request can carry up to 50 MB.
- An Excel file with language codes in row 1 is read from its first sheet only. Without them, hidden sheets are never translated and cannot be chosen.
- A PowerPoint or Excel file is refused, with a message that says why and what to do, when it is password-protected, is not a valid file, has no text to translate (for Excel: on its visible sheets, or in the columns you chose), or is too large: an Excel file over 256 MB once unzipped, or a presentation whose slides, notes and diagrams are over 64 MB once unzipped or hold too many shapes.
