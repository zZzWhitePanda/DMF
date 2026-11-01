# Multi-stage build:
#   1. node builder compiles the Next.js dashboard to a static export
#      inside /app/dashboard/out, which we copy into the Go build context.
#   2. go builder compiles the server with the dashboard-build/ embedded.
#   3. final image is a tiny distroless base running the single binary.
#
# Deployed to Fly.io via `fly deploy`. See fly.toml for runtime config.

# -----------------------------------------------------------------------------
# Stage 1: dashboard
# -----------------------------------------------------------------------------
FROM node:22-alpine AS dashboard
WORKDIR /app/dashboard
COPY dashboard/package.json dashboard/package-lock.json* ./
RUN npm ci --no-audit --no-fund
COPY dashboard/ .
RUN npm run build

# -----------------------------------------------------------------------------
# Stage 2: Go server (embeds dashboard output)
# -----------------------------------------------------------------------------
FROM golang:1.26-alpine AS server
WORKDIR /src
# Prime module cache.
COPY backend/go.mod backend/go.sum* ./
RUN go mod download || true
COPY backend/ .
# Replace the placeholder dashboard-build with the real one from the
# Next.js static export. The embed directive in internal/static/
# dashboard.go reads from this exact path at build time.
RUN rm -rf internal/static/dashboard-build
COPY --from=dashboard /app/dashboard/out ./internal/static/dashboard-build
# Build the server binary. CGO_ENABLED=0 because modernc.org/sqlite is pure-Go.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/dmf ./cmd/server

# -----------------------------------------------------------------------------
# Stage 3: runtime
# -----------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12 AS runtime
WORKDIR /app
COPY --from=server /out/dmf /app/dmf
# Fly volume mounts at /data; database lives there so it survives
# deploys. DATABASE_PATH env var is set in fly.toml.
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/dmf"]
