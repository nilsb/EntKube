# ── Stage 1: Build frontend ────────────────────────────────────
FROM node:22-alpine AS frontend
WORKDIR /frontend
COPY frontend/package*.json ./
RUN npm ci --silent
COPY frontend/ ./
# Output to /dist/static so stage 2 can copy it cleanly
RUN npx vite build --outDir /dist/static --emptyOutDir

# ── Stage 2: Build backend ─────────────────────────────────────
FROM golang:1.24-alpine AS backend
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# Embed the pre-built SPA so the binary can serve it
COPY --from=frontend /dist/static ./static
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /server ./cmd/server

# ── Stage 3: Minimal runtime ───────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=backend /server /server
COPY --from=backend /src/static /static

# /data holds the per-node X25519 key pair — mount a named volume here
VOLUME ["/data"]

EXPOSE 8080

ENV STATIC_DIR=/static

ENTRYPOINT ["/server"]
