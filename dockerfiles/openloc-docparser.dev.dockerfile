FROM python:3.12.15-slim@sha256:02108f5d322dd89f1c9e552442c25acb0543dfdbc455693a5599624f20d9155d

COPY --from=ghcr.io/astral-sh/uv:0.12.23@sha256:61d393e44e249f2e4b526b6c7ddcecce245946826e608e11c93ad4f5bba55b21 /uv /bin/uv

# System deps only — these rarely change, so this image rarely needs rebuilding.
# Python packages are installed at startup via the CMD (persisted in a volume).
RUN apt-get update && apt-get install -y --no-install-recommends \
    tesseract-ocr curl && rm -rf /var/lib/apt/lists/*

WORKDIR /app
EXPOSE 5200

# Sync into the system interpreter, whose site-packages is the parser-pip volume.
# --inexact keeps the base image's own packages, which uv.lock does not list.
ENV UV_PROJECT_ENVIRONMENT=/usr/local UV_LINK_MODE=copy

# Sync deps on startup (fast if nothing changed), then run uvicorn with reload.
# `python -m uvicorn`, not the `uvicorn` script: packages live in the parser-pip
# volume (site-packages), but console scripts go to /usr/local/bin, which a
# recreated container loses while uv sees the packages as already installed.
CMD uv sync --frozen --inexact && exec python -m uvicorn app:app --host 0.0.0.0 --port 5200 --reload
