package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/justpiple/ytdlp-api/internal/config"
	"github.com/justpiple/ytdlp-api/internal/gallerydl"
	"github.com/justpiple/ytdlp-api/internal/job"
	"github.com/justpiple/ytdlp-api/internal/model"
	"github.com/justpiple/ytdlp-api/internal/service"
	"github.com/justpiple/ytdlp-api/internal/storage"
	"github.com/justpiple/ytdlp-api/internal/ytdlp"
)

type handlerMockRunner struct{}

func (m *handlerMockRunner) GetInfo(_ context.Context, target string, platform model.Platform, _ bool, _ string) (model.MediaMetadata, error) {
	return model.MediaMetadata{
		ID:         "123",
		Title:      "Test Title",
		WebpageURL: target,
		Platform:   platform.String(),
		Formats:    []model.FormatOption{},
	}, nil
}

func (m *handlerMockRunner) Search(_ context.Context, platform model.Platform, query string, _ int, _ string) ([]model.SearchResult, error) {
	return []model.SearchResult{
		{
			ID:       "track1",
			Title:    query + " result",
			URL:      "https://www.youtube.com/watch?v=track1",
			Platform: platform.String(),
		},
	}, nil
}

func (m *handlerMockRunner) Download(_ context.Context, opts ytdlp.DownloadOptions, onProgress ytdlp.ProgressCallback) ([]model.MediaMetadata, []string, error) {
	if onProgress != nil {
		onProgress(model.ProgressEvent{
			Status:          model.JobStatusDownloading.String(),
			ProgressPercent: 50,
		})
	}
	ext := "mp4"
	if opts.MediaType == "audio" {
		ext = "mp3"
	}
	outPath := filepath.Join(opts.OutputDir, "01_item."+ext)
	if err := os.WriteFile(outPath, []byte("binary-payload"), 0o640); err != nil {
		return nil, nil, err
	}
	return []model.MediaMetadata{
		{
			ID:       "item",
			Title:    "Test Media",
			Platform: opts.Platform.String(),
		},
	}, []string{outPath}, nil
}

func (m *handlerMockRunner) Versions(_ context.Context) (string, string, error) {
	return "2025.01.15", "ffmpeg version 6.1", nil
}

type handlerMockGalleryRunner struct {
	called bool
}

func (m *handlerMockGalleryRunner) Download(_ context.Context, opts gallerydl.Options, _ ytdlp.ProgressCallback) (model.MediaMetadata, []string, error) {
	m.called = true
	f1 := filepath.Join(opts.OutputDir, "01_slide.jpg")
	f2 := filepath.Join(opts.OutputDir, "02_slide.mp4")
	if err := os.WriteFile(f1, []byte("img-bytes"), 0o640); err != nil {
		return model.MediaMetadata{}, nil, err
	}
	if err := os.WriteFile(f2, []byte("vid-bytes"), 0o640); err != nil {
		return model.MediaMetadata{}, nil, err
	}
	return model.MediaMetadata{
		ID:       "carousel-1",
		Title:    "Carousel Post",
		Platform: opts.Platform.String(),
	}, []string{f1, f2}, nil
}

func (m *handlerMockGalleryRunner) Version(_ context.Context) (string, error) {
	return "1.32.21", nil
}

func setupTestRouter(t *testing.T) (http.Handler, *service.Downloader, *handlerMockGalleryRunner, context.CancelFunc) {
	t.Helper()
	cfg := config.Config{
		GinMode:                "test",
		TempDir:                t.TempDir(),
		FileTTL:                10 * time.Minute,
		MaxConcurrentDownloads: 2,
		DownloadTimeout:        10 * time.Second,
		InfoTimeout:            5 * time.Second,
		MaxFilesize:            "100M",
		MaxPlaylistItems:       10,
	}
	store, err := storage.NewManager(cfg.TempDir, cfg.FileTTL)
	require.NoError(t, err)
	jobMgr := job.NewManager(cfg.MaxConcurrentDownloads)
	rootCtx, cancel := context.WithCancel(t.Context())
	galleryMock := &handlerMockGalleryRunner{}
	svc := service.NewDownloader(rootCtx, cfg, &handlerMockRunner{}, galleryMock, store, jobMgr)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	return NewRouter(cfg, svc, logger), svc, galleryMock, cancel
}

