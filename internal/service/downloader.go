package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bens-pa/ytdlp-api/internal/config"
	"github.com/bens-pa/ytdlp-api/internal/job"
	"github.com/bens-pa/ytdlp-api/internal/model"
	"github.com/bens-pa/ytdlp-api/internal/storage"
	"github.com/bens-pa/ytdlp-api/internal/ytdlp"
)

// Downloader orchestrates info extraction, search, sync/stream/async downloads, and health checks.
type Downloader struct {
	cfg     config.Config
	runner  ytdlp.Runner
	storage *storage.Manager
	jobs    *job.Manager

	rootCtx context.Context
	wg      sync.WaitGroup
}

// NewDownloader creates a new Downloader service with explicit dependencies.
func NewDownloader(
	rootCtx context.Context,
	cfg config.Config,
	runner ytdlp.Runner,
	storageMgr *storage.Manager,
	jobMgr *job.Manager,
) *Downloader {
	return &Downloader{
		cfg:     cfg,
		runner:  runner,
		storage: storageMgr,
		jobs:    jobMgr,
		rootCtx: rootCtx,
	}
}

// Wait blocks until all background async download goroutines finish.
func (d *Downloader) Wait() {
	d.wg.Wait()
}

// ExtractInfo fetches media metadata and optional formats without downloading files.
func (d *Downloader) ExtractInfo(ctx context.Context, req model.InfoRequest) (model.MediaMetadata, error) {
	targetURL, err := ytdlp.ValidateURL(req.URL)
	if err != nil {
		return model.MediaMetadata{}, err
	}

	platform := ytdlp.DetectPlatform(targetURL)
	runCtx, cancel := context.WithTimeout(ctx, d.cfg.InfoTimeout)
	defer cancel()

	meta, err := d.runner.GetInfo(runCtx, targetURL, platform, req.IncludeFormats, req.Proxy)
	if err != nil {
		return model.MediaMetadata{}, fmt.Errorf("extracting info: %w", err)
	}
	return meta, nil
}

// Search queries YouTube or SoundCloud for tracks/videos.
func (d *Downloader) Search(
	ctx context.Context,
	platform model.Platform,
	query string,
	limit int,
	proxy string,
) (model.SearchResponse, error) {
	runCtx, cancel := context.WithTimeout(ctx, d.cfg.InfoTimeout)
	defer cancel()

	results, err := d.runner.Search(runCtx, platform, query, limit, proxy)
	if err != nil {
		return model.SearchResponse{}, fmt.Errorf("searching %s: %w", platform.String(), err)
	}

	return model.SearchResponse{
		Platform: platform.String(),
		Query:    strings.TrimSpace(query),
		Count:    len(results),
		Results:  results,
	}, nil
}

// DownloadSync downloads media and registers resulting files for temporary HTTP retrieval.
func (d *Downloader) DownloadSync(
	ctx context.Context,
	baseURL string,
	req model.DownloadRequest,
	expectedPlatform model.Platform,
) (model.DownloadResult, error) {
	opts, err := d.prepareDownloadOptions(ctx, req, expectedPlatform)
	if err != nil {
		return model.DownloadResult{}, err
	}

	if err := d.jobs.Acquire(ctx); err != nil {
		return model.DownloadResult{}, fmt.Errorf("waiting for download slot: %w", err)
	}
	defer d.jobs.Release()

	jobID := storage.NewID()
	jobDir, err := d.storage.CreateJobDir(jobID)
	if err != nil {
		return model.DownloadResult{}, fmt.Errorf("creating temp job dir: %w", err)
	}
	opts.OutputDir = jobDir

	runCtx, cancel := context.WithTimeout(ctx, d.cfg.DownloadTimeout)
	defer cancel()

	metaList, filePaths, err := d.runner.Download(runCtx, opts, nil)
	if err != nil {
		d.storage.RemoveDir(jobDir)
		return model.DownloadResult{}, fmt.Errorf("downloading media: %w", err)
	}

	result, err := d.buildDownloadResult(baseURL, opts.Platform, metaList, filePaths)
	if err != nil {
		d.storage.RemoveDir(jobDir)
		return model.DownloadResult{}, err
	}

	return result, nil
}

