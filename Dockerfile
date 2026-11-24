# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS builder

WORKDIR /src

RUN apk add --no-cache git

# Install swag CLI to generate OpenAPI docs at build time
RUN go install github.com/swaggo/swag/cmd/swag@v1.16.4

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN swag init -g cmd/server/main.go && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/ytdlp-api ./cmd/server

FROM alpine:3.21 AS runtime

RUN apk add --no-cache \
    ca-certificates \
    curl \
    deno \
    ffmpeg \
    python3 \
    py3-pip \
    tzdata && \
    addgroup -S appgroup && \
    adduser -S appuser -G appgroup && \
    mkdir -p /app/cookies /tmp/ytdlp-api && \
    chown -R appuser:appgroup /app /tmp/ytdlp-api

COPY requirements.txt /tmp/requirements.txt
RUN pip install --no-cache-dir --break-system-packages -r /tmp/requirements.txt && \
    rm -f /tmp/requirements.txt

WORKDIR /app

COPY --from=builder /out/ytdlp-api /usr/local/bin/ytdlp-api

USER appuser

ENV PORT=8080 \
    GIN_MODE=release \
    BASE_URL="" \
    LOG_LEVEL=info \
    YTDLP_PATH=yt-dlp \
    FFMPEG_PATH=ffmpeg \
    GALLERYDL_PATH=gallery-dl \
    PROXY_URL="" \
    COOKIES_FILE="" \
    COOKIES_DIR=/app/cookies \
    TEMP_DIR=/tmp/ytdlp-api \
    FILE_TTL=15m \
    CLEANUP_INTERVAL=5m \
    MAX_CONCURRENT_DOWNLOADS=4 \
    DOWNLOAD_TIMEOUT=5m \
    INFO_TIMEOUT=45s \
    MAX_FILESIZE=100M \
    MAX_PLAYLIST_ITEMS=10

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -fsS "http://localhost:${PORT:-8080}/healthz" || exit 1

ENTRYPOINT ["ytdlp-api"]
