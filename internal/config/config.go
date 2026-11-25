package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds application runtime settings loaded from environment variables.
type Config struct {
	Port                   string
	GinMode                string
	BaseURL                string
	LogLevel               string
	YTDLPPath              string
	FFmpegPath             string
	GalleryDLPath          string
	ProxyURL               string
	CookiesFile            string
	CookiesDir             string
	TempDir                string
	FileTTL                time.Duration
	CleanupInterval        time.Duration
	MaxConcurrentDownloads int
	DownloadTimeout        time.Duration
	InfoTimeout            time.Duration
	MaxFilesize            string
	MaxPlaylistItems       int
	ConcurrentFragments    int
	FFmpegPreset           string
}

// Load reads configuration from environment variables with explicit defaults.
func Load() (Config, error) {
	cfg := Config{
		Port:                   envString("PORT", "8080"),
		GinMode:                envString("GIN_MODE", "release"),
		BaseURL:                strings.TrimRight(os.Getenv("BASE_URL"), "/"),
		LogLevel:               strings.ToLower(envString("LOG_LEVEL", "info")),
		YTDLPPath:              envString("YTDLP_PATH", "yt-dlp"),
		FFmpegPath:             envString("FFMPEG_PATH", "ffmpeg"),
		GalleryDLPath:          envString("GALLERYDL_PATH", "gallery-dl"),
		ProxyURL:               strings.TrimSpace(os.Getenv("PROXY_URL")),
		CookiesFile:            strings.TrimSpace(os.Getenv("COOKIES_FILE")),
		CookiesDir:             envString("COOKIES_DIR", "./cookies"),
		TempDir:                envString("TEMP_DIR", "/tmp/ytdlp-api"),
		FileTTL:                15 * time.Minute,
		CleanupInterval:        5 * time.Minute,
		MaxConcurrentDownloads: 4,
		DownloadTimeout:        5 * time.Minute,
		InfoTimeout:            45 * time.Second,
		MaxFilesize:            envString("MAX_FILESIZE", "100M"),
		MaxPlaylistItems:       10,
		ConcurrentFragments:    4,
		FFmpegPreset:           strings.ToLower(envString("FFMPEG_PRESET", "veryfast")),
	}

	var err error
	if cfg.FileTTL, err = envDuration("FILE_TTL", cfg.FileTTL); err != nil {
		return Config{}, fmt.Errorf("parsing FILE_TTL: %w", err)
	}
	if cfg.CleanupInterval, err = envDuration("CLEANUP_INTERVAL", cfg.CleanupInterval); err != nil {
		return Config{}, fmt.Errorf("parsing CLEANUP_INTERVAL: %w", err)
	}
	if cfg.DownloadTimeout, err = envDuration("DOWNLOAD_TIMEOUT", cfg.DownloadTimeout); err != nil {
		return Config{}, fmt.Errorf("parsing DOWNLOAD_TIMEOUT: %w", err)
	}
	if cfg.InfoTimeout, err = envDuration("INFO_TIMEOUT", cfg.InfoTimeout); err != nil {
		return Config{}, fmt.Errorf("parsing INFO_TIMEOUT: %w", err)
	}
	if cfg.MaxConcurrentDownloads, err = envPositiveInt("MAX_CONCURRENT_DOWNLOADS", cfg.MaxConcurrentDownloads); err != nil {
		return Config{}, fmt.Errorf("parsing MAX_CONCURRENT_DOWNLOADS: %w", err)
	}
	if cfg.MaxPlaylistItems, err = envPositiveInt("MAX_PLAYLIST_ITEMS", cfg.MaxPlaylistItems); err != nil {
		return Config{}, fmt.Errorf("parsing MAX_PLAYLIST_ITEMS: %w", err)
	}
	if cfg.ConcurrentFragments, err = envPositiveInt("CONCURRENT_FRAGMENTS", cfg.ConcurrentFragments); err != nil {
		return Config{}, fmt.Errorf("parsing CONCURRENT_FRAGMENTS: %w", err)
	}
	if !isValidFFmpegPreset(cfg.FFmpegPreset) {
		return Config{}, fmt.Errorf("parsing FFMPEG_PRESET: unsupported preset %q", cfg.FFmpegPreset)
	}

	return cfg, nil
}

func isValidFFmpegPreset(preset string) bool {
	switch preset {
	case "ultrafast", "superfast", "veryfast", "faster", "fast", "medium", "slow", "slower", "veryslow":
		return true
	default:
		return false
	}
}

func envString(key, fallback string) string {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return fallback
	}
	return val
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("duration must be positive, got %s", d)
	}
	return d, nil
}

func envPositiveInt(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid integer %q: %w", raw, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("integer must be positive, got %d", n)
	}
	return n, nil
}
