package ytdlp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/justpiple/ytdlp-api/internal/config"
	"github.com/justpiple/ytdlp-api/internal/model"
)

// ProgressCallback receives parsed download and post-processing progress events.
type ProgressCallback func(event model.ProgressEvent)

// Runner executes yt-dlp and ffmpeg operations.
type Runner interface {
	GetInfo(ctx context.Context, target string, platform model.Platform, includeFormats bool, proxyOverride string) (model.MediaMetadata, error)
	Search(ctx context.Context, platform model.Platform, query string, limit int, proxyOverride string) ([]model.SearchResult, error)
	Download(ctx context.Context, opts DownloadOptions, onProgress ProgressCallback) ([]model.MediaMetadata, []string, error)
	Versions(ctx context.Context) (ytdlpVer string, ffmpegVer string, err error)
}

// ExecRunner implements Runner by invoking local yt-dlp and ffmpeg binaries.
type ExecRunner struct {
	cfg config.Config
}

var _ Runner = (*ExecRunner)(nil)

// NewExecRunner creates a Runner backed by OS process execution.
func NewExecRunner(cfg config.Config) *ExecRunner {
	return &ExecRunner{cfg: cfg}
}

// GetInfo extracts media metadata and optional format options without downloading files.
func (r *ExecRunner) GetInfo(
	ctx context.Context,
	target string,
	platform model.Platform,
	includeFormats bool,
	proxyOverride string,
) (model.MediaMetadata, error) {
	args := BuildInfoArgs(r.cfg, target, platform, proxyOverride)
	stdout, stderr, err := r.runSimple(ctx, r.cfg.YTDLPPath, args...)
	if err != nil {
		return model.MediaMetadata{}, classifyError(stderr, err)
	}

	raw, err := parseRawInfo(stdout)
	if err != nil {
		return model.MediaMetadata{}, fmt.Errorf("parsing yt-dlp info json: %w", err)
	}

	return raw.toMetadata(platform, includeFormats), nil
}

// Search queries YouTube or SoundCloud via yt-dlp flat playlist search.
func (r *ExecRunner) Search(
	ctx context.Context,
	platform model.Platform,
	query string,
	limit int,
	proxyOverride string,
) ([]model.SearchResult, error) {
	args, err := BuildSearchArgs(r.cfg, platform, query, limit, proxyOverride)
	if err != nil {
		return nil, err
	}

	stdout, stderr, err := r.runSimple(ctx, r.cfg.YTDLPPath, args...)
	if err != nil {
		return nil, classifyError(stderr, err)
	}

	raw, err := parseRawInfo(stdout)
	if err != nil {
		return nil, fmt.Errorf("parsing yt-dlp search json: %w", err)
	}

	results := make([]model.SearchResult, 0, len(raw.Entries))
	for _, entry := range raw.Entries {
		entryURL := entry.WebpageURL
		if entryURL == "" {
			entryURL = entry.URL
		}
		if entryURL == "" && platform == model.PlatformYouTube && entry.ID != "" {
			entryURL = "https://www.youtube.com/watch?v=" + entry.ID
		}
		results = append(results, model.SearchResult{
			ID:        entry.ID,
			Title:     entry.Title,
			URL:       entryURL,
			Uploader:  firstNonEmpty(entry.Uploader, entry.Channel),
			Duration:  entry.Duration,
			Thumbnail: entry.bestThumbnail(),
			ViewCount: entry.ViewCount,
			Platform:  platform.String(),
		})
	}

	return results, nil
}

// Download executes yt-dlp to download and post-process media into opts.OutputDir,
// streaming live progress events through onProgress when provided.
func (r *ExecRunner) Download(
	ctx context.Context,
	opts DownloadOptions,
	onProgress ProgressCallback,
) ([]model.MediaMetadata, []string, error) {
	args := BuildDownloadArgs(r.cfg, opts)
	cmd := exec.CommandContext(ctx, r.cfg.YTDLPPath, args...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("creating stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("creating stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("starting yt-dlp: %w", err)
	}

	var stderrBuf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&stderrBuf, stderrPipe)
	}()

	metadataList := make([]model.MediaMetadata, 0, 1)
	scanner := bufio.NewScanner(stdoutPipe)
	// Increase scanner buffer to handle large yt-dlp single-line JSON outputs.
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if ev, ok := ParseProgressLine(line); ok {
			if onProgress != nil {
				onProgress(ev)
			}
			continue
		}

		if strings.HasPrefix(line, "{") {
			raw, parseErr := parseRawInfo([]byte(line))
			if parseErr == nil {
				metadataList = append(metadataList, raw.toMetadata(opts.Platform, false))
			}
		}
	}

	scanErr := scanner.Err()
	wg.Wait()
	waitErr := cmd.Wait()

	if scanErr != nil && waitErr == nil {
		return nil, nil, fmt.Errorf("reading yt-dlp stdout: %w", scanErr)
	}
	if waitErr != nil {
		// yt-dlp exits with status 101 when --max-downloads limit is reached cleanly on playlists.
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 101 && len(metadataList) > 0 {
			// Treat max-downloads truncation as partial success.
		} else {
			return nil, nil, classifyError(stderrBuf.String(), waitErr)
		}
	}

	files, err := collectDownloadedFiles(opts.OutputDir)
	if err != nil {
		return nil, nil, fmt.Errorf("collecting output files: %w", err)
	}
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("%w: no output file produced", ErrMediaNotFound)
	}

	return metadataList, files, nil
}

