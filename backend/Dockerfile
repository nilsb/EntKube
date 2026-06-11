# backend/Dockerfile — backend binary only (no SPA assets).
# For a complete image that includes the built frontend, use the root Dockerfile
# which adds a Node build stage and copies the static output.
#
# Build stage ──────────────────────────────────────────────────
FROM golang:1.24-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /server ./cmd/server

# ── Runtime stage ───────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /server /server

# /data is where the node key file is persisted (mount a volume here)
VOLUME ["/data"]

EXPOSE 8080

ENTRYPOINT ["/server"]
