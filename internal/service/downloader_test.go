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
	"github.com/justpiple/ytdlp-api/internal/gallerydl"
	"github.com/justpiple/ytdlp-api/internal/job"
	"github.com/justpiple/ytdlp-api/internal/model"
	"github.com/justpiple/ytdlp-api/internal/storage"
	"github.com/justpiple/ytdlp-api/internal/threads"
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
	if m.versionsFn == nil {
		return "2026.10.26", "ffmpeg version 6.1.2", nil
	}
	return m.versionsFn(ctx)
}

type mockGalleryRunner struct {
	downloadFn func(ctx context.Context, opts gallerydl.Options, onProgress ytdlp.ProgressCallback) (model.MediaMetadata, []string, error)
	versionFn  func(ctx context.Context) (string, error)
}

func (m *mockGalleryRunner) Download(ctx context.Context, opts gallerydl.Options, onProgress ytdlp.ProgressCallback) (model.MediaMetadata, []string, error) {
	return m.downloadFn(ctx, opts, onProgress)
}

func (m *mockGalleryRunner) Version(ctx context.Context) (string, error) {
	if m.versionFn == nil {
		return "1.32.21", nil
	}
	return m.versionFn(ctx)
}

type mockThreadsRunner struct {
	infoFn     func(ctx context.Context, target, proxy string) (model.MediaMetadata, error)
	downloadFn func(ctx context.Context, opts threads.Options, onProgress ytdlp.ProgressCallback) (model.MediaMetadata, []string, error)
}

func (m *mockThreadsRunner) GetInfo(ctx context.Context, target, proxy string) (model.MediaMetadata, error) {
	if m.infoFn == nil {
		return model.MediaMetadata{Platform: "threads", Formats: []model.FormatOption{}}, nil
	}
	return m.infoFn(ctx, target, proxy)
}

func (m *mockThreadsRunner) Download(ctx context.Context, opts threads.Options, onProgress ytdlp.ProgressCallback) (model.MediaMetadata, []string, error) {
	if m.downloadFn == nil {
		return model.MediaMetadata{}, nil, ytdlp.ErrMediaNotFound
	}
	return m.downloadFn(ctx, opts, onProgress)
}

func newTestService(t *testing.T, runner ytdlp.Runner, gallery gallerydl.Runner) (*Downloader, context.CancelFunc) {
	return newTestServiceWithThreads(t, runner, gallery, &mockThreadsRunner{})
}

func newTestServiceWithThreads(t *testing.T, runner ytdlp.Runner, gallery gallerydl.Runner, threadsRunner threads.Runner) (*Downloader, context.CancelFunc) {
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
	return NewDownloader(rootCtx, cfg, runner, gallery, threadsRunner, store, jobMgr), cancel
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

	svc, cancel := newTestService(t, runner, &mockGalleryRunner{})
	defer cancel()

	res, err := svc.DownloadSync(t.Context(), "http://localhost:8080", model.DownloadRequest{
		URL:         "dewa 19 kangen",
		Type:        "audio",
		AudioFormat: "mp3",
	}, model.PlatformYouTube)

	require.NoError(t, err)
	assert.Equal(t, "https://www.youtube.com/watch?v=abc123", capturedTarget)
	assert.Equal(t, "youtube", res.Platform)
	assert.Equal(t, model.MediaKindAudio, res.MediaType)
	assert.Equal(t, 1, res.Count)
	assert.Equal(t, engineYTDLP, res.Engine)
	require.Len(t, res.Files, 1)
	assert.Equal(t, 1, res.Files[0].Index)
	assert.Equal(t, model.MediaKindAudio, res.Files[0].MediaType)
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

	svc, cancel := newTestService(t, runner, &mockGalleryRunner{})
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
	assert.Equal(t, model.MediaKindVideo, ev.Result.MediaType)
	require.Len(t, ev.Result.Files, 1)
	assert.Equal(t, "video/mp4", ev.Result.Files[0].MimeType)
}

