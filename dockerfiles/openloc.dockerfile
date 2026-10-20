# The two build stages run on the build machine's own platform: the frontend
# output is the same for every platform, and Go cross-compiles for the target.
# Only the runtime stage is built per target, so an arm64 image needs no
# emulated npm or go build.

# ---- Stage 1: Build React frontend ----
FROM --platform=$BUILDPLATFORM public.ecr.aws/docker/library/node:24.21.0-alpine@sha256:ebfe2f90462722a7a4de65e91990e97fe0d401c70e0e762c5b53302f905ec1c1 AS frontend

WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --ignore-scripts

# The license notices of the production packages npm ci installed, for the
# runtime stage. web/licenses holds the texts of packages that ship none.
COPY web/licenses ./licenses
RUN --mount=type=bind,source=scripts/third-party-notices.sh,target=/notices.sh \
    sh /notices.sh npm > /app/notices-npm.md

# Declared after npm ci so changing them never busts the dependency layer.
# GIT_HASH must equal the VERSION the server runs with: Honeybadger applies the
# uploaded sourcemaps only when the two revisions match. HONEYBADGER_API_KEY enables the upload; when either is missing
# (local builds) vite skips the upload entirely.
ARG GIT_HASH
ARG HONEYBADGER_API_KEY

COPY web/ ./
# find -delete is load-bearing: sourcemap 'hidden' still writes .map files
# into dist/, and everything in dist/ is served publicly by the Go binary.
# Upload to Honeybadger, then strip the maps from the image.
RUN VERSION=$GIT_HASH HONEYBADGER_API_KEY=$HONEYBADGER_API_KEY npm run build \
    && find dist -name '*.map' -delete

# ---- Stage 2: Build Go backend ----
FROM --platform=$BUILDPLATFORM public.ecr.aws/docker/library/golang:1.26.9-alpine@sha256:cdfd4fe2da6b225d8b40c6b7a105736e548e83ff56d5d8f9394446eeb5eb84e0 AS backend
ARG TARGETOS TARGETARCH

RUN apk add --no-cache git

WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Copy source (excluding web/ — already built above)
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY pkg/ ./pkg/

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /server ./cmd/server

# The license notices of the modules linked into that binary (same target, so
# the same module set), for the runtime stage.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=bind,source=scripts/third-party-notices.sh,target=/notices.sh \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH sh /notices.sh go > /app/notices-go.md

# ---- Stage 3: Minimal runtime ----
FROM public.ecr.aws/docker/library/alpine:3.21.8@sha256:ce64758a109eb420d874a118f87920e625e12d3634e03b4a5573fd9f6e5d3507

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app && adduser -S app -G app

WORKDIR /app

# Go binary
COPY --from=backend /server ./server

# React SPA (served by Go when STATIC_DIR is set)
COPY --from=frontend /app/web/dist ./static

# Database migrations, also embedded in the binary (`./server migrate`)
COPY --from=backend /app/internal/database/migrations ./migrations

# License and third-party notices: the two build stages' sections behind one
# header, so the file lists exactly what this image ships.
RUN --mount=type=bind,source=scripts/third-party-notices.sh,target=/notices.sh \
    --mount=type=bind,from=backend,source=/app/notices-go.md,target=/notices-go.md \
    --mount=type=bind,from=frontend,source=/app/notices-npm.md,target=/notices-npm.md \
    { sh /notices.sh header openloc && cat /notices-go.md /notices-npm.md; } > THIRD_PARTY_NOTICES.md \
    && chmod 0644 THIRD_PARTY_NOTICES.md
COPY --chmod=0644 LICENSE ./

RUN chown -R app:app /app

USER app

ENV STATIC_DIR=/app/static
EXPOSE 8080

CMD ["./server"]
