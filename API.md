# API Documentation

REST API reference for **yt-dlp Media Downloader & Search API**.

- **Base URL:** `http://<host>:8080/api/v1` (`/healthz` and `/swagger/index.html` are also mounted at `/`)
- **Auth:** None (internal use)
- **Swagger UI:** `GET /swagger/index.html`

---

## Response Format

```json
// Success
{ "success": true, "message": "ok", "data": {} }

// Error
{ "success": false, "error": { "code": "INVALID_URL", "message": "ytdlp: invalid url: empty url" } }
```

### Error Codes

| Status | Code | Description |
| :--- | :--- | :--- |
| `400` | `INVALID_REQUEST` | Malformed JSON, missing field, or invalid parameter |
| `400` | `INVALID_URL` | Invalid or non-`http`/`https` URL |
| `400` | `UNSUPPORTED_PLATFORM` | URL does not match the platform endpoint or search is unsupported |
| `404` | `NOT_FOUND` | Media unavailable, no results, expired `file_id`, or unknown `job_id` |
| `413` | `MEDIA_TOO_LARGE` | Media exceeds `MAX_FILESIZE` / `max_filesize` |
| `502` | `UPSTREAM_BLOCKED` | Upstream blocked request (`403`/`429`), private content, or cookies required |
| `504` | `TIMEOUT` | Request exceeded `INFO_TIMEOUT` or `DOWNLOAD_TIMEOUT` |
| `500` | `INTERNAL_ERROR` | Unexpected execution or storage failure |

---

## Delivery Modes (`response_mode`)

All `POST` download endpoints support `response_mode`:

| Mode | Status | Behavior |
| :--- | :--- | :--- |
| `"url"` *(default)* | `200 OK` | Downloads to temporary storage and returns `metadata`, `media_type`, `count`, and `files[]` with `download_url`. |
| `"async"` | `202 Accepted` | Enqueues background job and returns `job_id`, `status_url`, and `sse_url`. |

---

## 1. System

- **`GET /healthz`** *(also `GET /api/v1/healthz`)* — Returns service status, `ytdlp_version`, `gallerydl_version`, `ffmpeg_version`, `active_jobs`, `max_concurrent`, and `stored_files`. Returns `503` if binaries are unavailable.
- **`GET /api/v1/platforms`** — Lists supported platforms, enabled features, and expected cookie filenames.

---

## 2. Unified Endpoints

### `POST /api/v1/info`

Extracts metadata and optional format list without downloading media files.

| Field | Type | Required | Default | Description |
| :--- | :--- | :--- | :--- | :--- |
| `url` | `string` | Yes | — | Media URL |
| `include_formats` | `bool` | No | `false` | Include available stream formats and `direct_url` values |
| `proxy` | `string` | No | `""` | Optional proxy override |

### `POST /api/v1/download`

Auto-detects platform and media type (`video`, `audio`, `image`, or `carousel`) from `url` for non-YouTube sources. YouTube defaults to `"video"` unless `type: "audio"` is specified; SoundCloud always stays `"audio"`. Short/share links (`vt.tiktok.com`, `fb.watch`, `t.co`, `instagram.com/share/`, `pin.it`, `threads.com/t/`) are automatically resolved before downloading.

| Field | Type | Required | Default | Description |
| :--- | :--- | :--- | :--- | :--- |
| `url` | `string` | Yes | — | Media URL (or search query if `is_search: true`) |
| `type` | `string` | No | `"auto"` | `"auto"` (non-YouTube), `"video"`, or `"audio"` |
| `quality` | `string` | No | `"720p"` | `"best"`, `"1080p"`, `"720p"`, `"480p"`, `"360p"`, `"worst"` |
| `audio_format` | `string` | No | `"mp3"` | `"mp3"`, `"m4a"`, `"opus"`, `"wav"` (when `type="audio"`) |
| `response_mode` | `string` | No | `"url"` | `"url"` or `"async"` |
| `h264_compatible` | `bool` | No | `true` | Prefer H.264 (`avc1`) + AAC (`m4a`) MP4 streams |
| `embed_thumbnail` | `bool` | No | `true` | Embed cover art and metadata into `mp3`/`m4a` |
| `is_search` | `bool` | No | `false` | Treat `url` as a search query (YouTube/SoundCloud) |
| `max_filesize` | `string` | No | `MAX_FILESIZE` | Override max filesize (e.g. `"50M"`) |
| `proxy` | `string` | No | `PROXY_URL` | Optional proxy override |

```bash
curl -X POST http://localhost:8080/api/v1/download \
  -H "Content-Type: application/json" \
  -d '{"url":"https://www.instagram.com/p/C0abc123/"}'
```

#### Carousel / Typed Response Example

