package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/justpiple/ytdlp-api/internal/config"
	"github.com/justpiple/ytdlp-api/internal/job"
	"github.com/justpiple/ytdlp-api/internal/model"
	"github.com/justpiple/ytdlp-api/internal/storage"
	"github.com/justpiple/ytdlp-api/internal/ytdlp"
)

type mockRunner struct {
	infoFn     func(ctx context.Context, target string, platform model.Platform, includeFormats bool, proxy string) (model.MediaMetadata, error)
	searchFn   func(ctx context.Context, platform model.Platform, query string, limit int, proxy string) ([]model.SearchResult, error)
	downloadFn func(ctx context.Context, opts ytdlp.DownloadOptions, onProgress ytdlp.ProgressCallback) ([]model.MediaMetadata, []string, error)
	versionsFn func(ctx context.Context) (string, string, error)
}

func (m *mockRunner) GetInfo(ctx context.Context, target string, platform model.Platform, includeFormats bool, proxy string) (model.MediaMetadata, error) {
	return m.infoFn(ctx, target, platform, includeFormats, proxy)
}

func (m *mockRunner) Search(ctx context.Context, platform model.Platform, query string, limit int, proxy string) ([]model.SearchResult, error) {
	return m.searchFn(ctx, platform, query, limit, proxy)
}

func (m *mockRunner) Download(ctx context.Context, opts ytdlp.DownloadOptions, onProgress ytdlp.ProgressCallback) ([]model.MediaMetadata, []string, error) {
	return m.downloadFn(ctx, opts, onProgress)
}

func (m *mockRunner) Versions(ctx context.Context) (string, string, error) {
	return m.versionsFn(ctx)
}

func newTestService(t *testing.T, runner ytdlp.Runner) (*Downloader, context.CancelFunc) {
	t.Helper()
	cfg := config.Config{
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
	return NewDownloader(rootCtx, cfg, runner, store, jobMgr), cancel
}

func TestDownloader_PlaySearchAndAudio(t *testing.T) {
	defer goleak.VerifyNone(t)

	var capturedTarget string
	runner := &mockRunner{
		searchFn: func(ctx context.Context, platform model.Platform, query string, limit int, proxy string) ([]model.SearchResult, error) {
			return []model.SearchResult{
				{
					ID:       "abc123",
					Title:    "Dewa 19 - Kangen",
					URL:      "https://www.youtube.com/watch?v=abc123",
					Platform: "youtube",
				},
			}, nil
		},
		downloadFn: func(ctx context.Context, opts ytdlp.DownloadOptions, onProgress ytdlp.ProgressCallback) ([]model.MediaMetadata, []string, error) {
			capturedTarget = opts.Target
			outPath := filepath.Join(opts.OutputDir, "01_abc123.mp3")
			if err := os.WriteFile(outPath, []byte("fake-mp3"), 0o640); err != nil {
				return nil, nil, err
			}
			return []model.MediaMetadata{
				{
					ID:       "abc123",
					Title:    "Dewa 19 - Kangen",
					Platform: "youtube",
				},
			}, []string{outPath}, nil
		},
	}

	svc, cancel := newTestService(t, runner)
	defer cancel()

	res, err := svc.DownloadSync(t.Context(), "http://localhost:8080", model.DownloadRequest{
		URL:         "dewa 19 kangen",
		Type:        "audio",
		AudioFormat: "mp3",
	}, model.PlatformYouTube)

	require.NoError(t, err)
	assert.Equal(t, "https://www.youtube.com/watch?v=abc123", capturedTarget)
	assert.Equal(t, "youtube", res.Platform)
	require.Len(t, res.Files, 1)
	assert.Equal(t, "mp3", res.Files[0].Extension)
	assert.Contains(t, res.Files[0].DownloadURL, "http://localhost:8080/api/v1/files/")
}

func TestDownloader_AsyncJobLifecycle(t *testing.T) {
	defer goleak.VerifyNone(t)

	runner := &mockRunner{
		downloadFn: func(ctx context.Context, opts ytdlp.DownloadOptions, onProgress ytdlp.ProgressCallback) ([]model.MediaMetadata, []string, error) {
			if onProgress != nil {
				onProgress(model.ProgressEvent{
					Status:          model.JobStatusDownloading.String(),
					ProgressPercent: 60,
					Speed:           "3.2MiB/s",
					ETA:             "00:02",
				})
			}
			outPath := filepath.Join(opts.OutputDir, "01_vid.mp4")
			if err := os.WriteFile(outPath, []byte("fake-mp4"), 0o640); err != nil {
				return nil, nil, err
			}
			return []model.MediaMetadata{{ID: "vid", Title: "Sample", Platform: "tiktok"}}, []string{outPath}, nil
		},
	}

	svc, cancel := newTestService(t, runner)
	defer cancel()

	accepted, err := svc.StartAsyncDownload(t.Context(), "http://localhost:8080", model.DownloadRequest{
		URL:          "https://www.tiktok.com/@u/video/123",
		ResponseMode: "async",
	}, model.PlatformTikTok)
	require.NoError(t, err)
	assert.NotEmpty(t, accepted.JobID)

	svc.Wait()

	ev, err := svc.GetJob(accepted.JobID)
	require.NoError(t, err)
	assert.Equal(t, model.JobStatusCompleted.String(), ev.Status)
	require.NotNil(t, ev.Result)
	require.Len(t, ev.Result.Files, 1)
	assert.Equal(t, "video/mp4", ev.Result.Files[0].MimeType)
}