type testAPIResponse[T any] struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

func TestSystemEndpoints(t *testing.T) {
	router, _, _, cancel := setupTestRouter(t)
	defer cancel()

	t.Run("healthz", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		var resp testAPIResponse[model.HealthResponse]
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
		assert.Equal(t, "2025.01.15", resp.Data.YTDLPVersion)
		assert.Equal(t, "1.32.21", resp.Data.GalleryDLVersion)
	})

	t.Run("platforms list", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/platforms", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		var resp testAPIResponse[[]model.PlatformCapability]
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.GreaterOrEqual(t, len(resp.Data), 8)
	})
}

func TestUnifiedAndPlatformEndpoints(t *testing.T) {
	router, _, _, cancel := setupTestRouter(t)
	defer cancel()

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
	}{
		{
			name:       "unified info",
			method:     http.MethodPost,
			path:       "/api/v1/info",
			body:       `{"url":"https://www.instagram.com/reel/abc/"}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "unified download url mode",
			method:     http.MethodPost,
			path:       "/api/v1/download",
			body:       `{"url":"https://www.tiktok.com/@u/video/123"}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "unified search",
			method:     http.MethodGet,
			path:       "/api/v1/search?platform=soundcloud&q=ambient&limit=3",
			wantStatus: http.StatusOK,
		},
		{
			name:       "youtube search",
			method:     http.MethodGet,
			path:       "/api/v1/youtube/search?q=lofi",
			wantStatus: http.StatusOK,
		},
		{
			name:       "youtube video",
			method:     http.MethodPost,
			path:       "/api/v1/youtube/video",
			body:       `{"url":"https://youtu.be/abc123","quality":"720p"}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "youtube audio !play query",
			method:     http.MethodPost,
			path:       "/api/v1/youtube/audio",
			body:       `{"url":"never gonna give you up","is_search":true}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "tiktok download",
			method:     http.MethodPost,
			path:       "/api/v1/tiktok/download",
			body:       `{"url":"https://vt.tiktok.com/abc/"}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "instagram download",
			method:     http.MethodPost,
			path:       "/api/v1/instagram/download",
			body:       `{"url":"https://www.instagram.com/p/abc/"}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "threads download",
			method:     http.MethodPost,
			path:       "/api/v1/threads/download",
			body:       `{"url":"https://www.threads.net/@u/post/abc"}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "twitter download",
			method:     http.MethodPost,
			path:       "/api/v1/twitter/download",
			body:       `{"url":"https://x.com/user/status/123"}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "soundcloud search",
			method:     http.MethodGet,
			path:       "/api/v1/soundcloud/search?q=synthwave",
			wantStatus: http.StatusOK,
		},
		{
			name:       "soundcloud download",
			method:     http.MethodPost,
			path:       "/api/v1/soundcloud/download",
			body:       `{"url":"https://soundcloud.com/artist/track"}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "facebook download",
			method:     http.MethodPost,
			path:       "/api/v1/facebook/download",
			body:       `{"url":"https://fb.watch/abc/"}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "platform mismatch rejected",
			method:     http.MethodPost,
			path:       "/api/v1/tiktok/download",
			body:       `{"url":"https://www.youtube.com/watch?v=123"}`,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var bodyReader io.Reader
			if tt.body != "" {
				bodyReader = bytes.NewBufferString(tt.body)
			}
			req := httptest.NewRequest(tt.method, tt.path, bodyReader)
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code, rec.Body.String())
		})
	}
}