// DownloadForStream downloads media into a temporary directory and returns the primary file path
// plus a cleanup callback the caller MUST defer after streaming the file.
func (d *Downloader) DownloadForStream(
	ctx context.Context,
	req model.DownloadRequest,
	expectedPlatform model.Platform,
) (model.MediaMetadata, string, func(), error) {
	opts, err := d.prepareDownloadOptions(ctx, req, expectedPlatform)
	if err != nil {
		return model.MediaMetadata{}, "", nil, err
	}

	if err := d.jobs.Acquire(ctx); err != nil {
		return model.MediaMetadata{}, "", nil, fmt.Errorf("waiting for download slot: %w", err)
	}
	defer d.jobs.Release()

	jobID := storage.NewID()
	jobDir, err := d.storage.CreateJobDir(jobID)
	if err != nil {
		return model.MediaMetadata{}, "", nil, fmt.Errorf("creating temp job dir: %w", err)
	}
	cleanup := func() {
		d.storage.RemoveDir(jobDir)
	}
	opts.OutputDir = jobDir

	runCtx, cancel := context.WithTimeout(ctx, d.cfg.DownloadTimeout)
	defer cancel()

	metaList, filePaths, err := d.runner.Download(runCtx, opts, nil)
	if err != nil {
		cleanup()
		return model.MediaMetadata{}, "", nil, fmt.Errorf("downloading stream media: %w", err)
	}

	meta := pickPrimaryMetadata(metaList, opts.Platform)
	return meta, filePaths[0], cleanup, nil
}

// StartAsyncDownload enqueues a download job in the background and returns URLs for polling and SSE.
func (d *Downloader) StartAsyncDownload(
	ctx context.Context,
	baseURL string,
	req model.DownloadRequest,
	expectedPlatform model.Platform,
) (model.JobAcceptedResponse, error) {
	opts, err := d.prepareDownloadOptions(ctx, req, expectedPlatform)
	if err != nil {
		return model.JobAcceptedResponse{}, err
	}

	jobID := d.jobs.Create()
	cleanBase := strings.TrimRight(baseURL, "/")

	d.wg.Add(1)
	go d.runAsyncJob(jobID, cleanBase, opts)

	return model.JobAcceptedResponse{
		JobID:     jobID,
		Status:    model.JobStatusQueued.String(),
		StatusURL: fmt.Sprintf("%s/api/v1/jobs/%s", cleanBase, jobID),
		SSEURL:    fmt.Sprintf("%s/api/v1/jobs/%s/events", cleanBase, jobID),
	}, nil
}

func (d *Downloader) runAsyncJob(jobID, baseURL string, opts ytdlp.DownloadOptions) {
	defer d.wg.Done()

	jobCtx, cancel := context.WithTimeout(d.rootCtx, d.cfg.DownloadTimeout)
	defer cancel()

	if err := d.jobs.Acquire(jobCtx); err != nil {
		d.jobs.Update(jobID, model.ProgressEvent{
			Status:  model.JobStatusFailed.String(),
			Message: "download cancelled while waiting in queue",
			Error:   err.Error(),
		})
		return
	}
	defer d.jobs.Release()

	jobDir, err := d.storage.CreateJobDir(jobID)
	if err != nil {
		d.jobs.Update(jobID, model.ProgressEvent{
			Status:  model.JobStatusFailed.String(),
			Message: "failed to allocate temporary directory",
			Error:   err.Error(),
		})
		return
	}
	opts.OutputDir = jobDir

	d.jobs.Update(jobID, model.ProgressEvent{
		Status:  model.JobStatusDownloading.String(),
		Message: "starting download",
	})

	metaList, filePaths, err := d.runner.Download(jobCtx, opts, func(ev model.ProgressEvent) {
		d.jobs.Update(jobID, ev)
	})
	if err != nil {
		d.storage.RemoveDir(jobDir)
		d.jobs.Update(jobID, model.ProgressEvent{
			Status:  model.JobStatusFailed.String(),
			Message: "download failed",
			Error:   err.Error(),
		})
		return
	}

	result, err := d.buildDownloadResult(baseURL, opts.Platform, metaList, filePaths)
	if err != nil {
		d.storage.RemoveDir(jobDir)
		d.jobs.Update(jobID, model.ProgressEvent{
			Status:  model.JobStatusFailed.String(),
			Message: "failed to register output files",
			Error:   err.Error(),
		})
		return
	}

	d.jobs.Update(jobID, model.ProgressEvent{
		Status:          model.JobStatusCompleted.String(),
		ProgressPercent: 100,
		Message:         "completed",
		Result:          &result,
	})
}

