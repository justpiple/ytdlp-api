package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/justpiple/ytdlp-api/internal/config"
	"github.com/justpiple/ytdlp-api/internal/gallerydl"
	"github.com/justpiple/ytdlp-api/internal/job"
	"github.com/justpiple/ytdlp-api/internal/model"
	"github.com/justpiple/ytdlp-api/internal/storage"
	"github.com/justpiple/ytdlp-api/internal/threads"
	"github.com/justpiple/ytdlp-api/internal/ytdlp"
)

const (
	engineYTDLP     = "yt-dlp"
	engineGalleryDL = "gallery-dl"
	engineThreads   = "threads"
)

// Downloader orchestrates info extraction, search, sync/async downloads, and health checks.
type Downloader struct {
	cfg     config.Config
	runner  ytdlp.Runner
	gallery gallerydl.Runner
	threads threads.Runner
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
	gallery gallerydl.Runner,
	threadsRunner threads.Runner,
	storageMgr *storage.Manager,
	jobMgr *job.Manager,
) *Downloader {
	return &Downloader{
		cfg:     cfg,
		runner:  runner,
		gallery: gallery,
		threads: threadsRunner,
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

	effectiveProxy := strings.TrimSpace(req.Proxy)
	if effectiveProxy == "" {
		effectiveProxy = d.cfg.ProxyURL
	}
	targetURL = ytdlp.ResolveShortURL(ctx, targetURL, effectiveProxy)

	platform := ytdlp.DetectPlatform(targetURL)
	runCtx, cancel := context.WithTimeout(ctx, d.cfg.InfoTimeout)
	defer cancel()

	if platform == model.PlatformThreads && d.threads != nil {
		meta, err := d.threads.GetInfo(runCtx, targetURL, req.Proxy)
		if err != nil {
			return model.MediaMetadata{}, fmt.Errorf("extracting info: %w", err)
		}
		return meta, nil
	}

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

	runCtx, cancel := context.WithTimeout(ctx, d.cfg.DownloadTimeout)
	defer cancel()

	engine, metaList, filePaths, err := d.fetchMedia(runCtx, jobDir, opts, nil)
	if err != nil {
		d.storage.RemoveDir(jobDir)
		return model.DownloadResult{}, fmt.Errorf("downloading media: %w", err)
	}

	result, err := d.buildDownloadResult(baseURL, opts.Platform, engine, metaList, filePaths)
	if err != nil {
		d.storage.RemoveDir(jobDir)
		return model.DownloadResult{}, err
	}

	return result, nil
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

	d.jobs.Update(jobID, model.ProgressEvent{
		Status:  model.JobStatusDownloading.String(),
		Message: "starting download",
	})

	engine, metaList, filePaths, err := d.fetchMedia(jobCtx, jobDir, opts, func(ev model.ProgressEvent) {
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

	result, err := d.buildDownloadResult(baseURL, opts.Platform, engine, metaList, filePaths)
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

func (d *Downloader) fetchMedia(
	ctx context.Context,
	jobDir string,
	opts ytdlp.DownloadOptions,
	onProgress ytdlp.ProgressCallback,
) (string, []model.MediaMetadata, []string, error) {
	engine, metaList, filePaths, err := d.fetchRawMedia(ctx, jobDir, opts, onProgress)
	if err != nil {
		return "", nil, nil, err
	}

	if opts.H264Compatible && opts.MediaType != model.MediaKindAudio {
		filePaths, err = ytdlp.EnsureH264CompatibleFiles(ctx, d.cfg.FFmpegPath, d.cfg.FFmpegPreset, filePaths, onProgress)
		if err != nil {
			return "", nil, nil, fmt.Errorf("ensuring h264 compatibility: %w", err)
		}
	}

	return engine, metaList, filePaths, nil
}

func (d *Downloader) fetchRawMedia(
	ctx context.Context,
	jobDir string,
	opts ytdlp.DownloadOptions,
	onProgress ytdlp.ProgressCallback,
) (string, []model.MediaMetadata, []string, error) {
	if opts.Platform == model.PlatformThreads && d.threads != nil {
		thMeta, thFiles, err := d.runThreads(ctx, jobDir, opts, onProgress)
		if err != nil {
			return "", nil, nil, err
		}
		if len(thFiles) == 0 {
			return "", nil, nil, fmt.Errorf("%w: no output file produced", ytdlp.ErrMediaNotFound)
		}
		return engineThreads, []model.MediaMetadata{thMeta}, thFiles, nil
	}

	if opts.MediaType != "auto" || d.gallery == nil {
		metaList, files, err := d.runYTDLP(ctx, jobDir, opts, onProgress)
		if err != nil {
			return "", nil, nil, err
		}
		if len(files) == 0 {
			return "", nil, nil, fmt.Errorf("%w: no output file produced", ytdlp.ErrMediaNotFound)
		}
		return engineYTDLP, metaList, files, nil
	}

	galleryFirst := preferGalleryFirst(opts.Platform, opts.Target)
	primaryEngine, primaryMeta, primaryFiles, primaryErr := d.runEngine(ctx, jobDir, opts, onProgress, galleryFirst)
	if primaryErr == nil && len(primaryFiles) > 0 {
		// TikTok photo slideshows cause yt-dlp to download only background music without video/images.
		// Recover images via gallery-dl when yt-dlp returns audio-only for a TikTok URL in auto mode.
		if !galleryFirst && opts.Platform == model.PlatformTikTok && isAllAudioFiles(primaryFiles) {
			if gdMeta, gdFiles, ok := d.tryTikTokSlideshowFallback(ctx, jobDir, opts, primaryFiles, onProgress); ok {
				return engineGalleryDL, []model.MediaMetadata{gdMeta}, gdFiles, nil
			}
		}
		return primaryEngine, primaryMeta, primaryFiles, nil
	}

	if primaryErr == nil {
		primaryErr = fmt.Errorf("%w: no output file produced", ytdlp.ErrMediaNotFound)
	}
	if !shouldFallback(ctx, primaryErr) {
		return "", nil, nil, primaryErr
	}

	clearDirContents(jobDir)
	fallbackEngine, fallbackMeta, fallbackFiles, fallbackErr := d.runEngine(ctx, jobDir, opts, onProgress, !galleryFirst)
	if fallbackErr == nil && len(fallbackFiles) > 0 {
		return fallbackEngine, fallbackMeta, fallbackFiles, nil
	}
	if fallbackErr != nil && !shouldFallback(ctx, fallbackErr) {
		return "", nil, nil, fallbackErr
	}
	return "", nil, nil, primaryErr
}

func (d *Downloader) runEngine(
	ctx context.Context,
	outputDir string,
	opts ytdlp.DownloadOptions,
	onProgress ytdlp.ProgressCallback,
	useGalleryDL bool,
) (string, []model.MediaMetadata, []string, error) {
	if useGalleryDL {
		gdMeta, gdFiles, err := d.runGalleryDL(ctx, outputDir, opts, onProgress)
		if err != nil {
			return "", nil, nil, err
		}
		return engineGalleryDL, []model.MediaMetadata{gdMeta}, gdFiles, nil
	}
	ytMeta, ytFiles, err := d.runYTDLP(ctx, outputDir, opts, onProgress)
	if err != nil {
		return "", nil, nil, err
	}
	return engineYTDLP, ytMeta, ytFiles, nil
}

func (d *Downloader) runYTDLP(
	ctx context.Context,
	outputDir string,
	opts ytdlp.DownloadOptions,
	onProgress ytdlp.ProgressCallback,
) ([]model.MediaMetadata, []string, error) {
	runOpts := opts
	runOpts.OutputDir = outputDir
	return d.runner.Download(ctx, runOpts, onProgress)
}

func (d *Downloader) runGalleryDL(
	ctx context.Context,
	outputDir string,
	opts ytdlp.DownloadOptions,
	onProgress ytdlp.ProgressCallback,
) (model.MediaMetadata, []string, error) {
	return d.gallery.Download(ctx, gallerydl.Options{
		Target:      opts.Target,
		Platform:    opts.Platform,
		MaxFilesize: opts.MaxFilesize,
		MaxItems:    d.cfg.MaxPlaylistItems,
		Proxy:       opts.Proxy,
		OutputDir:   outputDir,
	}, onProgress)
}

func (d *Downloader) runThreads(
	ctx context.Context,
	outputDir string,
	opts ytdlp.DownloadOptions,
	onProgress ytdlp.ProgressCallback,
) (model.MediaMetadata, []string, error) {
	return d.threads.Download(ctx, threads.Options{
		Target:      opts.Target,
		MediaType:   opts.MediaType,
		AudioFormat: opts.AudioFormat,
		MaxFilesize: opts.MaxFilesize,
		MaxItems:    d.cfg.MaxPlaylistItems,
		Proxy:       opts.Proxy,
		OutputDir:   outputDir,
	}, onProgress)
}

func (d *Downloader) tryTikTokSlideshowFallback(
	ctx context.Context,
	jobDir string,
	opts ytdlp.DownloadOptions,
	ytFiles []string,
	onProgress ytdlp.ProgressCallback,
) (model.MediaMetadata, []string, bool) {
	gdDir := filepath.Join(jobDir, "gd")
	if err := os.MkdirAll(gdDir, 0o750); err != nil {
		return model.MediaMetadata{}, nil, false
	}

	gdMeta, gdFiles, gdErr := d.runGalleryDL(ctx, gdDir, opts, onProgress)
	if gdErr != nil || !hasAnyImageFile(gdFiles) {
		_ = os.RemoveAll(gdDir)
		return model.MediaMetadata{}, nil, false
	}

	moved := make([]string, 0, len(gdFiles))
	for _, src := range gdFiles {
		dst := filepath.Join(jobDir, "gd_"+filepath.Base(src))
		if err := os.Rename(src, dst); err != nil {
			_ = os.RemoveAll(gdDir)
			for _, m := range moved {
				_ = os.Remove(m)
			}
			return model.MediaMetadata{}, nil, false
		}
		moved = append(moved, dst)
	}

	for _, old := range ytFiles {
		_ = os.Remove(old)
	}
	_ = os.RemoveAll(gdDir)
	return gdMeta, moved, true
}

func preferGalleryFirst(platform model.Platform, target string) bool {
	switch platform {
	case model.PlatformInstagram, model.PlatformThreads, model.PlatformTwitter, model.PlatformPinterest:
		return true
	case model.PlatformTikTok:
		return ytdlp.IsTikTokPhotoURL(target)
	default:
		return false
	}
}

func shouldFallback(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if errors.Is(err, ytdlp.ErrUpstreamBlocked) || errors.Is(err, ytdlp.ErrMediaTooLarge) {
		return false
	}
	return true
}

func isAllAudioFiles(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		if classifyFilePathKind(p) != model.MediaKindAudio {
			return false
		}
	}
	return true
}

func hasAnyImageFile(paths []string) bool {
	for _, p := range paths {
		if classifyFilePathKind(p) == model.MediaKindImage {
			return true
		}
	}
	return false
}

func classifyFilePathKind(path string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	switch ext {
	case "mp3", "m4a", "opus", "ogg", "wav", "aac", "flac", "weba":
		return model.MediaKindAudio
	case "jpg", "jpeg", "png", "webp", "gif", "heic", "avif":
		return model.MediaKindImage
	case "mp4", "webm", "mkv", "mov":
		return model.MediaKindVideo
	default:
		return model.MediaKindOther
	}
}

func clearDirContents(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		_ = os.RemoveAll(filepath.Join(dir, entry.Name()))
	}
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

	ytVer, ffVer, ytErr := d.runner.Versions(checkCtx)

	var gdVer string
	var gdErr error
	if d.gallery != nil {
		gdVer, gdErr = d.gallery.Version(checkCtx)
	}

	joinedErr := errors.Join(ytErr, gdErr)
	status := "ok"
	if joinedErr != nil {
		status = "degraded"
	}

	return model.HealthResponse{
		Status:           status,
		YTDLPVersion:     ytVer,
		GalleryDLVersion: gdVer,
		FFmpegVersion:    ffVer,
		ActiveJobs:       d.jobs.ActiveCount(),
		MaxConcurrent:    d.jobs.MaxConcurrent(),
		StoredFiles:      d.storage.Count(),
	}, joinedErr
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
			Features:   []string{"auto", "video", "audio", "carousel"},
			CookiesKey: "tiktok.txt",
		},
		{
			Name:       model.PlatformInstagram.String(),
			Display:    "Instagram",
			Features:   []string{"auto", "video", "image", "audio", "carousel", "reels", "stories"},
			CookiesKey: "instagram.txt",
		},
		{
			Name:       model.PlatformThreads.String(),
			Display:    "Threads",
			Features:   []string{"auto", "video", "image", "audio", "carousel"},
			CookiesKey: "threads.txt",
		},
		{
			Name:       model.PlatformTwitter.String(),
			Display:    "X (Twitter)",
			Features:   []string{"auto", "video", "image", "audio", "gif", "carousel"},
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
			Features:   []string{"auto", "video", "image", "audio", "reels"},
			CookiesKey: "facebook.txt",
		},
		{
			Name:       model.PlatformPinterest.String(),
			Display:    "Pinterest",
			Features:   []string{"auto", "image", "video", "gif", "carousel"},
			CookiesKey: "pinterest.txt",
		},
		{
			Name:       model.PlatformGeneric.String(),
			Display:    "Universal (yt-dlp + gallery-dl supported sites)",
			Features:   []string{"auto", "video", "audio", "image", "carousel", "info"},
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
		initialDetected := ytdlp.DetectPlatform(validated)
		if expectedPlatform != model.PlatformUnknown && expectedPlatform != model.PlatformGeneric && initialDetected != expectedPlatform {
			return ytdlp.DownloadOptions{}, fmt.Errorf(
				"%w: expected %s url, got %s",
				ytdlp.ErrUnsupportedPlatform,
				expectedPlatform.String(),
				initialDetected.String(),
			)
		}

		effectiveProxy := strings.TrimSpace(req.Proxy)
		if effectiveProxy == "" {
			effectiveProxy = d.cfg.ProxyURL
		}
		target = ytdlp.ResolveShortURL(ctx, validated, effectiveProxy)
		resolvedPlatform = ytdlp.DetectPlatform(target)
		if resolvedPlatform == model.PlatformUnknown {
			resolvedPlatform = initialDetected
		}
		if expectedPlatform != model.PlatformUnknown && expectedPlatform != model.PlatformGeneric && resolvedPlatform == model.PlatformGeneric {
			resolvedPlatform = expectedPlatform
		}
	}

	mediaType := resolveMediaType(req.Type, resolvedPlatform)

	h264Compat := true
	if req.H264Compatible != nil {
		h264Compat = *req.H264Compatible
	}

	embedThumb := true
	if req.EmbedThumbnail != nil {
		embedThumb = *req.EmbedThumbnail
	}

	return ytdlp.DownloadOptions{
		Target:         target,
		Platform:       resolvedPlatform,
		MediaType:      mediaType,
		Quality:        req.Quality,
		AudioFormat:    req.AudioFormat,
		H264Compatible: h264Compat,
		EmbedThumbnail: embedThumb,
		MaxFilesize:    req.MaxFilesize,
		Proxy:          req.Proxy,
	}, nil
}

func resolveMediaType(requested string, platform model.Platform) string {
	mediaType := strings.ToLower(strings.TrimSpace(requested))
	switch {
	case platform == model.PlatformSoundCloud:
		return model.MediaKindAudio
	case platform == model.PlatformYouTube:
		if mediaType == model.MediaKindAudio {
			return model.MediaKindAudio
		}
		return model.MediaKindVideo
	default:
		if mediaType == "" {
			return "auto"
		}
		return mediaType
	}
}

func (d *Downloader) buildDownloadResult(
	baseURL string,
	platform model.Platform,
	engine string,
	metaList []model.MediaMetadata,
	filePaths []string,
) (model.DownloadResult, error) {
	cleanBase := strings.TrimRight(baseURL, "/")
	files := make([]model.DownloadedFile, 0, len(filePaths))

	for i, path := range filePaths {
		stored, err := d.storage.RegisterFile(path)
		if err != nil {
			return model.DownloadResult{}, fmt.Errorf("registering file %q: %w", path, err)
		}
		files = append(files, model.DownloadedFile{
			Index:       i + 1,
			MediaType:   storage.MediaKindFromMIME(stored.MimeType),
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
		Platform:  platform.String(),
		MediaType: summarizeResultMediaType(files),
		Count:     len(files),
		Engine:    engine,
		Metadata:  meta,
		Files:     files,
	}, nil
}

func summarizeResultMediaType(files []model.DownloadedFile) string {
	if len(files) > 1 {
		return model.MediaKindCarousel
	}
	if len(files) == 1 {
		return files[0].MediaType
	}
	return model.MediaKindOther
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
