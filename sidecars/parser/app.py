"""FastAPI entrypoint for the parser sidecar.

Provides /parse/docx, /parse/pdf, /reconstruct/docx, and /health endpoints.
"""

import json

from fastapi import FastAPI, File, Form, UploadFile
from fastapi.responses import JSONResponse, Response

from parsers import docx_parser, pdf_parser

app = FastAPI(title="OpenLoc Parser Sidecar", version="1.0.0")


@app.get("/health")
async def health():
    return {"status": "ok"}


@app.post("/parse/docx")
async def parse_docx(file: UploadFile = File(...)):
    content = await file.read()
    try:
        segments = docx_parser.parse(content)
    except Exception as e:
        return JSONResponse(status_code=400, content={"error": f"Failed to parse DOCX: {e}"})

    return {
        "segments": [
            {"key": s.key, "text": s.text, "position": s.position, "context": s.context, "rich_text": s.rich_text}
            for s in segments
        ]
    }


@app.post("/parse/pdf")
async def parse_pdf(file: UploadFile = File(...)):
    content = await file.read()
    try:
        segments = pdf_parser.parse(content)
    except Exception as e:
        return JSONResponse(status_code=400, content={"error": f"Failed to parse PDF: {e}"})

    return {
        "segments": [
            {"key": s.key, "text": s.text, "position": s.position, "context": s.context}
            for s in segments
        ]
    }


@app.post("/reconstruct/docx")
async def reconstruct_docx(
    file: UploadFile = File(...),
    translations: str = Form(...),
):
    content = await file.read()
    try:
        trans_map = json.loads(translations)
    except json.JSONDecodeError as e:
        return JSONResponse(status_code=400, content={"error": f"Invalid translations JSON: {e}"})

    try:
        result = docx_parser.reconstruct(content, trans_map)
    except Exception as e:
        return JSONResponse(status_code=400, content={"error": f"Failed to reconstruct DOCX: {e}"})

    return Response(
        content=result,
        media_type="application/vnd.openxmlformats-officedocument.wordprocessingml.document",
        headers={"Content-Disposition": "attachment; filename=translated.docx"},
    )
