# Production multi-stage build for the single `prexel` binary.
# Stage 1 builds the Vue frontend → web/dist.
# Stage 2 builds the Go binary with web/dist embedded via go:embed.
# Final stage is distroless/nonroot containing only the binary.

# --- Stage 1: build frontend ---------------------------------------------------
FROM node:26-alpine AS web-builder
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
# `npm ci` is strict: fails if package-lock.json is out of sync with package.json,
# which is exactly what we want in CI / prod images. Use `npm install` locally to
# regenerate the lockfile, then commit it.
RUN npm ci
COPY web/ ./
RUN npm run build

# --- Stage 2: build backend with embedded frontend ----------------------------
FROM golang:1.25-alpine AS go-builder
ENV CGO_ENABLED=0
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
# Drop the dev placeholder web/dist (if present) and copy the real build.
RUN rm -rf web/dist
COPY --from=web-builder /web/dist ./web/dist
RUN go build -trimpath -ldflags="-s -w" -o /out/prexel .

# --- Stage 3: minimal runtime image -------------------------------------------
FROM gcr.io/distroless/base-debian12:nonroot
COPY --from=go-builder /out/prexel /prexel
EXPOSE 3000
# Distroless `nonroot` ships uid/gid 65532. Setting it explicitly here is
# belt-and-suspenders: it survives image flattening / re-tagging and makes the
# non-root posture obvious to anyone inspecting the image.
USER 65532:65532
# The `prexel healthcheck` subcommand hits https://127.0.0.1:$PREXEL_PORT/api/v1/healthz
# (self-signed cert accepted on loopback). Implemented in another PR — relies on
# it being shipped in the same release.
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD ["/prexel", "healthcheck"]
ENTRYPOINT ["/prexel"]
CMD ["serve"]
