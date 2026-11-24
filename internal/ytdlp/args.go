package ytdlp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/justpiple/ytdlp-api/internal/config"
	"github.com/justpiple/ytdlp-api/internal/model"
)

const progressTemplate = "download:YTDLP_PROGRESS|%(progress._percent_str)s|%(progress.downloaded_bytes)s|%(progress.total_bytes)s|%(progress._speed_str)s|%(progress._eta_str)s"

// DownloadOptions configures how yt-dlp downloads and post-processes media.
type DownloadOptions struct {
	Target         string
	Platform       model.Platform
	MediaType      string // "video" or "audio"
	Quality        string // "best", "1080p", "720p", "480p", "360p", "worst"
	AudioFormat    string // "mp3", "m4a", "opus", "wav"
	H264Compatible bool
	EmbedThumbnail bool
	MaxFilesize    string
	Proxy          string
	OutputDir      string
}

// BuildInfoArgs constructs CLI arguments for extracting JSON metadata without downloading.
func BuildInfoArgs(cfg config.Config, target string, platform model.Platform, proxyOverride string) []string {
	args := baseArgs(cfg, platform, proxyOverride)
	args = append(
		args,
		"--dump-single-json",
		"--no-download",
		"--no-playlist",
		target,
	)
	return args
}

// BuildSearchArgs constructs CLI arguments for searching YouTube or SoundCloud.
func BuildSearchArgs(cfg config.Config, platform model.Platform, query string, limit int, proxyOverride string) ([]string, error) {
	prefix := ""
	switch platform {
	case model.PlatformYouTube:
		prefix = "ytsearch"
	case model.PlatformSoundCloud:
		prefix = "scsearch"
	default:
		return nil, fmt.Errorf("%w: search is only supported for youtube and soundcloud", ErrUnsupportedPlatform)
	}

	if limit <= 0 {
		limit = 5
	}
	if limit > 25 {
		limit = 25
	}

	sanitizedQuery := strings.TrimSpace(query)
	if sanitizedQuery == "" {
		return nil, fmt.Errorf("%w: search query is empty", ErrInvalidURL)
	}

	args := baseArgs(cfg, platform, proxyOverride)
	searchTarget := fmt.Sprintf("%s%d:%s", prefix, limit, sanitizedQuery)
	args = append(
		args,
		"--dump-single-json",
		"--flat-playlist",
		"--no-download",
		searchTarget,
	)
	return args, nil
}

// BuildDownloadArgs constructs CLI arguments for downloading media into OutputDir.
func BuildDownloadArgs(cfg config.Config, opts DownloadOptions) []string {
	args := baseArgs(cfg, opts.Platform, opts.Proxy)

	outputTemplate := filepath.Join(opts.OutputDir, "%(autonumber)02d_%(id)s.%(ext)s")
	args = append(
		args,
		"--print-json",
		"--no-simulate",
		"--newline",
		"--progress-template", progressTemplate,
		"--restrict-filenames",
		"--max-downloads", fmt.Sprintf("%d", cfg.MaxPlaylistItems),
		"--playlist-items", fmt.Sprintf("1-%d", cfg.MaxPlaylistItems),
		"-o", outputTemplate,
	)

	maxSize := strings.TrimSpace(opts.MaxFilesize)
	if maxSize == "" {
		maxSize = cfg.MaxFilesize
	}
	if maxSize != "" {
		args = append(args, "--max-filesize", maxSize)
	}

	if opts.MediaType == "audio" {
		audioFormat := normalizeAudioFormat(opts.AudioFormat)
		args = append(
			args,
			"-f", "bestaudio/best",
			"-x",
			"--audio-format", audioFormat,
			"--audio-quality", "0",
			"--add-metadata",
		)
		if opts.EmbedThumbnail && (audioFormat == "mp3" || audioFormat == "m4a") {
			args = append(args, "--embed-thumbnail", "--convert-thumbnails", "jpg")
		}
	} else {
		var formatSelector string
		if opts.MediaType == model.MediaKindOther || opts.MediaType == "auto" {
			formatSelector = adaptiveFormatSelector(opts.Quality, opts.H264Compatible)
		} else {
			formatSelector = videoFormatSelector(opts.Quality, opts.H264Compatible)
		}
		args = append(args, "-f", formatSelector)
		if opts.H264Compatible {
			// Prefer H.264 (avc1) + AAC (m4a) for universal inline playback across mobile and web players.
			args = append(args, "-S", "vcodec:h264,fps,res,acodec:m4a", "--merge-output-format", "mp4")
		} else {
			args = append(args, "--merge-output-format", "mp4")
		}
	}

	args = append(args, opts.Target)
	return args
}