// GetJob returns the current status snapshot of an async job.
func (d *Downloader) GetJob(jobID string) (model.ProgressEvent, error) {
	return d.jobs.Get(jobID)
}

// SubscribeJob opens a live SSE channel for an async job.
func (d *Downloader) SubscribeJob(jobID string) (<-chan model.ProgressEvent, func(), error) {
	return d.jobs.Subscribe(jobID)
}

// GetFile retrieves a stored file record for HTTP delivery.
func (d *Downloader) GetFile(fileID string) (storage.StoredFile, error) {
	return d.storage.GetFile(fileID)
}

// DeleteFile removes a stored file from registry and disk.
func (d *Downloader) DeleteFile(fileID string) error {
	return d.storage.DeleteFile(fileID)
}

// Health inspects dependency versions, active concurrency, and storage status.
func (d *Downloader) Health(ctx context.Context) (model.HealthResponse, error) {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	ytVer, ffVer, err := d.runner.Versions(checkCtx)
	status := "ok"
	if err != nil {
		status = "degraded"
	}

	return model.HealthResponse{
		Status:        status,
		YTDLPVersion:  ytVer,
		FFmpegVersion: ffVer,
		ActiveJobs:    d.jobs.ActiveCount(),
		MaxConcurrent: d.jobs.MaxConcurrent(),
		StoredFiles:   d.storage.Count(),
	}, err
}

// SupportedPlatforms returns metadata describing all primary supported platforms.
func (d *Downloader) SupportedPlatforms() []model.PlatformCapability {
	return []model.PlatformCapability{
		{
			Name:       model.PlatformYouTube.String(),
			Display:    "YouTube",
			Features:   []string{"video", "audio", "search", "play"},
			CookiesKey: "youtube.txt",
		},
		{
			Name:       model.PlatformTikTok.String(),
			Display:    "TikTok",
			Features:   []string{"video", "audio", "carousel"},
			CookiesKey: "tiktok.txt",
		},
		{
			Name:       model.PlatformInstagram.String(),
			Display:    "Instagram",
			Features:   []string{"video", "audio", "carousel", "reels", "stories"},
			CookiesKey: "instagram.txt",
		},
		{
			Name:       model.PlatformThreads.String(),
			Display:    "Threads",
			Features:   []string{"video", "audio", "carousel"},
			CookiesKey: "threads.txt",
		},
		{
			Name:       model.PlatformTwitter.String(),
			Display:    "X (Twitter)",
			Features:   []string{"video", "audio", "gif", "multi-video"},
			CookiesKey: "twitter.txt",
		},
		{
			Name:       model.PlatformSoundCloud.String(),
			Display:    "SoundCloud",
			Features:   []string{"audio", "search", "play"},
			CookiesKey: "soundcloud.txt",
		},
		{
			Name:       model.PlatformFacebook.String(),
			Display:    "Facebook",
			Features:   []string{"video", "audio", "reels"},
			CookiesKey: "facebook.txt",
		},
		{
			Name:       model.PlatformGeneric.String(),
			Display:    "Universal (yt-dlp supported sites)",
			Features:   []string{"video", "audio", "info"},
			CookiesKey: "cookies.txt",
		},
	}
}

