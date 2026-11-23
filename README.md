# yt-dlp Media Downloader & Search API (Go + Gin)

Internal REST API berbasis **Go + Gin** yang membungkus `yt-dlp` dan `ffmpeg` untuk mengunduh media serta melakukan pencarian dari berbagai platform:

- **YouTube** (`!ytmp3`, `!ytmp4`, `!ytsearch`, `!play`)
- **TikTok** (video watermark-free, audio, multi-slide)
- **Instagram** (Reels, Post, Carousel, Stories)
- **Threads** (Video, Carousel)
- **X (Twitter)** (Video, GIF, Multi-video)
- **SoundCloud** (Search & Download audio)
- **Facebook** (Reels, Watch, Public Video)
- **Universal** (1000+ situs dari `yt-dlp`)

Dirancang khusus untuk integrasi **Bot WhatsApp** dan deployment **Docker** tanpa autentikasi (internal/private network).

---

## Fitur Utama

- **Hybrid 3-Mode Response (`response_mode`)**:
  - `"url"` *(default)*: Synchronous JSON berisi metadata lengkap + `download_url` lokal (`/api/v1/files/:id`). Ideal untuk 90% command bot WhatsApp (`!ytmp3`, `!tiktok`, `!ig`, `!sc`) karena `yt-dlp` hanya berjalan 1x dan mendukung multi-file/carousel.
  - `"async"`: Mengembalikan `202 Accepted` + `job_id`, endpoint polling (`GET /api/v1/jobs/:id`), dan **SSE real-time progress** (`GET /api/v1/jobs/:id/events`) untuk video panjang/resolusi tinggi.
  - `"stream"`: Langsung mengirimkan binary file (`Content-Disposition: attachment`).
- **WhatsApp Codec Optimization (`whatsapp_compatible: true`)**: Memprioritaskan stream **H.264 (`avc1`) + AAC (`m4a`)** MP4 agar video dapat diputar langsung di WhatsApp Android/iOS/Web tanpa layar hitam.
- **Auto-Cleanup (`?delete_after=true` + TTL Worker)**: File sementara otomatis terhapus setelah diambil bot atau setelah TTL kedaluwarsa.
- **Proxy & Cookies Support**: Mendukung `PROXY_URL` di `.env`, override `proxy` per request, serta file cookies global (`COOKIES_FILE`) maupun per-platform (`cookies/instagram.txt`, `cookies/youtube.txt`, dll).
- **Concurrency Limiter**: Membatasi jumlah proses `yt-dlp`/`ffmpeg` paralel (`MAX_CONCURRENT_DOWNLOADS`) agar container aman dari OOM.
- **Swagger UI**: Dokumentasi interaktif di `/swagger/index.html`.

---

## Menjalankan dengan Docker Compose

1. Salin file contoh konfigurasi environment:
   ```bash
   cp .env.example .env
   ```
2. *(Opsional)* Atur `PROXY_URL` di `.env` atau letakkan file cookies Netscape format ke folder `cookies/` (mis. `cookies/instagram.txt`, `cookies/youtube.txt`).
3. Jalankan container:
   ```bash
   docker compose up -d --build
   ```
4. Buka Swagger UI di `http://localhost:8080/swagger/index.html` atau cek healthcheck:
   ```bash
   curl http://localhost:8080/healthz
   ```

---

## Contoh Penggunaan untuk Bot WhatsApp

### 1. Command `!play` / `!ytmp3` (URL atau Kata Kunci)
```bash
curl -X POST http://localhost:8080/api/v1/youtube/audio \
  -H "Content-Type: application/json" \
  -d '{
    "url": "dewa 19 kangen",
    "audio_format": "mp3",
    "is_search": true
  }'
```
Kemudian bot mengambil file dengan `?delete_after=true`:
```bash
curl -o song.mp3 "http://localhost:8080/api/v1/files/<file_id>?delete_after=true"
```

### 2. Command `!tiktok` / `!ig` / `!threads` / `!x` / `!fb` (Auto-detect via Universal Endpoint)
```bash
curl -X POST http://localhost:8080/api/v1/download \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://www.instagram.com/reel/C0000000000/",
    "quality": "720p",
    "whatsapp_compatible": true
  }'
```

### 3. Command `!ytmp4` Durasi Panjang dengan Live SSE Progress
1. Mulai job async:
   ```bash
   curl -X POST http://localhost:8080/api/v1/youtube/video \
     -H "Content-Type: application/json" \
     -d '{
       "url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
       "quality": "1080p",
       "response_mode": "async"
     }'
   ```
2. Subscribe progress melalui SSE:
   ```bash
   curl -N http://localhost:8080/api/v1/jobs/<job_id>/events
   ```

---

## Pengembangan Lokal

```bash
make swag        # Generate Swagger docs
make test-race   # Jalankan seluruh unit test dengan race detector
make vet         # Static analysis
make build       # Build binary ke bin/ytdlp-api
```

---

## GitHub Actions CI/CD, GHCR & Webhook Notifikasi

Repository ini menggunakan 1 workflow ringkas di [`.github/workflows/ci.yml`](.github/workflows/ci.yml):
- **`test`**: Menjalankan `go vet ./...` dan `go test -race -shuffle=on ./...` pada Go `1.26`.
- **`docker`**: Berjalan setelah `test` lulus — otomatis mengambil versi `yt-dlp` terbaru dari PyPI (`YTDLP_VERSION`), mem-build image Docker (dengan GHA cache agar layer `apk`/`ffmpeg`/`deno` tidak dibangun ulang kalau tidak berubah), lalu push ke **GitHub Container Registry (`ghcr.io/<owner>/<repo>:latest`)**.
- **Auto-refresh mingguan**: Cron setiap Minggu `03:00 UTC` mengecek & membungkus rilis `yt-dlp` terbaru.

### Mengaktifkan Webhook Notifikasi Build
Tambahkan repository secret di **GitHub → Settings → Secrets and variables → Actions → New repository secret**:
- Name: `BUILD_WEBHOOK_URL`
- Value: URL webhook penerima (endpoint Bot WhatsApp, Discord, Telegram, atau ntfy.sh)

Setiap kali workflow selesai (baik `success` maupun `failure`), GitHub akan mengirim payload JSON berisi `repo`, `event`, `status`, `branch`, `sha`, `ytdlp_version`, `tags`, `digest`, dan `run_url`.

### Pull Image dari GHCR di Server
```bash
docker pull ghcr.io/<username>/ytdlp-api:latest
```
