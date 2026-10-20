FROM public.ecr.aws/docker/library/python:3.12.15-slim@sha256:02108f5d322dd89f1c9e552442c25acb0543dfdbc455693a5599624f20d9155d

# System deps: tesseract for OCR, curl for healthcheck
RUN apt-get update && apt-get install -y --no-install-recommends \
    tesseract-ocr curl && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Install into the system interpreter (no venv). --inexact keeps pip and the
# rest of the base image's packages, which uv.lock does not list.
ENV UV_PROJECT_ENVIRONMENT=/usr/local UV_COMPILE_BYTECODE=1 UV_LINK_MODE=copy

# Production deps only (no dev group). uv is mounted for this step only, so
# its binary, which the notices do not cover, is not in the image.
COPY sidecars/parser/pyproject.toml sidecars/parser/uv.lock ./
RUN --mount=type=bind,from=ghcr.io/astral-sh/uv:0.12.23@sha256:61d393e44e249f2e4b526b6c7ddcecce245946826e608e11c93ad4f5bba55b21,source=/uv,target=/bin/uv \
    --mount=type=cache,target=/root/.cache/uv \
    uv sync --frozen --inexact --no-dev

# License and third-party notices, from the Python packages installed above,
# so the file lists exactly what this image ships.
RUN --mount=type=bind,source=scripts/third-party-notices.sh,target=/notices.sh \
    { sh /notices.sh header openloc-docparser && sh /notices.sh py; } > THIRD_PARTY_NOTICES.md \
    && chmod 0644 THIRD_PARTY_NOTICES.md
COPY --chmod=0644 LICENSE ./

COPY sidecars/parser/ .

# Remove test files from prod image
RUN rm -rf tests/ __pycache__/ .pytest_cache/

# Non-root user
RUN groupadd -r app && useradd -r -g app app && chown -R app:app /app
USER app

EXPOSE 5200

# 2 workers + access log disabled for performance.
# Scale workers via UVICORN_WORKERS env var or k8s replicas.
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "5200", "--workers", "2", "--no-access-log"]
