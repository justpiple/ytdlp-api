package ytdlp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/justpiple/ytdlp-api/internal/config"
	"github.com/justpiple/ytdlp-api/internal/model"
)

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "valid https", input: "https://www.youtube.com/watch?v=dQw4w9WgXcQ", wantErr: false},
		{name: "valid http", input: "http://example.com/video", wantErr: false},
		{name: "empty url", input: "   ", wantErr: true},
		{name: "cli flag injection", input: "--exec=rm", wantErr: true},
		{name: "file scheme rejected", input: "file:///etc/passwd", wantErr: true},
		{name: "missing host", input: "https:///path", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)
			got, err := ValidateURL(tt.input)
			if tt.wantErr {
				is.Error(err)
				is.ErrorIs(err, ErrInvalidURL)
				return
			}
			is.NoError(err)
			is.NotEmpty(got)
		})
	}
}

func TestDetectPlatform(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected model.Platform
	}{
		{name: "youtube watch", url: "https://www.youtube.com/watch?v=abc", expected: model.PlatformYouTube},
		{name: "youtube short", url: "https://youtu.be/abc", expected: model.PlatformYouTube},
		{name: "youtube music", url: "https://music.youtube.com/watch?v=abc", expected: model.PlatformYouTube},
		{name: "tiktok web", url: "https://www.tiktok.com/@user/video/123", expected: model.PlatformTikTok},
		{name: "tiktok short vt", url: "https://vt.tiktok.com/ZSN123/", expected: model.PlatformTikTok},
		{name: "instagram reel", url: "https://www.instagram.com/reel/C123/", expected: model.PlatformInstagram},
		{name: "threads net", url: "https://www.threads.net/@user/post/123", expected: model.PlatformThreads},
		{name: "threads com", url: "https://www.threads.com/@user/post/123", expected: model.PlatformThreads},
		{name: "x com", url: "https://x.com/user/status/123", expected: model.PlatformTwitter},
		{name: "twitter com", url: "https://twitter.com/user/status/123", expected: model.PlatformTwitter},
		{name: "soundcloud", url: "https://soundcloud.com/artist/track", expected: model.PlatformSoundCloud},
		{name: "facebook watch", url: "https://fb.watch/abc123/", expected: model.PlatformFacebook},
		{name: "facebook web", url: "https://www.facebook.com/reel/123", expected: model.PlatformFacebook},
		{name: "generic site", url: "https://vimeo.com/12345", expected: model.PlatformGeneric},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)
			is.Equal(tt.expected, DetectPlatform(tt.url))
		})
	}
}

func TestBuildDownloadArgs(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	igCookie := filepath.Join(tmpDir, "instagram.txt")
	require.NoError(t, os.WriteFile(igCookie, []byte("# Netscape HTTP Cookie File\n"), 0o600))

	cfg := config.Config{
		FFmpegPath:       "ffmpeg",
		ProxyURL:         "http://env-proxy:8080",
		CookiesDir:       tmpDir,
		MaxFilesize:      "100M",
		MaxPlaylistItems: 10,
	}

	t.Run("whatsapp compatible video with env proxy and platform cookies", func(t *testing.T) {
		is := assert.New(t)
		args := BuildDownloadArgs(cfg, DownloadOptions{
			Target:             "https://www.instagram.com/reel/123/",
			Platform:           model.PlatformInstagram,
			MediaType:          "video",
			Quality:            "720p",
			WhatsAppCompatible: true,
			OutputDir:          "/tmp/out",
		})

		is.Contains(args, "--proxy")
		is.Contains(args, "http://env-proxy:8080")
		is.Contains(args, "--cookies")
		is.Contains(args, igCookie)
		is.Contains(args, "vcodec:h264,fps,res,acodec:m4a")
		is.Contains(args, "mp4")
		is.Contains(args, "100M")
	})

	t.Run("audio extraction with proxy override", func(t *testing.T) {
		is := assert.New(t)
		args := BuildDownloadArgs(cfg, DownloadOptions{
			Target:         "https://www.youtube.com/watch?v=123",
			Platform:       model.PlatformYouTube,
			MediaType:      "audio",
			AudioFormat:    "mp3",
			EmbedThumbnail: true,
			Proxy:          "socks5://custom:1080",
			OutputDir:      "/tmp/out",
		})

		is.Contains(args, "socks5://custom:1080")
		is.NotContains(args, "http://env-proxy:8080")
		is.Contains(args, "--audio-format")
		is.Contains(args, "mp3")
		is.Contains(args, "--embed-thumbnail")
	})
}

func TestBuildSearchArgs(t *testing.T) {
	t.Parallel()
	cfg := config.Config{FFmpegPath: "ffmpeg"}

	tests := []struct {
		name     string
		platform model.Platform
		query    string
		limit    int
		wantArg  string
		wantErr  bool
	}{
		{
			name:     "youtube search",
			platform: model.PlatformYouTube,
			query:    "dewa 19 kangen",
			limit:    5,
			wantArg:  "ytsearch5:dewa 19 kangen",
		},
		{
			name:     "soundcloud search",
			platform: model.PlatformSoundCloud,
			query:    "lofi chill",
			limit:    3,
			wantArg:  "scsearch3:lofi chill",
		},
		{
			name:     "unsupported platform search",
			platform: model.PlatformTikTok,
			query:    "dance",
			limit:    5,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)
			req := require.New(t)
			args, err := BuildSearchArgs(cfg, tt.platform, tt.query, tt.limit, "")
			if tt.wantErr {
				req.Error(err)
				is.ErrorIs(err, ErrUnsupportedPlatform)
				return
			}
			req.NoError(err)
			is.Contains(args, tt.wantArg)
		})
	}
}

func TestParseProgressLine(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantOK     bool
		wantStatus string
		wantPct    float64
	}{
		{
			name:       "download progress line",
			line:       "YTDLP_PROGRESS| 45.2%|5242880|11534336| 2.45MiB/s|00:03",
			wantOK:     true,
			wantStatus: "downloading",
			wantPct:    45.2,
		},
		{
			name:       "ffmpeg extract audio line",
			line:       "[ExtractAudio] Destination: /tmp/01_abc.mp3",
			wantOK:     true,
			wantStatus: "postprocessing",
			wantPct:    99,
		},
		{
			name:   "unrelated log line",
			line:   "[youtube] Extracting URL: https://youtube.com/...",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)
			ev, ok := ParseProgressLine(tt.line)
			is.Equal(tt.wantOK, ok)
			if tt.wantOK {
				is.Equal(tt.wantStatus, ev.Status)
				is.InDelta(tt.wantPct, ev.ProgressPercent, 0.01)
			}
		})
	}
}