func (d *Downloader) prepareDownloadOptions(
	ctx context.Context,
	req model.DownloadRequest,
	expectedPlatform model.Platform,
) (ytdlp.DownloadOptions, error) {
	rawInput := strings.TrimSpace(req.URL)
	if rawInput == "" {
		return ytdlp.DownloadOptions{}, fmt.Errorf("%w: url or query is required", ytdlp.ErrInvalidURL)
	}

	target := rawInput
	resolvedPlatform := expectedPlatform

	// Support !play-style search-and-download on YouTube and SoundCloud when input is not a URL.
	shouldSearch := req.IsSearch || (!ytdlp.IsHTTPURL(rawInput) &&
		(expectedPlatform == model.PlatformYouTube || expectedPlatform == model.PlatformSoundCloud))

	if shouldSearch {
		searchPlatform := expectedPlatform
		if searchPlatform == model.PlatformUnknown || searchPlatform == model.PlatformGeneric {
			searchPlatform = model.PlatformYouTube
		}
		if searchPlatform != model.PlatformYouTube && searchPlatform != model.PlatformSoundCloud {
			return ytdlp.DownloadOptions{}, fmt.Errorf("%w: search-download only supports youtube or soundcloud", ytdlp.ErrUnsupportedPlatform)
		}

		searchCtx, cancel := context.WithTimeout(ctx, d.cfg.InfoTimeout)
		defer cancel()

		results, err := d.runner.Search(searchCtx, searchPlatform, rawInput, 1, req.Proxy)
		if err != nil {
			return ytdlp.DownloadOptions{}, fmt.Errorf("resolving search query: %w", err)
		}
		if len(results) == 0 || results[0].URL == "" {
			return ytdlp.DownloadOptions{}, fmt.Errorf("%w: no search results for %q", ytdlp.ErrMediaNotFound, rawInput)
		}
		target = results[0].URL
		resolvedPlatform = searchPlatform
	} else {
		validated, err := ytdlp.ValidateURL(rawInput)
		if err != nil {
			return ytdlp.DownloadOptions{}, err
		}
		target = validated
		detected := ytdlp.DetectPlatform(target)
		if expectedPlatform != model.PlatformUnknown && expectedPlatform != model.PlatformGeneric && detected != expectedPlatform {
			return ytdlp.DownloadOptions{}, fmt.Errorf(
				"%w: expected %s url, got %s",
				ytdlp.ErrUnsupportedPlatform,
				expectedPlatform.String(),
				detected.String(),
			)
		}
		resolvedPlatform = detected
	}

	mediaType := strings.ToLower(strings.TrimSpace(req.Type))
	if mediaType != "audio" {
		mediaType = "video"
	}

	waCompat := true
	if req.WhatsAppCompatible != nil {
		waCompat = *req.WhatsAppCompatible
	}

	embedThumb := true
	if req.EmbedThumbnail != nil {
		embedThumb = *req.EmbedThumbnail
	}

	return ytdlp.DownloadOptions{
		Target:             target,
		Platform:           resolvedPlatform,
		MediaType:          mediaType,
		Quality:            req.Quality,
		AudioFormat:        req.AudioFormat,
		WhatsAppCompatible: waCompat,
		EmbedThumbnail:     embedThumb,
		MaxFilesize:        req.MaxFilesize,
		Proxy:              req.Proxy,
	}, nil
}

func (d *Downloader) buildDownloadResult(
	baseURL string,
	platform model.Platform,
	metaList []model.MediaMetadata,
	filePaths []string,
) (model.DownloadResult, error) {
	cleanBase := strings.TrimRight(baseURL, "/")
	files := make([]model.DownloadedFile, 0, len(filePaths))

	for _, path := range filePaths {
		stored, err := d.storage.RegisterFile(path)
		if err != nil {
			return model.DownloadResult{}, fmt.Errorf("registering file %q: %w", path, err)
		}
		files = append(files, model.DownloadedFile{
			FileID:      stored.ID,
			Filename:    stored.Filename,
			Extension:   stored.Extension,
			MimeType:    stored.MimeType,
			SizeBytes:   stored.SizeBytes,
			DownloadURL: fmt.Sprintf("%s/api/v1/files/%s", cleanBase, stored.ID),
			ExpiresAt:   stored.ExpiresAt,
		})
	}

	meta := pickPrimaryMetadata(metaList, platform)
	return model.DownloadResult{
		Platform: platform.String(),
		Metadata: meta,
		Files:    files,
	}, nil
}

func pickPrimaryMetadata(metaList []model.MediaMetadata, platform model.Platform) model.MediaMetadata {
	if len(metaList) > 0 {
		meta := metaList[0]
		if meta.Platform == "" || meta.Platform == model.PlatformUnknown.String() {
			meta.Platform = platform.String()
		}
		return meta
	}
	return model.MediaMetadata{
		Platform: platform.String(),
		Formats:  []model.FormatOption{},
	}
}

// IsClientError reports whether err represents a 4xx client validation/upstream error.
func IsClientError(err error) bool {
	return errors.Is(err, ytdlp.ErrInvalidURL) ||
		errors.Is(err, ytdlp.ErrUnsupportedPlatform) ||
		errors.Is(err, ytdlp.ErrMediaNotFound) ||
		errors.Is(err, ytdlp.ErrMediaTooLarge) ||
		errors.Is(err, storage.ErrFileNotFound) ||
		errors.Is(err, job.ErrJobNotFound)
}
