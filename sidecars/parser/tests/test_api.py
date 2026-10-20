"""Integration tests for the FastAPI endpoints."""

import json
from pathlib import Path

from httpx import AsyncClient
import pytest

from app import app

FIXTURES = Path(__file__).parent / "fixtures"


@pytest.fixture
def client():
    """Create a test client."""
    from httpx import ASGITransport
    transport = ASGITransport(app=app)
    return AsyncClient(transport=transport, base_url="http://test")


@pytest.mark.anyio
async def test_health(client):
    resp = await client.get("/health")
    assert resp.status_code == 200
    assert resp.json() == {"status": "ok"}


@pytest.mark.anyio
async def test_parse_docx(client):
    content = (FIXTURES / "sample.docx").read_bytes()
    resp = await client.post("/parse/docx", files={"file": ("test.docx", content)})
    assert resp.status_code == 200

    data = resp.json()
    segments = data["segments"]
    assert len(segments) == 9
    assert segments[0]["key"] == "para:0"
    assert segments[0]["text"] == "Welcome to OpenLoc"
    assert segments[0]["context"] == "body"


@pytest.mark.anyio
async def test_parse_pdf(client):
    content = (FIXTURES / "sample.pdf").read_bytes()
    resp = await client.post("/parse/pdf", files={"file": ("test.pdf", content)})
    assert resp.status_code == 200

    data = resp.json()
    segments = data["segments"]
    assert len(segments) >= 2
    assert "page:0:block:0" == segments[0]["key"]


@pytest.mark.anyio
async def test_reconstruct_docx(client):
    content = (FIXTURES / "sample.docx").read_bytes()
    translations = json.dumps({"para:0": "Hola Mundo"})

    resp = await client.post(
        "/reconstruct/docx",
        files={"file": ("test.docx", content)},
        data={"translations": translations},
    )
    assert resp.status_code == 200
    assert resp.headers["content-type"] == "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

    from parsers import docx_parser
    segments = docx_parser.parse(resp.content)
    assert segments[0].text == "Hola Mundo"


@pytest.mark.anyio
async def test_reconstruct_docx_invalid_json(client):
    content = (FIXTURES / "sample.docx").read_bytes()
    resp = await client.post(
        "/reconstruct/docx",
        files={"file": ("test.docx", content)},
        data={"translations": "not-json"},
    )
    assert resp.status_code == 400
    assert "Invalid translations JSON" in resp.json()["error"]