func TestDownloadAndDeleteAfter(t *testing.T) {
	router, _, _, cancel := setupTestRouter(t)
	defer cancel()

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/youtube/audio",
		bytes.NewBufferString(`{"url":"https://www.youtube.com/watch?v=123"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp testAPIResponse[model.DownloadResult]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Data.Files, 1)
	fileID := resp.Data.Files[0].FileID

	// Download with delete_after=true
	dlReq := httptest.NewRequest(http.MethodGet, "/api/v1/files/"+fileID+"?delete_after=true", nil)
	dlRec := httptest.NewRecorder()
	router.ServeHTTP(dlRec, dlReq)
	assert.Equal(t, http.StatusOK, dlRec.Code)
	assert.Equal(t, "binary-payload", dlRec.Body.String())

	// Second download should 404 because delete_after=true removed it
	dlReq2 := httptest.NewRequest(http.MethodGet, "/api/v1/files/"+fileID, nil)
	dlRec2 := httptest.NewRecorder()
	router.ServeHTTP(dlRec2, dlReq2)
	assert.Equal(t, http.StatusNotFound, dlRec2.Code)
}

func TestCarouselAutoDetectAndValidation(t *testing.T) {
	t.Run("instagram carousel returns typed ordered files", func(t *testing.T) {
		router, _, galleryMock, cancel := setupTestRouter(t)
		defer cancel()

		req := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/instagram/download",
			bytes.NewBufferString(`{"url":"https://www.instagram.com/p/C0abc/"}`),
		)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.True(t, galleryMock.called)

		var resp testAPIResponse[model.DownloadResult]
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Equal(t, model.MediaKindCarousel, resp.Data.MediaType)
		assert.Equal(t, 2, resp.Data.Count)
		assert.Equal(t, "gallery-dl", resp.Data.Engine)
		require.Len(t, resp.Data.Files, 2)
		assert.Equal(t, 1, resp.Data.Files[0].Index)
		assert.Equal(t, model.MediaKindImage, resp.Data.Files[0].MediaType)
		assert.Equal(t, 2, resp.Data.Files[1].Index)
		assert.Equal(t, model.MediaKindVideo, resp.Data.Files[1].MediaType)
	})

	t.Run("soundcloud via unified /download returns audio only without gallery-dl", func(t *testing.T) {
		router, _, galleryMock, cancel := setupTestRouter(t)
		defer cancel()

		req := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/download",
			bytes.NewBufferString(`{"url":"https://soundcloud.com/artist/track"}`),
		)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.False(t, galleryMock.called)

		var resp testAPIResponse[model.DownloadResult]
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Equal(t, model.MediaKindAudio, resp.Data.MediaType)
		assert.Equal(t, "yt-dlp", resp.Data.Engine)
	})

	t.Run("stream response_mode rejected with 400", func(t *testing.T) {
		router, _, _, cancel := setupTestRouter(t)
		defer cancel()

		req := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/download",
			bytes.NewBufferString(`{"url":"https://x.com/user/status/123","response_mode":"stream"}`),
		)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("invalid type rejected with 400", func(t *testing.T) {
		router, _, _, cancel := setupTestRouter(t)
		defer cancel()

		req := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/instagram/download",
			bytes.NewBufferString(`{"url":"https://www.instagram.com/p/C0abc/","type":"image"}`),
		)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestAsyncJobAndSSE(t *testing.T) {
	router, svc, _, cancel := setupTestRouter(t)
	defer cancel()

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/youtube/video",
		bytes.NewBufferString(`{"url":"https://www.youtube.com/watch?v=123","response_mode":"async"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	var accepted testAPIResponse[model.JobAcceptedResponse]
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &accepted))

	svc.Wait()

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+accepted.Data.JobID, nil)
	statusRec := httptest.NewRecorder()
	router.ServeHTTP(statusRec, statusReq)
	assert.Equal(t, http.StatusOK, statusRec.Code)

	sseReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+accepted.Data.JobID+"/events", nil)
	sseRec := httptest.NewRecorder()
	router.ServeHTTP(sseRec, sseReq)

	assert.Equal(t, http.StatusOK, sseRec.Code)
	assert.True(t, strings.Contains(sseRec.Body.String(), "event:completed"))
}
