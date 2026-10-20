"""Generates the layout fixtures used by tests/test_pdf.py.

Run from sidecars/parser: python tests/fixtures/make_layout_pdfs.py
The PDFs are checked in; rerun only when a fixture needs to change.
"""

from pathlib import Path

from reportlab.lib.pagesizes import A4
from reportlab.lib.styles import getSampleStyleSheet
from reportlab.platypus import (
    Frame,
    PageTemplate,
    Paragraph,
    BaseDocTemplate,
    FrameBreak,
    PageBreak,
    Spacer,
    Table,
    TableStyle,
)
from reportlab.lib import colors

OUT = Path(__file__).parent
STYLES = getSampleStyleSheet()
BODY = STYLES["BodyText"]

LONG_A = (
    "The pump must be primed before the first run. Fill the housing with clean "
    "water up to the marked line, close the valve and wait until the pressure "
    "gauge reads between two and three bar."
)
LONG_B = (
    "Never run the pump dry. Running without water for more than thirty seconds "
    "damages the seal and voids the warranty for the whole unit."
)
LONG_C = (
    "Clean the filter every month. Remove the cover, rinse the mesh under running "
    "water and put it back with the arrow pointing towards the motor."
)
LONG_D = "Le réservoir doit être rempli avant la première utilisation, même en été."


def two_column() -> None:
    doc = BaseDocTemplate(str(OUT / "layout_two_column.pdf"), pagesize=A4)
    w, h = A4
    gap, margin = 20, 50
    col = (w - 2 * margin - gap) / 2
    title = Frame(margin, h - 110, w - 2 * margin, 60, id="title")
    left = Frame(margin, margin, col, h - 170, id="left")
    right = Frame(margin + col + gap, margin, col, h - 170, id="right")
    doc.addPageTemplates([PageTemplate(frames=[title, left, right])])
    story = [
        Paragraph("Installation guide", STYLES["Title"]),
        FrameBreak(),
        Paragraph(LONG_A, BODY),
        Spacer(1, 12),
        Paragraph(LONG_B, BODY),
        FrameBreak(),
        Paragraph(LONG_C, BODY),
        Spacer(1, 12),
        Paragraph(LONG_D, BODY),
    ]
    doc.build(story)


def table() -> None:
    doc = BaseDocTemplate(str(OUT / "layout_table.pdf"), pagesize=A4)
    w, h = A4
    doc.addPageTemplates([PageTemplate(frames=[Frame(50, 50, w - 100, h - 100)])])
    data = [
        ["Part", "Material", "Replace every"],
        ["Seal", "Rubber", "12 months"],
        ["Filter mesh", "Stainless steel", "24 months"],
        ["Valve", "Brass", "Never"],
    ]
    t = Table(data, colWidths=[140, 140, 140])
    t.setStyle(TableStyle([("GRID", (0, 0), (-1, -1), 0.5, colors.black)]))
    story = [Paragraph("Spare parts", STYLES["Heading1"]), t, Spacer(1, 20), Paragraph(LONG_B, BODY)]
    doc.build(story)


def header_footer() -> None:
    def decorate(canvas, doc):
        w, h = A4
        canvas.setFont("Helvetica", 9)
        canvas.drawString(50, h - 30, "Acme Pumps - Service manual")
        canvas.drawRightString(w - 50, 25, f"Page {doc.page}")

    doc = BaseDocTemplate(str(OUT / "layout_header_footer.pdf"), pagesize=A4)
    w, h = A4
    doc.addPageTemplates([PageTemplate(frames=[Frame(50, 60, w - 100, h - 130)], onPage=decorate)])
    story = [Paragraph(LONG_A, BODY), Spacer(1, 12), Paragraph(LONG_B, BODY), PageBreak(), Paragraph(LONG_C, BODY)]
    doc.build(story)


if __name__ == "__main__":
    two_column()
    table()
    header_footer()
