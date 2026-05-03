# ============================================================
# Stage 1: Build the dragonscale binary
# ============================================================
FROM golang:1.26.2-bookworm AS builder

RUN apt-get update \
    && apt-get install -y --no-install-recommends build-essential ca-certificates git make \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=1 GOOS=linux make build

# ============================================================
# Stage 2: Minimal runtime image
# ============================================================
FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl tzdata \
    && rm -rf /var/lib/apt/lists/*

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD curl -fsS http://localhost:18790/health || exit 1

# Copy binary
COPY --from=builder /src/bin/dragonscale /usr/local/bin/dragonscale

# Create non-root user and group
RUN groupadd --gid 1000 dragonscale && \
    useradd --uid 1000 --gid 1000 --create-home --shell /usr/sbin/nologin dragonscale && \
    install -d -o dragonscale -g dragonscale /home/dragonscale/.config/dragonscale /home/dragonscale/.local/share/dragonscale /home/dragonscale/.cache

# Switch to non-root user
USER dragonscale
ENV XDG_CONFIG_HOME=/home/dragonscale/.config \
    XDG_DATA_HOME=/home/dragonscale/.local/share \
    XDG_CACHE_HOME=/home/dragonscale/.cache

ENTRYPOINT ["dragonscale"]
CMD ["gateway"]
