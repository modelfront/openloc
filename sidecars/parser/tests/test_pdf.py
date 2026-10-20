"""Tests for PDF parser."""

import io
import logging
import shutil
import sys
from pathlib import Path

import pypdfium2 as pdfium
import pytest

from parsers import pdf_parser

FIXTURES = Path(__file__).parent / "fixtures"


def test_parse_sample_pdf():
    """Parse sample.pdf and verify text-layer extraction."""
    content = (FIXTURES / "sample.pdf").read_bytes()
    segments = pdf_parser.parse(content)

    assert len(segments) >= 2

    # Page 1 segments
    page0 = [s for s in segments if s.context == "page:0"]
    assert len(page0) >= 1
    page0_texts = " ".join(s.text for s in page0)
    assert "Welcome to OpenLoc" in page0_texts

    # Page 2 segments
    page1 = [s for s in segments if s.context == "page:1"]
    assert len(page1) >= 1
    page1_texts = " ".join(s.text for s in page1)
    assert "Translation quality matters" in page1_texts


def test_parse_keys_include_page_and_block():
    """Keys should follow page:P:block:B format."""
    content = (FIXTURES / "sample.pdf").read_bytes()
    segments = pdf_parser.parse(content)

    for seg in segments:
        parts = seg.key.split(":")
        assert len(parts) == 4, f"Expected page:P:block:B, got {seg.key}"
        assert parts[0] == "page"
        assert parts[2] == "block"


def test_parse_positions_are_sequential():
    """Positions should be sequential starting from 0."""
    content = (FIXTURES / "sample.pdf").read_bytes()
    segments = pdf_parser.parse(content)

    for i, seg in enumerate(segments):
        assert seg.position == i


def test_parse_scanned_pdf_ocr_fallback():
    """Scanned PDF goes through the OCR fallback without raising."""
    content = (FIXTURES / "sample_scanned.pdf").read_bytes()
    segments = pdf_parser.parse(content)

    # OCR may not be installed in the test env, so this pins only that the
    # fallback path does not raise.
    assert isinstance(segments, list)


def _empty_pdf() -> bytes:
    doc = pdfium.PdfDocument.new()
    doc.new_page(595, 842)
    buf = io.BytesIO()
    doc.save(buf)
    return buf.getvalue()


def test_parse_empty_pdf():
    """Parsing a minimal PDF with no text does not raise."""
    segments = pdf_parser.parse(_empty_pdf())
    # Pins that an empty page does not raise.
    assert isinstance(segments, list)


def test_parse_without_tesseract_binary_yields_no_segments(monkeypatch):
    """OCR is optional: a missing tesseract binary behaves like a missing module."""
    import pytesseract

    def missing(*_args, **_kwargs):
        raise pytesseract.TesseractNotFoundError()

    monkeypatch.setattr(pytesseract, "image_to_string", missing)
    assert pdf_parser.parse(_empty_pdf()) == []


def _texts(name: str) -> list[str]:
    return [s.text for s in pdf_parser.parse((FIXTURES / name).read_bytes())]


def test_two_columns_read_column_by_column():
    """The left column is finished before the right one starts, even with a narrow gutter."""
    starts = [t.split()[0] for t in _texts("layout_two_column.pdf")]
    assert starts == ["Installation", "The", "Never", "Clean", "Le"]


def test_wrapped_paragraph_stays_one_segment():
    """A paragraph wrapped over several lines is one segment, accents intact."""
    texts = _texts("layout_two_column.pdf")
    assert texts[-1] == "Le réservoir doit être rempli avant la première\nutilisation, même en été."


def test_table_cells_are_separate_segments_in_row_order():
    texts = _texts("layout_table.pdf")
    assert texts[1:13] == [
        "Part", "Material", "Replace every",
        "Seal", "Rubber", "12 months",
        "Filter mesh", "Stainless steel", "24 months",
        "Valve", "Brass", "Never",
    ]


def test_header_and_footer_are_their_own_segments_per_page():
    segments = pdf_parser.parse((FIXTURES / "layout_header_footer.pdf").read_bytes())
    page1 = [s.text for s in segments if s.context == "page:1"]
    assert page1[0] == "Acme Pumps - Service manual"
    assert "Page 2" in page1
    assert any(t.startswith("Clean the filter every month.") for t in page1)


def _capture_ocr_images(monkeypatch) -> list:
    """Replaces tesseract with a stub that records each rendered page image."""
    import pytesseract

    images = []

    def capture(image, *_args, **_kwargs):
        images.append(image)
        return ""

    monkeypatch.setattr(pytesseract, "image_to_string", capture)
    return images


def test_ocr_renders_page_at_300_dpi(monkeypatch):
    """The scanned fixture is 192x96 pt, so 300 DPI gives an 800x400 image with ink on it."""
    images = _capture_ocr_images(monkeypatch)
    pdf_parser.parse((FIXTURES / "sample_scanned.pdf").read_bytes())

    assert [im.size for im in images] == [(800, 400)]
    darkest, _ = images[0].convert("L").getextrema()
    assert darkest < 128, "rendered page is blank"


def test_ocr_render_stays_within_pixel_budget(monkeypatch):
    """A page far larger than A4 is rendered at a lower scale, not at 300 DPI."""
    images = _capture_ocr_images(monkeypatch)
    doc = pdfium.PdfDocument.new()
    doc.new_page(3000, 3000)  # 300 DPI would be 12500x12500 px
    buf = io.BytesIO()
    doc.save(buf)

    pdf_parser.parse(buf.getvalue())

    assert len(images) == 1
    width, height = images[0].size
    assert width * height <= pdf_parser.OCR_MAX_PIXELS


def test_missing_tesseract_binary_is_logged(monkeypatch, caplog):
    import pytesseract

    def missing(*_args, **_kwargs):
        raise pytesseract.TesseractNotFoundError()

    monkeypatch.setattr(pytesseract, "image_to_string", missing)
    with caplog.at_level(logging.WARNING, logger=pdf_parser.__name__):
        pdf_parser.parse(_empty_pdf())

    assert [r.action for r in caplog.records] == ["pdf.ocr_unavailable"]


def test_missing_pytesseract_module_is_logged(monkeypatch, caplog):
    monkeypatch.setitem(sys.modules, "pytesseract", None)  # makes the import raise ImportError
    with caplog.at_level(logging.WARNING, logger=pdf_parser.__name__):
        assert pdf_parser.parse(_empty_pdf()) == []

    assert [r.action for r in caplog.records] == ["pdf.ocr_unavailable"]


@pytest.mark.skipif(shutil.which("tesseract") is None, reason="tesseract binary not installed")
def test_scanned_pdf_text_is_read_with_ocr():
    segments = pdf_parser.parse((FIXTURES / "sample_scanned.pdf").read_bytes())

    text = " ".join(s.text for s in segments)
    assert "Hello" in text
    assert "paragraph" in text
    assert {s.context for s in segments} == {"page:0"}