```json
{
  "success": true,
  "message": "ok",
  "data": {
    "platform": "instagram",
    "media_type": "carousel",
    "count": 3,
    "engine": "gallery-dl",
    "metadata": {
      "id": "C0abc123",
      "title": "Sunset carousel",
      "uploader": "traveler",
      "webpage_url": "https://www.instagram.com/p/C0abc123/",
      "platform": "instagram"
    },
    "files": [
      { "index": 1, "media_type": "image", "filename": "01.jpg", "extension": "jpg", "mime_type": "image/jpeg", "size_bytes": 245100, "download_url": "http://localhost:8080/api/v1/files/...", "expires_at": "..." },
      { "index": 2, "media_type": "video", "filename": "02.mp4", "extension": "mp4", "mime_type": "video/mp4", "size_bytes": 3120400, "download_url": "http://localhost:8080/api/v1/files/...", "expires_at": "..." },
      { "index": 3, "media_type": "image", "filename": "03.jpg", "extension": "jpg", "mime_type": "image/jpeg", "size_bytes": 198700, "download_url": "http://localhost:8080/api/v1/files/...", "expires_at": "..." }
    ]
  }
}
```

- `data.media_type`: `"video"`, `"audio"`, `"image"`, or `"carousel"` (when `count > 1`).
- `files[].index`: `1..N` in original post order (for TikTok photo slideshows, images come first and background music last).
- `files[].media_type`: `"video"`, `"audio"`, `"image"`, or `"other"`.

Client handling example:

```js
const { media_type, files } = res.data;
if (media_type === "carousel") sendAlbum(files);
else if (media_type === "image") sendImage(files[0].download_url);
else if (media_type === "video") sendVideo(files[0].download_url);
else sendAudio(files[0].download_url);
```

### `GET /api/v1/search`

Query params: `platform` (`youtube` default | `soundcloud`), `q` (required), `limit` (`1`–`25`, default `5`), `proxy`.

```bash
curl "http://localhost:8080/api/v1/search?platform=youtube&q=lofi+beats&limit=5"
```

---

## 3. Platform-Specific Endpoints

Platform endpoints validate that `url` matches the target platform (`400 UNSUPPORTED_PLATFORM` otherwise).

- **YouTube**
  - `GET /api/v1/youtube/search?q=...&limit=5`
  - `POST /api/v1/youtube/video` — Fields: `url` (required), `quality`, `response_mode`, `h264_compatible`, `max_filesize`, `proxy`.
  - `POST /api/v1/youtube/audio` — Fields: `url` (required — URL or search keyword), `audio_format`, `response_mode`, `embed_thumbnail`, `is_search`, `max_filesize`, `proxy`.
- **TikTok**
  - `POST /api/v1/tiktok/download` — Fields: `url` (required), `type` (`auto`/`video`/`audio`, default `"auto"`), `audio_format`, `response_mode`, `h264_compatible`, `max_filesize`, `proxy`. Auto-resolves short links (`vt.tiktok.com`) and recovers photo slideshows as `carousel`.
- **Instagram, Threads, X (Twitter), Facebook, Pinterest**
  - `POST /api/v1/instagram/download`
  - `POST /api/v1/threads/download`
  - `POST /api/v1/twitter/download`
  - `POST /api/v1/facebook/download`
  - `POST /api/v1/pinterest/download`
  - Shared fields: `url` (required), `type` (`auto`/`video`/`audio`, default `"auto"`), `quality`, `audio_format`, `response_mode`, `h264_compatible`, `max_filesize`, `proxy`. Multi-item carousels return `media_type: "carousel"` and ordered entries in `data.files[]`. Pinterest resolves `pin.it` short links automatically, and Threads supports both `threads.com` and `threads.net` (via native SSR extraction, `engine: "threads"`).
- **SoundCloud**
  - `GET /api/v1/soundcloud/search?q=...&limit=5`
  - `POST /api/v1/soundcloud/download` — Fields: `url` (required — URL or search keyword), `audio_format`, `response_mode`, `embed_thumbnail`, `is_search`, `max_filesize`, `proxy` (always audio-only).

---

## 4. Async Jobs, SSE & Files

- **`GET /api/v1/jobs/:job_id`** — Returns current `ProgressEvent` (`queued`, `downloading`, `postprocessing`, `completed`, `failed`) and `data.result` when finished.
- **`GET /api/v1/jobs/:job_id/events`** — `text/event-stream` (SSE) pushing live progress events until completion or failure.
- **`GET /api/v1/files/:file_id`** — Downloads a hosted temporary file. Pass `?delete_after=true` to delete the file immediately after transfer completes.
- **`DELETE /api/v1/files/:file_id`** — Manually deletes a hosted temporary file.
