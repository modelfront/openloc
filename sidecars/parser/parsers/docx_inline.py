"""Inline content of a DOCX paragraph as XLIFF 1.2 rich text, and back.

A paragraph's runs and hyperlinks become text with paired <g> tags:
- Bold → <g id="N" ctype="bold">text</g>
- Italic → <g id="N" ctype="italic">text</g>
- Hyperlink → <g id="N" ctype="link">text</g>
Tags nest for combined formatting and for formatting inside a link.

On reconstruct a tag's id points back at the source element it came from, so a
translated link keeps its target and tagged text keeps its run's formatting.
"""

from __future__ import annotations

import copy
import re
from dataclasses import dataclass, field

from docx.oxml.ns import qn
from docx.text.hyperlink import Hyperlink
from docx.text.paragraph import Paragraph
from docx.text.run import Run

FORMAT_CTYPES = ("bold", "italic")
LINK = "link"

# The chip grammar of internal/parsers/common/sentence_segments.go (richToken): paired <g>, self-closing <x/>.
_CHIP = re.compile(r'<g(?:\s[^>]*)?>|</g>|<x(?:\s[^>]*)?/>')
_CHIP_ATTR = re.compile(r'\s(id|ctype)="([^"]*)"')


@dataclass
class SourceTag:
    ctype: str
    run: Run  # whose formatting the tagged text takes
    link: Hyperlink | None = None


@dataclass
class ParagraphTags:
    rich_text: str  # "" when the paragraph needs no tags
    by_id: dict[str, SourceTag] = field(default_factory=dict)
    base: Run | None = None  # formatting for text outside every tag


def paragraph_tags(para: Paragraph) -> ParagraphTags:
    """Tag a paragraph's formatting and links.

    A paragraph with one run and no link needs no tags, and neither does one
    whose runs are all plain.
    """
    items = [item for item in para.iter_inner_content() if item.text]
    runs: list[Run] = []
    for item in items:
        runs.extend(_text_runs(item.runs) if isinstance(item, Hyperlink) else [item])
    if not runs:
        return ParagraphTags("", base=para.runs[0] if para.runs else None)
    has_link = any(isinstance(item, Hyperlink) for item in items)
    if len(runs) == 1 and not has_link:
        return ParagraphTags("", base=runs[0])

    by_id: dict[str, SourceTag] = {}

    def open_tag(ctype: str, run: Run, link: Hyperlink | None = None) -> str:
        tag_id = str(len(by_id) + 1)
        by_id[tag_id] = SourceTag(ctype, run, link)
        return f'<g id="{tag_id}" ctype="{ctype}">'

    def run_rich(run: Run) -> str:
        ctypes = [c for c in FORMAT_CTYPES if getattr(run, c)]
        return "".join(open_tag(c, run) for c in ctypes) + run.text + "</g>" * len(ctypes)

    parts: list[str] = []
    for item in items:
        if isinstance(item, Hyperlink):
            link_runs = _text_runs(item.runs)
            parts.append(open_tag(LINK, link_runs[0], item) + "".join(map(run_rich, link_runs)) + "</g>")
        else:
            parts.append(run_rich(item))

    if not by_id:
        return ParagraphTags("", base=runs[0])
    plain = (r for r in items if isinstance(r, Run) and not any(getattr(r, c) for c in FORMAT_CTYPES))
    return ParagraphTags("".join(parts), by_id, next(plain, runs[0]))


def write_translation(para: Paragraph, translation: str) -> None:
    """Replace a paragraph's runs and links with the translation.

    Text outside tags takes the formatting of the paragraph's first plain run.
    A tag whose id and ctype match the source takes its source run's formatting,
    and a link tag rebuilds the source hyperlink around its text. Tags the source
    does not have still set bold and italic; an unknown link tag gives plain text.
    """
    tags = paragraph_tags(para)
    for item in list(para.iter_inner_content()):
        para._p.remove(item._element)

    open_link: _Open | None = None
    link_el = None
    for text, stack in _pieces(translation, tags.by_id):
        run = para.add_run(text)
        source = next((o.source.run for o in reversed(stack) if o.source), tags.base)
        if source is not None:
            _copy_run_formatting(source, run)
        for ctype in FORMAT_CTYPES:
            if any(o.ctype == ctype for o in stack):
                setattr(run, ctype, True)
            elif tags.by_id:
                # The source expresses bold and italic as tags, so the translation's tags decide.
                setattr(run, ctype, None)

        link = next((o for o in reversed(stack) if o.ctype == LINK and o.source), None)
        if link is None:
            open_link = None
            continue
        if link is not open_link:
            link_el = _empty_copy(link.source.link._element)
            run._r.addprevious(link_el)
            open_link = link
        link_el.append(run._r)


@dataclass(eq=False)
class _Open:
    """One opening tag in the translation; identity tells two copies of a tag apart."""

    ctype: str
    source: SourceTag | None


def _pieces(translation: str, by_id: dict[str, SourceTag]) -> list[tuple[str, tuple[_Open, ...]]]:
    """Split a translation into text pieces, each with the tags open around it.

    A stray closing tag is ignored, an unclosed tag closes at the end, a line
    break chip becomes "\\n" and any other self-closing chip is dropped.
    """
    pieces: list[tuple[str, tuple[_Open, ...]]] = []
    stack: list[_Open] = []

    def add(text: str):
        if not text:
            return
        key = tuple(stack)
        if pieces and pieces[-1][1] == key:
            pieces[-1] = (pieces[-1][0] + text, key)
        else:
            pieces.append((text, key))

    pos = 0
    for m in _CHIP.finditer(translation):
        add(translation[pos : m.start()])
        pos = m.end()
        tag = m.group(0)
        attrs = dict(_CHIP_ATTR.findall(tag))
        if tag == "</g>":
            if stack:
                stack.pop()
        elif tag.startswith("<x"):
            if attrs.get("ctype") == "lb":
                add("\n")
        else:
            ctype = attrs.get("ctype", "")
            source = by_id.get(attrs.get("id", ""))
            stack.append(_Open(ctype, source if source and source.ctype == ctype else None))
    add(translation[pos:])
    return pieces


def _text_runs(runs: list[Run]) -> list[Run]:
    return [r for r in runs if r.text]


def _empty_copy(element):
    """A copy of an element with its attributes and none of its children."""
    clone = copy.deepcopy(element)
    for child in list(clone):
        clone.remove(child)
    return clone


def _copy_run_formatting(source_run: Run, target_run: Run):
    """Copy character-level formatting (the whole w:rPr) from one run to another."""
    source_rpr = source_run._element.find(qn("w:rPr"))
    if source_rpr is not None:
        new_rpr = copy.deepcopy(source_rpr)
        existing = target_run._element.find(qn("w:rPr"))
        if existing is not None:
            target_run._element.remove(existing)
        # OOXML schema order: rPr must be the first child of w:r.
        target_run._element.insert(0, new_rpr)