func baseArgs(cfg config.Config, platform model.Platform, proxyOverride string) []string {
	args := []string{
		"--no-warnings",
		"--encoding", "utf-8",
	}

	if ffmpegLoc := resolveFFmpegLocation(cfg.FFmpegPath); ffmpegLoc != "" {
		args = append(args, "--ffmpeg-location", ffmpegLoc)
	}

	proxy := strings.TrimSpace(proxyOverride)
	if proxy == "" {
		proxy = cfg.ProxyURL
	}
	if proxy != "" {
		args = append(args, "--proxy", proxy)
	}

	if cookiePath := ResolveCookiesFile(cfg, platform); cookiePath != "" {
		args = append(args, "--cookies", cookiePath)
	}

	return args
}

// resolveFFmpegLocation resolves bare executable names (e.g. "ffmpeg") via PATH
// because yt-dlp's --ffmpeg-location requires an existing filesystem path and fails
// when given an unresolved binary name.
func resolveFFmpegLocation(ffmpegPath string) string {
	trimmed := strings.TrimSpace(ffmpegPath)
	if trimmed == "" {
		return ""
	}
	if trimmed != "." && trimmed != ".." && !strings.ContainsAny(trimmed, `/\`) {
		resolved, err := exec.LookPath(trimmed)
		if err != nil {
			return ""
		}
		return resolved
	}
	return trimmed
}

// ResolveCookiesFile finds the most specific Netscape cookies file for platform,
// falling back to cfg.CookiesFile if no platform file exists.
func ResolveCookiesFile(cfg config.Config, platform model.Platform) string {
	if cfg.CookiesDir != "" && platform != model.PlatformUnknown && platform != model.PlatformGeneric {
		candidate := filepath.Join(cfg.CookiesDir, platform.String()+".txt")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	if cfg.CookiesFile != "" {
		if info, err := os.Stat(cfg.CookiesFile); err == nil && !info.IsDir() {
			return cfg.CookiesFile
		}
	}
	return ""
}

func normalizeAudioFormat(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "m4a", "opus", "wav", "mp3":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "mp3"
	}
}

func videoFormatSelector(quality string, h264Compatible bool) string {
	maxHeight := 0
	switch strings.ToLower(strings.TrimSpace(quality)) {
	case "1080p", "1080":
		maxHeight = 1080
	case "720p", "720":
		maxHeight = 720
	case "480p", "480":
		maxHeight = 480
	case "360p", "360":
		maxHeight = 360
	case "worst":
		return "worstvideo+worstaudio/worst"
	case "best":
		maxHeight = 0
	default:
		// Default to 720p for fast delivery when quality is unspecified.
		maxHeight = 720
	}

	if maxHeight > 0 {
		if h264Compatible {
			return fmt.Sprintf(
				"bestvideo[height<=%d][vcodec^=avc1]+bestaudio[acodec^=mp4a]/best[height<=%d][vcodec^=avc1]/bestvideo[height<=%d]+bestaudio/best[height<=%d]/best",
				maxHeight,
				maxHeight,
				maxHeight,
				maxHeight,
			)
		}
		return fmt.Sprintf(
			"bestvideo[height<=%d]+bestaudio/best[height<=%d]/best",
			maxHeight,
			maxHeight,
		)
	}

	if h264Compatible {
		return "bestvideo[vcodec^=avc1]+bestaudio[acodec^=mp4a]/best[vcodec^=avc1]/bestvideo+bestaudio/best"
	}
	return "bestvideo+bestaudio/best"
}

func adaptiveFormatSelector(quality string, h264Compatible bool) string {
	return videoFormatSelector(quality, h264Compatible) + "/bestaudio/best"
}
