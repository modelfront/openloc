"""Tests for DOCX parser."""

import os
from pathlib import Path

from parsers import docx_parser

FIXTURES = Path(__file__).parent / "fixtures"


def test_parse_sample_docx():
    """Parse sample.docx and verify all segments."""
    content = (FIXTURES / "sample.docx").read_bytes()
    segments = docx_parser.parse(content)

    assert len(segments) == 9

    # Body paragraphs
    assert segments[0].key == "para:0"
    assert segments[0].text == "Welcome to OpenLoc"
    assert segments[0].context == "body"

    assert segments[1].text == "This is a sample document for testing."
    assert segments[2].text == "Translation quality matters."

    # Table cells
    table_segments = [s for s in segments if s.context == "table"]
    assert len(table_segments) == 4
    table_texts = [s.text for s in table_segments]
    assert "Name" in table_texts
    assert "Role" in table_texts
    assert "Alice" in table_texts
    assert "Translator" in table_texts

    # Header and footer
    header = [s for s in segments if s.context == "header"]
    assert len(header) == 1
    assert header[0].text == "Document Header"

    footer = [s for s in segments if s.context == "footer"]
    assert len(footer) == 1
    assert footer[0].text == "Page 1"


def test_parse_keys_are_sequential():
    """Keys should be sequential para:0, para:1, etc."""
    content = (FIXTURES / "sample.docx").read_bytes()
    segments = docx_parser.parse(content)

    for i, seg in enumerate(segments):
        assert seg.key == f"para:{i}"
        assert seg.position == i


def test_reconstruct_replaces_text():
    """Reconstruct should replace translated segments and keep others."""
    content = (FIXTURES / "sample.docx").read_bytes()
    translations = {
        "para:0": "Bienvenido a OpenLoc",
        "para:1": "Este es un documento de ejemplo.",
    }
    result = docx_parser.reconstruct(content, translations)

    segments = docx_parser.parse(result)
    assert segments[0].text == "Bienvenido a OpenLoc"
    assert segments[1].text == "Este es un documento de ejemplo."
    # Untranslated segments should remain
    assert segments[2].text == "Translation quality matters."


def test_reconstruct_preserves_formatting():
    """Reconstruct should preserve paragraph formatting from first run."""
    content = (FIXTURES / "sample.docx").read_bytes()
    from docx import Document
    import io

    doc = Document(io.BytesIO(content))
    original_runs = doc.paragraphs[1].runs
    has_bold = any(r.bold for r in original_runs if r.bold is not None)

    translations = {"para:1": "Texto traducido"}
    result = docx_parser.reconstruct(content, translations)

    doc2 = Document(io.BytesIO(result))
    new_runs = doc2.paragraphs[1].runs
    assert len(new_runs) >= 1
    assert new_runs[0].text == "Texto traducido"


def test_reconstruct_empty_translations():
    """Reconstruct with no translations returns original content."""
    content = (FIXTURES / "sample.docx").read_bytes()
    result = docx_parser.reconstruct(content, {})

    segments = docx_parser.parse(result)
    assert segments[0].text == "Welcome to OpenLoc"


def test_parse_empty_docx():
    """Parsing a minimal/empty DOCX should return empty segments."""
    from docx import Document
    import io

    doc = Document()
    buf = io.BytesIO()
    doc.save(buf)

    segments = docx_parser.parse(buf.getvalue())
    assert segments == []