// Versions checks the installed versions of yt-dlp and ffmpeg.
func (r *ExecRunner) Versions(ctx context.Context) (string, string, error) {
	ytOut, _, err := r.runSimple(ctx, r.cfg.YTDLPPath, "--version")
	if err != nil {
		return "", "", fmt.Errorf("checking yt-dlp version: %w", err)
	}

	ffOut, _, err := r.runSimple(ctx, r.cfg.FFmpegPath, "-version")
	if err != nil {
		return strings.TrimSpace(string(ytOut)), "", fmt.Errorf("checking ffmpeg version: %w", err)
	}

	ffFirstLine, _, _ := strings.Cut(string(ffOut), "\n")
	return strings.TrimSpace(string(ytOut)), strings.TrimSpace(ffFirstLine), nil
}

func (r *ExecRunner) runSimple(ctx context.Context, bin string, args ...string) ([]byte, string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.Bytes(), stderr.String(), err
}

// ParseProgressLine parses yt-dlp progress and post-processing status lines.
func ParseProgressLine(line string) (model.ProgressEvent, bool) {
	if strings.HasPrefix(line, "YTDLP_PROGRESS|") {
		parts := strings.Split(line, "|")
		if len(parts) < 6 {
			return model.ProgressEvent{}, false
		}

		percentStr := strings.TrimSpace(strings.TrimSuffix(parts[1], "%"))
		var percent float64
		if parsed, err := strconv.ParseFloat(percentStr, 64); err == nil {
			percent = parsed
		}
		var downloaded int64
		if parsed, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64); err == nil {
			downloaded = parsed
		}
		var total int64
		if parsed, err := strconv.ParseInt(strings.TrimSpace(parts[3]), 10, 64); err == nil {
			total = parsed
		}

		return model.ProgressEvent{
			Status:          model.JobStatusDownloading.String(),
			ProgressPercent: percent,
			DownloadedBytes: downloaded,
			TotalBytes:      total,
			Speed:           cleanPlaceholder(parts[4]),
			ETA:             cleanPlaceholder(parts[5]),
			Message:         "downloading media",
		}, true
	}

	if strings.HasPrefix(line, "[ExtractAudio]") || strings.HasPrefix(line, "[Merger]") || strings.HasPrefix(line, "[EmbedThumbnail]") {
		return model.ProgressEvent{
			Status:          model.JobStatusPostProcessing.String(),
			ProgressPercent: 99,
			Message:         "processing media with ffmpeg",
		}, true
	}

	return model.ProgressEvent{}, false
}

func cleanPlaceholder(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" || trimmed == "NA" || trimmed == "Unknown" {
		return ""
	}
	return trimmed
}

func collectDownloadedFiles(dir string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		return nil, fmt.Errorf("globbing output dir: %w", err)
	}

	files := make([]string, 0, len(matches))
	for _, path := range matches {
		ext := strings.ToLower(filepath.Ext(path))
		// Skip temporary partial or sidecar files left by yt-dlp.
		if ext == ".part" || ext == ".ytdl" || ext == ".temp" {
			continue
		}
		files = append(files, path)
	}
	return files, nil
}

func classifyError(stderr string, err error) error {
	lower := strings.ToLower(stderr)
	switch {
	case strings.Contains(lower, "file is larger than max-filesize"),
		strings.Contains(lower, "max-filesize"):
		return fmt.Errorf("%w: %s", ErrMediaTooLarge, summarizeStderr(stderr))
	case strings.Contains(lower, "sign in"),
		strings.Contains(lower, "cookies"),
		strings.Contains(lower, "login required"),
		strings.Contains(lower, "private video"),
		strings.Contains(lower, "http error 403"),
		strings.Contains(lower, "http error 429"):
		return fmt.Errorf("%w: %s", ErrUpstreamBlocked, summarizeStderr(stderr))
	case strings.Contains(lower, "video unavailable"),
		strings.Contains(lower, "not available"),
		strings.Contains(lower, "404"):
		return fmt.Errorf("%w: %s", ErrMediaNotFound, summarizeStderr(stderr))
	default:
		msg := summarizeStderr(stderr)
		if msg == "" {
			return fmt.Errorf("yt-dlp execution failed: %w", err)
		}
		return fmt.Errorf("yt-dlp execution failed (%s): %w", msg, err)
	}
}