func TestDownloader_AutoDetectAndCarouselStrategy(t *testing.T) {
	t.Run("instagram auto prefers gallery-dl and returns ordered carousel", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		ytCalled := false
		runner := &mockRunner{
			downloadFn: func(ctx context.Context, opts ytdlp.DownloadOptions, _ ytdlp.ProgressCallback) ([]model.MediaMetadata, []string, error) {
				ytCalled = true
				return nil, nil, ytdlp.ErrMediaNotFound
			},
		}
		gallery := &mockGalleryRunner{
			downloadFn: func(ctx context.Context, opts gallerydl.Options, _ ytdlp.ProgressCallback) (model.MediaMetadata, []string, error) {
				f1 := filepath.Join(opts.OutputDir, "01.jpg")
				f2 := filepath.Join(opts.OutputDir, "02.mp4")
				f3 := filepath.Join(opts.OutputDir, "03.jpg")
				for _, p := range []string{f1, f2, f3} {
					require.NoError(t, os.WriteFile(p, []byte("payload"), 0o640))
				}
				return model.MediaMetadata{ID: "C0ig", Title: "IG Carousel", Platform: "instagram"}, []string{f1, f2, f3}, nil
			},
		}

		svc, cancel := newTestService(t, runner, gallery)
		defer cancel()

		res, err := svc.DownloadSync(t.Context(), "http://localhost:8080", model.DownloadRequest{
			URL: "https://www.instagram.com/p/C0ig/",
		}, model.PlatformUnknown)
		require.NoError(t, err)
		assert.False(t, ytCalled)
		assert.Equal(t, "instagram", res.Platform)
		assert.Equal(t, model.MediaKindCarousel, res.MediaType)
		assert.Equal(t, 3, res.Count)
		assert.Equal(t, engineGalleryDL, res.Engine)
		require.Len(t, res.Files, 3)
		assert.Equal(t, []int{1, 2, 3}, []int{res.Files[0].Index, res.Files[1].Index, res.Files[2].Index})
		assert.Equal(t, []string{model.MediaKindImage, model.MediaKindVideo, model.MediaKindImage}, []string{
			res.Files[0].MediaType, res.Files[1].MediaType, res.Files[2].MediaType,
		})
	})

	t.Run("gallery-dl not-found falls back to yt-dlp", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		runner := &mockRunner{
			downloadFn: func(ctx context.Context, opts ytdlp.DownloadOptions, _ ytdlp.ProgressCallback) ([]model.MediaMetadata, []string, error) {
				p := filepath.Join(opts.OutputDir, "01_reel.mp4")
				require.NoError(t, os.WriteFile(p, []byte("mp4"), 0o640))
				return []model.MediaMetadata{{ID: "reel", Platform: "instagram"}}, []string{p}, nil
			},
		}
		gallery := &mockGalleryRunner{
			downloadFn: func(ctx context.Context, opts gallerydl.Options, _ ytdlp.ProgressCallback) (model.MediaMetadata, []string, error) {
				return model.MediaMetadata{}, nil, ytdlp.ErrMediaNotFound
			},
		}

		svc, cancel := newTestService(t, runner, gallery)
		defer cancel()

		res, err := svc.DownloadSync(t.Context(), "http://localhost:8080", model.DownloadRequest{
			URL: "https://www.instagram.com/reel/abc/",
		}, model.PlatformInstagram)
		require.NoError(t, err)
		assert.Equal(t, engineYTDLP, res.Engine)
		assert.Equal(t, model.MediaKindVideo, res.MediaType)
	})

	t.Run("UPSTREAM_BLOCKED skips fallback", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		ytCalled := false
		runner := &mockRunner{
			downloadFn: func(ctx context.Context, opts ytdlp.DownloadOptions, _ ytdlp.ProgressCallback) ([]model.MediaMetadata, []string, error) {
				ytCalled = true
				return nil, nil, nil
			},
		}
		gallery := &mockGalleryRunner{
			downloadFn: func(ctx context.Context, opts gallerydl.Options, _ ytdlp.ProgressCallback) (model.MediaMetadata, []string, error) {
				return model.MediaMetadata{}, nil, ytdlp.ErrUpstreamBlocked
			},
		}

		svc, cancel := newTestService(t, runner, gallery)
		defer cancel()

		_, err := svc.DownloadSync(t.Context(), "http://localhost:8080", model.DownloadRequest{
			URL: "https://www.instagram.com/p/private/",
		}, model.PlatformInstagram)
		require.Error(t, err)
		assert.ErrorIs(t, err, ytdlp.ErrUpstreamBlocked)
		assert.False(t, ytCalled)
	})

	t.Run("tiktok photo URL goes to gallery-dl first", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		ytCalled := false
		runner := &mockRunner{
			downloadFn: func(ctx context.Context, opts ytdlp.DownloadOptions, _ ytdlp.ProgressCallback) ([]model.MediaMetadata, []string, error) {
				ytCalled = true
				return nil, nil, nil
			},
		}
		gallery := &mockGalleryRunner{
			downloadFn: func(ctx context.Context, opts gallerydl.Options, _ ytdlp.ProgressCallback) (model.MediaMetadata, []string, error) {
				f1 := filepath.Join(opts.OutputDir, "01.jpg")
				f2 := filepath.Join(opts.OutputDir, "02.mp3")
				require.NoError(t, os.WriteFile(f1, []byte("img"), 0o640))
				require.NoError(t, os.WriteFile(f2, []byte("aud"), 0o640))
				return model.MediaMetadata{ID: "734", Platform: "tiktok"}, []string{f1, f2}, nil
			},
		}

		svc, cancel := newTestService(t, runner, gallery)
		defer cancel()

		res, err := svc.DownloadSync(t.Context(), "http://localhost:8080", model.DownloadRequest{
			URL: "https://www.tiktok.com/@u/photo/734567890",
		}, model.PlatformTikTok)
		require.NoError(t, err)
		assert.False(t, ytCalled)
		assert.Equal(t, engineGalleryDL, res.Engine)
		assert.Equal(t, model.MediaKindCarousel, res.MediaType)
		assert.Equal(t, model.MediaKindImage, res.Files[0].MediaType)
		assert.Equal(t, model.MediaKindAudio, res.Files[1].MediaType)
	})

	t.Run("tiktok unresolved url producing audio-only triggers gallery-dl image recovery", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		runner := &mockRunner{
			downloadFn: func(ctx context.Context, opts ytdlp.DownloadOptions, _ ytdlp.ProgressCallback) ([]model.MediaMetadata, []string, error) {
				audioOnly := filepath.Join(opts.OutputDir, "01_music.mp3")
				require.NoError(t, os.WriteFile(audioOnly, []byte("bg-music"), 0o640))
				return []model.MediaMetadata{{ID: "tt1", Platform: "tiktok"}}, []string{audioOnly}, nil
			},
		}
		gallery := &mockGalleryRunner{
			downloadFn: func(ctx context.Context, opts gallerydl.Options, _ ytdlp.ProgressCallback) (model.MediaMetadata, []string, error) {
				slide1 := filepath.Join(opts.OutputDir, "01.jpg")
				slide2 := filepath.Join(opts.OutputDir, "02.jpg")
				require.NoError(t, os.WriteFile(slide1, []byte("img1"), 0o640))
				require.NoError(t, os.WriteFile(slide2, []byte("img2"), 0o640))
				return model.MediaMetadata{ID: "tt1", Platform: "tiktok"}, []string{slide1, slide2}, nil
			},
		}

		svc, cancel := newTestService(t, runner, gallery)
		defer cancel()

		res, err := svc.DownloadSync(t.Context(), "http://localhost:8080", model.DownloadRequest{
			URL: "https://www.tiktok.com/@u/video/999",
		}, model.PlatformTikTok)
		require.NoError(t, err)
		assert.Equal(t, engineGalleryDL, res.Engine)
		assert.Equal(t, model.MediaKindCarousel, res.MediaType)
		assert.Equal(t, 2, res.Count)
	})

	t.Run("soundcloud always forces audio and skips gallery-dl", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		gdCalled := false
		var capturedMediaType string
		runner := &mockRunner{
			downloadFn: func(ctx context.Context, opts ytdlp.DownloadOptions, _ ytdlp.ProgressCallback) ([]model.MediaMetadata, []string, error) {
				capturedMediaType = opts.MediaType
				p := filepath.Join(opts.OutputDir, "01_track.mp3")
				require.NoError(t, os.WriteFile(p, []byte("audio"), 0o640))
				return []model.MediaMetadata{{ID: "sc1", Platform: "soundcloud"}}, []string{p}, nil
			},
		}
		gallery := &mockGalleryRunner{
			downloadFn: func(ctx context.Context, opts gallerydl.Options, _ ytdlp.ProgressCallback) (model.MediaMetadata, []string, error) {
				gdCalled = true
				return model.MediaMetadata{}, nil, nil
			},
		}

		svc, cancel := newTestService(t, runner, gallery)
		defer cancel()

		res, err := svc.DownloadSync(t.Context(), "http://localhost:8080", model.DownloadRequest{
			URL:  "https://soundcloud.com/artist/track",
			Type: "video",
		}, model.PlatformUnknown)
		require.NoError(t, err)
		assert.False(t, gdCalled)
		assert.Equal(t, model.MediaKindAudio, capturedMediaType)
		assert.Equal(t, model.MediaKindAudio, res.MediaType)
		assert.Equal(t, engineYTDLP, res.Engine)
	})

	t.Run("explicit type=video forces yt-dlp on instagram", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		gdCalled := false
		var capturedMediaType string
		runner := &mockRunner{
			downloadFn: func(ctx context.Context, opts ytdlp.DownloadOptions, _ ytdlp.ProgressCallback) ([]model.MediaMetadata, []string, error) {
				capturedMediaType = opts.MediaType
				p := filepath.Join(opts.OutputDir, "01_vid.mp4")
				require.NoError(t, os.WriteFile(p, []byte("video"), 0o640))
				return []model.MediaMetadata{{ID: "ig1", Platform: "instagram"}}, []string{p}, nil
			},
		}
		gallery := &mockGalleryRunner{
			downloadFn: func(ctx context.Context, opts gallerydl.Options, _ ytdlp.ProgressCallback) (model.MediaMetadata, []string, error) {
				gdCalled = true
				return model.MediaMetadata{}, nil, nil
			},
		}

		svc, cancel := newTestService(t, runner, gallery)
		defer cancel()

		res, err := svc.DownloadSync(t.Context(), "http://localhost:8080", model.DownloadRequest{
			URL:  "https://www.instagram.com/reel/abc/",
			Type: "video",
		}, model.PlatformInstagram)
		require.NoError(t, err)
		assert.False(t, gdCalled)
		assert.Equal(t, model.MediaKindVideo, capturedMediaType)
		assert.Equal(t, engineYTDLP, res.Engine)
	})

	t.Run("youtube default forces video and skips gallery-dl", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		gdCalled := false
		var capturedMediaType string
		runner := &mockRunner{
			downloadFn: func(ctx context.Context, opts ytdlp.DownloadOptions, _ ytdlp.ProgressCallback) ([]model.MediaMetadata, []string, error) {
				capturedMediaType = opts.MediaType
				p := filepath.Join(opts.OutputDir, "01_yt.mp4")
				require.NoError(t, os.WriteFile(p, []byte("video"), 0o640))
				return []model.MediaMetadata{{ID: "yt1", Platform: "youtube"}}, []string{p}, nil
			},
		}
		gallery := &mockGalleryRunner{
			downloadFn: func(ctx context.Context, opts gallerydl.Options, _ ytdlp.ProgressCallback) (model.MediaMetadata, []string, error) {
				gdCalled = true
				return model.MediaMetadata{}, nil, nil
			},
		}

		svc, cancel := newTestService(t, runner, gallery)
		defer cancel()

		res, err := svc.DownloadSync(t.Context(), "http://localhost:8080", model.DownloadRequest{
			URL: "https://www.youtube.com/watch?v=abc",
		}, model.PlatformUnknown)
		require.NoError(t, err)
		assert.False(t, gdCalled)
		assert.Equal(t, model.MediaKindVideo, capturedMediaType)
		assert.Equal(t, engineYTDLP, res.Engine)
	})

	t.Run("pinterest auto prefers gallery-dl and falls back to yt-dlp on not-found", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		ytCalled := false
		runner := &mockRunner{
			downloadFn: func(ctx context.Context, opts ytdlp.DownloadOptions, _ ytdlp.ProgressCallback) ([]model.MediaMetadata, []string, error) {
				ytCalled = true
				p := filepath.Join(opts.OutputDir, "01_pin.mp4")
				require.NoError(t, os.WriteFile(p, []byte("pin-video"), 0o640))
				return []model.MediaMetadata{{ID: "123456", Platform: "pinterest"}}, []string{p}, nil
			},
		}
		gallery := &mockGalleryRunner{
			downloadFn: func(ctx context.Context, opts gallerydl.Options, _ ytdlp.ProgressCallback) (model.MediaMetadata, []string, error) {
				f1 := filepath.Join(opts.OutputDir, "01.jpg")
				require.NoError(t, os.WriteFile(f1, []byte("pin-img"), 0o640))
				return model.MediaMetadata{ID: "123456", Title: "Pin Image", Platform: "pinterest"}, []string{f1}, nil
			},
		}

		svc, cancel := newTestService(t, runner, gallery)
		defer cancel()

		res, err := svc.DownloadSync(t.Context(), "http://localhost:8080", model.DownloadRequest{
			URL: "https://www.pinterest.com/pin/123456/",
		}, model.PlatformPinterest)
		require.NoError(t, err)
		assert.False(t, ytCalled)
		assert.Equal(t, "pinterest", res.Platform)
		assert.Equal(t, model.MediaKindImage, res.MediaType)
		assert.Equal(t, engineGalleryDL, res.Engine)
		require.Len(t, res.Files, 1)
		assert.Equal(t, model.MediaKindImage, res.Files[0].MediaType)

		gallery.downloadFn = func(ctx context.Context, opts gallerydl.Options, _ ytdlp.ProgressCallback) (model.MediaMetadata, []string, error) {
			return model.MediaMetadata{}, nil, ytdlp.ErrMediaNotFound
		}
		res2, err := svc.DownloadSync(t.Context(), "http://localhost:8080", model.DownloadRequest{
			URL: "https://www.pinterest.com/pin/123456/",
		}, model.PlatformPinterest)
		require.NoError(t, err)
		assert.True(t, ytCalled)
		assert.Equal(t, engineYTDLP, res2.Engine)
		assert.Equal(t, model.MediaKindVideo, res2.MediaType)
	})

	t.Run("empty primary result without error falls back and returns error when fallback also fails", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		runner := &mockRunner{
			downloadFn: func(ctx context.Context, opts ytdlp.DownloadOptions, _ ytdlp.ProgressCallback) ([]model.MediaMetadata, []string, error) {
				return nil, nil, ytdlp.ErrMediaNotFound
			},
		}
		gallery := &mockGalleryRunner{
			downloadFn: func(ctx context.Context, opts gallerydl.Options, _ ytdlp.ProgressCallback) (model.MediaMetadata, []string, error) {
				return model.MediaMetadata{}, nil, nil
			},
		}

		svc, cancel := newTestService(t, runner, gallery)
		defer cancel()

		_, err := svc.DownloadSync(t.Context(), "http://localhost:8080", model.DownloadRequest{
			URL: "https://www.instagram.com/p/empty/",
		}, model.PlatformInstagram)
		require.Error(t, err)
		assert.ErrorIs(t, err, ytdlp.ErrMediaNotFound)
	})

	t.Run("threads uses native threads runner for download and info without falling back to gallery-dl", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		gdCalled := false
		ytCalled := false
		runner := &mockRunner{
			infoFn: func(ctx context.Context, target string, platform model.Platform, includeFormats bool, proxy string) (model.MediaMetadata, error) {
				ytCalled = true
				return model.MediaMetadata{}, nil
			},
			downloadFn: func(ctx context.Context, opts ytdlp.DownloadOptions, _ ytdlp.ProgressCallback) ([]model.MediaMetadata, []string, error) {
				ytCalled = true
				return nil, nil, nil
			},
		}
		gallery := &mockGalleryRunner{
			downloadFn: func(ctx context.Context, opts gallerydl.Options, _ ytdlp.ProgressCallback) (model.MediaMetadata, []string, error) {
				gdCalled = true
				return model.MediaMetadata{}, nil, nil
			},
		}
		threadsRunner := &mockThreadsRunner{
			infoFn: func(ctx context.Context, target, proxy string) (model.MediaMetadata, error) {
				return model.MediaMetadata{
					ID:         "DgFv18ok-A3",
					Title:      "Threads caption",
					Uploader:   "nexorapublic",
					WebpageURL: target,
					Extractor:  "threads",
					Platform:   "threads",
				}, nil
			},
			downloadFn: func(ctx context.Context, opts threads.Options, _ ytdlp.ProgressCallback) (model.MediaMetadata, []string, error) {
				img := filepath.Join(opts.OutputDir, "01_DgFv18ok-A3.jpg")
				require.NoError(t, os.WriteFile(img, []byte("jpg"), 0o640))
				return model.MediaMetadata{
					ID:         "DgFv18ok-A3",
					Title:      "Threads caption",
					Uploader:   "nexorapublic",
					WebpageURL: opts.Target,
					Extractor:  "threads",
					Platform:   "threads",
				}, []string{img}, nil
			},
		}

		svc, cancel := newTestServiceWithThreads(t, runner, gallery, threadsRunner)
		defer cancel()

		info, err := svc.ExtractInfo(t.Context(), model.InfoRequest{
			URL: "https://www.threads.com/@nexorapublic/post/DgFv18ok-A3",
		})
		require.NoError(t, err)
		assert.Equal(t, "DgFv18ok-A3", info.ID)
		assert.Equal(t, "threads", info.Platform)

		res, err := svc.DownloadSync(t.Context(), "http://localhost:8080", model.DownloadRequest{
			URL: "https://www.threads.com/@nexorapublic/post/DgFv18ok-A3",
		}, model.PlatformUnknown)
		require.NoError(t, err)
		assert.False(t, gdCalled)
		assert.False(t, ytCalled)
		assert.Equal(t, "threads", res.Platform)
		assert.Equal(t, engineThreads, res.Engine)
		assert.Equal(t, model.MediaKindImage, res.MediaType)
		require.Len(t, res.Files, 1)
	})
}

