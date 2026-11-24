# yt-dlp Media Downloader & Search API

A lightweight **Go + Gin** REST API wrapping [`yt-dlp`](https://github.com/yt-dlp/yt-dlp), [`gallery-dl`](https://github.com/mikf/gallery-dl), and `ffmpeg` for extracting metadata, searching tracks/videos, and auto-downloading videos, audio, photos, and carousels across major platforms. Designed for private/internal Docker deployments without authentication.

> **API Reference:** See [API.md](API.md) for endpoints and examples, or open `/swagger/index.html` when running.

---

## Supported Platforms

| Platform | Features | Endpoints |
| :--- | :--- | :--- |
| **YouTube** | Video, Audio, Search | `/api/v1/youtube/{search,video,audio}` |
| **TikTok** | Video, Audio, Carousel | `/api/v1/tiktok/download` |
| **Instagram** | Reels, Videos, Photos, Carousels, Stories | `/api/v1/instagram/download` |
| **Threads** | Video, Image, Carousel | `/api/v1/threads/download` |
| **X (Twitter)** | Video, GIF, Images, Multi-item | `/api/v1/twitter/download` |
| **SoundCloud** | Search, Audio-only | `/api/v1/soundcloud/{search,download}` |
| **Facebook** | Reels, Watch, Public Video/Image | `/api/v1/facebook/download` |
| **Universal** | Auto-detect + [1,000+ `yt-dlp`](https://github.com/yt-dlp/yt-dlp/blob/master/supportedsites.md) / [`gallery-dl`](https://github.com/mikf/gallery-dl/blob/master/docs/supportedsites.md) sites | `/api/v1/{info,download,search}` |

---

## Quick Start

### Docker Compose

```bash
cp .env.example .env
docker compose up -d --build
```

### Prebuilt Image (GHCR)

```bash
docker pull ghcr.io/justpiple/ytdlp-api:latest
docker run -d --name ytdlp-api \
  -p 8080:8080 \
  -v ./cookies:/app/cookies:ro \
  ghcr.io/justpiple/ytdlp-api:latest
```

### Local Development

Requires **Go 1.26+**, `yt-dlp`, `gallery-dl`, `ffmpeg`, and `deno`.

```bash
make swag        # Regenerate Swagger specs in docs/
make test-race   # Run tests with -race and -shuffle=on
make run         # Start server on :8080
```

---

## Configuration

Configure via `.env` or environment variables (see [`.env.example`](.env.example)). All variables include `${VAR:-default}` bindings in [`docker-compose.yml`](docker-compose.yml) so PaaS tools like Coolify auto-populate defaults for customization:

| Variable | Default | Description |
| :--- | :--- | :--- |
| `PORT` | `8080` | HTTP listening port |
| `GIN_MODE` | `release` | Gin mode (`debug`, `release`, `test`) |
| `BASE_URL` | `""` | Base URL for generated `download_url` links (auto-detected if empty) |
| `LOG_LEVEL` | `info` | JSON log level (`debug`, `info`, `warn`, `error`) |
| `PROXY_URL` | `""` | Default upstream proxy (`http://...` or `socks5://...`) |
| `YTDLP_PATH` | `yt-dlp` | Path or command name for `yt-dlp` |
| `GALLERYDL_PATH` | `gallery-dl` | Path or command name for `gallery-dl` |
| `FFMPEG_PATH` | `ffmpeg` | Path or command name for `ffmpeg` |
| `COOKIES_FILE` | `""` | Fallback global Netscape `cookies.txt` path |
| `COOKIES_DIR` | `./cookies` | Directory for per-platform cookies (e.g. `instagram.txt`, `youtube.txt`) |
| `TEMP_DIR` | `/tmp/ytdlp-api` | Temporary storage path for downloaded files |
| `FILE_TTL` | `15m` | TTL before hosted temporary files expire |
| `CLEANUP_INTERVAL` | `5m` | Cleanup interval for expired temporary files |
| `MAX_CONCURRENT_DOWNLOADS` | `4` | Max concurrent `yt-dlp`/`ffmpeg` processes |
| `DOWNLOAD_TIMEOUT` | `5m` | Per-download timeout |
| `INFO_TIMEOUT` | `45s` | Metadata & search timeout |
| `MAX_FILESIZE` | `100M` | Default `--max-filesize` limit |
| `MAX_PLAYLIST_ITEMS` | `10` | Max items downloaded for carousel/playlist URLs |

---

## Cookies & Proxy

- **Per-platform cookies:** Place Netscape-format cookie files in `./cookies/` (e.g. `instagram.txt`, `youtube.txt`, `twitter.txt`, `threads.txt`, `facebook.txt`, `tiktok.txt`).
- **Global cookies:** Set `COOKIES_FILE` as a fallback.
- **Proxy:** Set `PROXY_URL` globally, or pass `"proxy"` in individual API requests.

---

## CI/CD

- [`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs `go vet`, `go test -race -shuffle=on`, builds the Docker image, verifies `/healthz` & `/swagger/index.html` via a container smoke test, and pushes to `ghcr.io/justpiple/ytdlp-api`.
- [`.github/dependabot.yml`](.github/dependabot.yml) tracks [`requirements.txt`](requirements.txt) daily and opens PRs for new `yt-dlp` releases.
