# syntax=docker/dockerfile:1.7
# Multi-stage build for memory-allocator. Final image is a static binary
# on distroless — no shell, no package manager, ~15MB total.
#
# Build:    docker build -t memory-allocator:latest .
# Run:      docker run --rm -p 8090:8090 memory-allocator:latest
# Verify:   curl http://localhost:8090/health

# ---------- Stage 1: build ----------
FROM golang:1.26-alpine AS build

WORKDIR /src

# Cache go.mod/go.sum separately for faster incremental builds.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy the rest of the source.
COPY . .

# Build a static, stripped, versioned binary.
# CGO_ENABLED=0 → static binary, runs in scratch/distroless.
# -trimpath → reproducible builds, no local paths in binary.
# -ldflags "-s -w" → strip debug info, smaller binary.
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -trimpath \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o /out/memory-allocator \
        ./cmd/server

# ---------- Stage 2: runtime ----------
# Distroless static: just glibc / ca-certificates / tzdata, no shell.
# ~2MB base, no package manager, no shell injection surface.
FROM gcr.io/distroless/static-debian12:nonroot

# OCI labels.
LABEL org.opencontainers.image.title="memory-allocator" \
      org.opencontainers.image.description="Interactive memory-allocator playground with WebSocket UI" \
      org.opencontainers.image.source="https://github.com/sanskar/memory-allocator" \
      org.opencontainers.image.licenses="MIT"

# Static assets are baked in by an init container at deploy time, or
# served from a sidecar; the binary itself is a single file. The
# web/static directory is read from the working directory at runtime,
# so we set it to /var/lib/memory-allocator/web/static by convention.
WORKDIR /var/lib/memory-allocator
COPY --from=build /out/memory-allocator /usr/local/bin/memory-allocator
COPY web/static ./web/static

# Distroless nonroot runs as UID 65532. The image is read-only.
USER nonroot:nonroot

# Default port. Override with MEMALLOC_PORT env var.
ENV MEMALLOC_PORT=8090
ENV MEMALLOC_HOST=0.0.0.0
ENV MEMALLOC_STATIC_DIR=/var/lib/memory-allocator/web/static
EXPOSE 8090

# Health checks must be supplied by the orchestrator (Docker Compose,
# Kubernetes). The /health endpoint on the server returns 200 OK with
# a JSON body; a typical check is:
#   wget -qO- http://localhost:8090/health >/dev/null || exit 1
# Distroless images don't ship wget, so we don't define HEALTHCHECK
# here — let the orchestrator handle it.

ENTRYPOINT ["/usr/local/bin/memory-allocator"]