func TestDownloader_H264CompatibilityPostProcessing(t *testing.T) {
	tests := []struct {
		name           string
		h264Compat     *bool
		wantTranscoded bool
	}{
		{
			name:           "default h264_compatible=true transcodes vp9 output",
			h264Compat:     nil,
			wantTranscoded: true,
		},
		{
			name:           "explicit h264_compatible=false keeps original vp9 output",
			h264Compat:     new(false),
			wantTranscoded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer goleak.VerifyNone(t)

			binDir := t.TempDir()
			ffprobePath := filepath.Join(binDir, "ffprobe")
			ffmpegPath := filepath.Join(binDir, "ffmpeg")
			require.NoError(t, os.WriteFile(ffprobePath, []byte(`#!/bin/sh
printf '{"streams":[{"index":0,"codec_type":"video","codec_name":"vp9","codec_tag_string":"vp09","pix_fmt":"yuv420p"}]}\n'
`), 0o755))
			require.NoError(t, os.WriteFile(ffmpegPath, []byte(`#!/bin/sh
for last in "$@"; do :; done
printf 'h264-converted' > "$last"
`), 0o755))

			var producedPath string
			runner := &mockRunner{
				downloadFn: func(ctx context.Context, opts ytdlp.DownloadOptions, _ ytdlp.ProgressCallback) ([]model.MediaMetadata, []string, error) {
					producedPath = filepath.Join(opts.OutputDir, "01_DgGv7HKztVz.mp4")
					require.NoError(t, os.WriteFile(producedPath, []byte("raw-vp9"), 0o640))
					return []model.MediaMetadata{{ID: "DgGv7HKztVz", Platform: "youtube"}}, []string{producedPath}, nil
				},
			}

			cfg := config.Config{
				FFmpegPath:             ffmpegPath,
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
			defer cancel()

			svc := NewDownloader(rootCtx, cfg, runner, &mockGalleryRunner{}, &mockThreadsRunner{}, store, jobMgr)
			res, err := svc.DownloadSync(t.Context(), "http://localhost:8080", model.DownloadRequest{
				URL:            "https://www.youtube.com/watch?v=DgGv7HKztVz",
				H264Compatible: tt.h264Compat,
			}, model.PlatformYouTube)
			require.NoError(t, err)
			require.Len(t, res.Files, 1)

			data, readErr := os.ReadFile(producedPath)
			require.NoError(t, readErr)
			if tt.wantTranscoded {
				assert.Equal(t, "h264-converted", string(data))
			} else {
				assert.Equal(t, "raw-vp9", string(data))
			}
		})
	}
}