func summarizeStderr(stderr string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return strings.TrimPrefix(line, "ERROR: ")
		}
	}
	return ""
}

type rawThumbnail struct {
	URL string `json:"url"`
}

type rawFormat struct {
	FormatID       string  `json:"format_id"`
	Ext            string  `json:"ext"`
	Resolution     string  `json:"resolution"`
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	FPS            float64 `json:"fps"`
	VCodec         string  `json:"vcodec"`
	ACodec         string  `json:"acodec"`
	Filesize       int64   `json:"filesize"`
	FilesizeApprox int64   `json:"filesize_approx"`
	TBR            float64 `json:"tbr"`
	URL            string  `json:"url"`
}

type rawYTDLPInfo struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Uploader    string         `json:"uploader"`
	Channel     string         `json:"channel"`
	UploaderURL string         `json:"uploader_url"`
	ChannelURL  string         `json:"channel_url"`
	Duration    float64        `json:"duration"`
	Thumbnail   string         `json:"thumbnail"`
	Thumbnails  []rawThumbnail `json:"thumbnails"`
	WebpageURL  string         `json:"webpage_url"`
	URL         string         `json:"url"`
	Extractor   string         `json:"extractor_key"`
	ViewCount   int64          `json:"view_count"`
	LikeCount   int64          `json:"like_count"`
	UploadDate  string         `json:"upload_date"`
	IsLive      bool           `json:"is_live"`
	Formats     []rawFormat    `json:"formats"`
	Entries     []rawYTDLPInfo `json:"entries"`
}

func parseRawInfo(data []byte) (rawYTDLPInfo, error) {
	var raw rawYTDLPInfo
	if err := json.Unmarshal(data, &raw); err != nil {
		return rawYTDLPInfo{}, err
	}
	return raw, nil
}

func (r rawYTDLPInfo) bestThumbnail() string {
	if r.Thumbnail != "" {
		return r.Thumbnail
	}
	if len(r.Thumbnails) > 0 {
		return r.Thumbnails[len(r.Thumbnails)-1].URL
	}
	return ""
}

func (r rawYTDLPInfo) toMetadata(platform model.Platform, includeFormats bool) model.MediaMetadata {
	source := r
	if source.Title == "" && len(source.Entries) > 0 {
		source = source.Entries[0]
	}

	resolvedPlatform := platform
	if resolvedPlatform == model.PlatformUnknown {
		resolvedPlatform = DetectPlatform(firstNonEmpty(source.WebpageURL, source.URL))
	}

	meta := model.MediaMetadata{
		ID:          source.ID,
		Title:       source.Title,
		Description: source.Description,
		Uploader:    firstNonEmpty(source.Uploader, source.Channel),
		UploaderURL: firstNonEmpty(source.UploaderURL, source.ChannelURL),
		Duration:    source.Duration,
		Thumbnail:   source.bestThumbnail(),
		WebpageURL:  firstNonEmpty(source.WebpageURL, source.URL),
		Extractor:   strings.ToLower(source.Extractor),
		Platform:    resolvedPlatform.String(),
		ViewCount:   source.ViewCount,
		LikeCount:   source.LikeCount,
		UploadDate:  source.UploadDate,
		IsLive:      source.IsLive,
		Formats:     []model.FormatOption{},
	}

	if includeFormats && len(source.Formats) > 0 {
		formats := make([]model.FormatOption, 0, len(source.Formats))
		for _, f := range source.Formats {
			size := f.Filesize
			if size == 0 {
				size = f.FilesizeApprox
			}
			formats = append(formats, model.FormatOption{
				FormatID:   f.FormatID,
				Extension:  f.Ext,
				Resolution: f.Resolution,
				Width:      f.Width,
				Height:     f.Height,
				FPS:        f.FPS,
				VCodec:     cleanCodec(f.VCodec),
				ACodec:     cleanCodec(f.ACodec),
				Filesize:   size,
				TBR:        f.TBR,
				DirectURL:  f.URL,
			})
		}
		meta.Formats = formats
	}

	return meta
}

func cleanCodec(codec string) string {
	if codec == "none" {
		return ""
	}
	return codec
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
