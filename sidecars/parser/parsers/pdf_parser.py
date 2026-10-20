"""PDF parser: pdfminer.six for text-layer extraction, pytesseract for OCR fallback."""

from __future__ import annotations

import io
import logging
import math
from collections.abc import Iterator
from dataclasses import dataclass

import pypdfium2 as pdfium
from pdfminer.converter import PDFPageAggregator
from pdfminer.layout import LAParams, LTChar, LTComponent, LTTextContainer
from pdfminer.pdfinterp import PDFPageInterpreter, PDFResourceManager
from pdfminer.pdfpage import PDFPage

logger = logging.getLogger(__name__)

# Render scale for OCR: PDF user space is 72 points per inch, OCR wants 300 DPI.
OCR_SCALE = 300 / 72
# Upper bound on one rendered page (~A3 at 300 DPI is 17 MP). The page size comes
# from the uploaded file, so an oversized page is rendered at a lower scale.
OCR_MAX_PIXELS = 25_000_000


@dataclass
class Segment:
    key: str
    text: str
    position: int
    context: str  # "page:N"


def parse(content: bytes) -> list[Segment]:
    """Extract translatable segments from a PDF file.

    Strategy:
    1. Try text-layer extraction (accurate for digital PDFs).
    2. If no text found on any page, fall back to OCR via pytesseract.
    """
    segments = _extract_text_layer(content)
    if not segments:
        segments = _extract_via_ocr(content)
    return segments


class _StreamOrderAggregator(PDFPageAggregator):
    """Numbers every character in content-stream order before layout analysis.

    pdfminer orders text boxes by geometry, which interleaves narrow columns.
    The order the PDF draws its text is the author's reading order, so blocks
    are sorted by their first character's stream index instead.
    """

    def end_page(self, page: PDFPage) -> None:
        for index, char in enumerate(_chars(self.cur_item)):
            char.stream_index = index
        super().end_page(page)


def _chars(item: LTComponent) -> Iterator[LTChar]:
    for obj in item:
        if isinstance(obj, LTChar):
            yield obj
        elif hasattr(obj, "__iter__"):
            yield from _chars(obj)


def _first_stream_index(box: LTTextContainer) -> int:
    return min((c.stream_index for c in _chars(box)), default=0)


def _extract_text_layer(content: bytes) -> list[Segment]:
    """Extract text blocks from the PDF text layer, one segment per block."""
    resources = PDFResourceManager()
    # boxes_flow=None skips pdfminer's geometric ordering; blocks are re-sorted below.
    device = _StreamOrderAggregator(resources, laparams=LAParams(boxes_flow=None))
    interpreter = PDFPageInterpreter(resources, device)

    segments: list[Segment] = []
    pos = 0
    for page_num, page in enumerate(PDFPage.get_pages(io.BytesIO(content))):
        interpreter.process_page(page)
        boxes = [
            obj for obj in device.get_result()
            if isinstance(obj, LTTextContainer) and obj.get_text().strip()
        ]
        boxes.sort(key=_first_stream_index)
        for block_idx, box in enumerate(boxes):
            segments.append(Segment(
                key=f"page:{page_num}:block:{block_idx}",
                text=box.get_text().strip(),
                position=pos,
                context=f"page:{page_num}",
            ))
            pos += 1

    return segments


def _extract_via_ocr(content: bytes) -> list[Segment]:
    """Fall back to OCR when PDF has no text layer (scanned documents).

    OCR is optional: without pytesseract or the tesseract binary, a scanned PDF
    yields no segments. The production image installs both, so that case is
    logged as a broken deployment.
    """
    try:
        import pytesseract
    except ImportError:
        _log_ocr_unavailable("pytesseract module not installed")
        return []

    segments: list[Segment] = []
    pos = 0

    doc = pdfium.PdfDocument(content)
    try:
        for page_num, page in enumerate(doc):
            image = page.render(scale=_ocr_scale(*page.get_size())).to_pil()
            try:
                text = pytesseract.image_to_string(image).strip()
            except pytesseract.TesseractNotFoundError:
                _log_ocr_unavailable("tesseract binary not found")
                return []
            if not text:
                continue

            # One segment per blank-line-separated paragraph
            paragraphs = [p.strip() for p in text.split("\n\n") if p.strip()]
            for block_idx, para in enumerate(paragraphs):
                segments.append(Segment(
                    key=f"page:{page_num}:block:{block_idx}",
                    text=para,
                    position=pos,
                    context=f"page:{page_num}",
                ))
                pos += 1
    finally:
        doc.close()

    return segments


def _ocr_scale(width_pt: float, height_pt: float) -> float:
    """300 DPI, lowered so the rendered page stays within OCR_MAX_PIXELS."""
    pixels = width_pt * height_pt * OCR_SCALE ** 2
    if pixels <= OCR_MAX_PIXELS:
        return OCR_SCALE
    return OCR_SCALE * math.sqrt(OCR_MAX_PIXELS / pixels)


def _log_ocr_unavailable(reason: str) -> None:
    logger.warning(
        "OCR unavailable, scanned PDF yields no segments: %s", reason,
        extra={"action": "pdf.ocr_unavailable"},
    )
