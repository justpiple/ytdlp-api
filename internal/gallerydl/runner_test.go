package gallerydl

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/justpiple/ytdlp-api/internal/config"
	"github.com/justpiple/ytdlp-api/internal/model"
	"github.com/justpiple/ytdlp-api/internal/ytdlp"
)

func TestBuildArgs(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	igCookie := filepath.Join(tmpDir, "instagram.txt")
	require.NoError(t, os.WriteFile(igCookie, []byte("# Netscape HTTP Cookie File\n"), 0o600))

	cfg := config.Config{
		GalleryDLPath:    "gallery-dl",
		ProxyURL:         "http://env-proxy:8080",
		CookiesDir:       tmpDir,
		MaxFilesize:      "100M",
		MaxPlaylistItems: 10,
	}

	args := BuildArgs(cfg, Options{
		Target:    "https://www.instagram.com/p/C0abc/",
		Platform:  model.PlatformInstagram,
		OutputDir: "/tmp/out",
	})

	is := assert.New(t)
	is.Contains(args, "-D")
	is.Contains(args, "/tmp/out")
	is.Contains(args, "--write-metadata")
	is.Contains(args, "--range")
	is.Contains(args, "1-10")
	is.Contains(args, "--filesize-max")
	is.Contains(args, "100M")
	is.Contains(args, "--proxy")
	is.Contains(args, "http://env-proxy:8080")
	is.Contains(args, "-C")
	is.Contains(args, igCookie)
	is.Equal("https://www.instagram.com/p/C0abc/", args[len(args)-1])
}

func TestExtractAndCleanupMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		jsonPayload  string
		platform     model.Platform
		wantID       string
		wantTitle    string
		wantUploader string
	}{
		{
			name:         "instagram post metadata",
			jsonPayload:  `{"post_shortcode":"C0abc123","description":"Sunset carousel","owner_username":"traveler","category":"instagram","post_url":"https://www.instagram.com/p/C0abc123/"}`,
			platform:     model.PlatformInstagram,
			wantID:       "C0abc123",
			wantTitle:    "Sunset carousel",
			wantUploader: "traveler",
		},
		{
			name:         "twitter post metadata with nested author and 64-bit snowflake id",
			jsonPayload:  `{"tweet_id":1899999999999999999,"content":"Photo dump","author":{"name":"alice"},"category":"twitter"}`,
			platform:     model.PlatformTwitter,
			wantID:       "1899999999999999999",
			wantTitle:    "Photo dump",
			wantUploader: "alice",
		},
		{
			name:         "pinterest pin metadata with nested pinner",
			jsonPayload:  `{"id":"9876543210","description":"Minimalist desk setup","pinner":{"username":"designfan","full_name":"Design Fan"},"category":"pinterest"}`,
			platform:     model.PlatformPinterest,
			wantID:       "9876543210",
			wantTitle:    "Minimalist desk setup",
			wantUploader: "designfan",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			jsonPath := filepath.Join(dir, "01_item.jpg.json")
			mediaPath := filepath.Join(dir, "01_item.jpg")
			require.NoError(t, os.WriteFile(jsonPath, []byte(tt.jsonPayload), 0o640))
			require.NoError(t, os.WriteFile(mediaPath, []byte("jpg"), 0o640))

			meta := extractAndCleanupMetadata(dir, tt.platform, "https://example.com/post")
			is := assert.New(t)
			is.Equal(tt.wantID, meta.ID)
			is.Equal(tt.wantTitle, meta.Title)
			is.Equal(tt.wantUploader, meta.Uploader)
			is.Equal(tt.platform.String(), meta.Platform)

			_, statErr := os.Stat(jsonPath)
			is.True(os.IsNotExist(statErr), "sidecar json must be deleted")
			_, mediaErr := os.Stat(mediaPath)
			is.NoError(mediaErr)
		})
	}
}

func TestCollectOrderedMediaFiles(t *testing.T) {
	t.Parallel()

	t.Run("natural sort puts 2 before 10 and places audio last", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		names := []string{"slide_10.jpg", "slide_2.jpg", "slide_1.jpg", "sound.mp3", "slide_1.jpg.json"}
		for _, n := range names {
			require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte("data"), 0o640))
		}

		got, err := collectOrderedMediaFiles(dir, nil)
		require.NoError(t, err)
		require.Len(t, got, 4)

		assert.Equal(t, []string{
			filepath.Join(dir, "slide_1.jpg"),
			filepath.Join(dir, "slide_2.jpg"),
			filepath.Join(dir, "slide_10.jpg"),
			filepath.Join(dir, "sound.mp3"),
		}, got)
	})

	t.Run("preserves stdout download order over alphabetical filename", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		for _, n := range []string{"z_first.jpg", "a_second.mp4"} {
			require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte("data"), 0o640))
		}

		stdoutOrder := []string{
			filepath.Join(dir, "z_first.jpg"),
			filepath.Join(dir, "a_second.mp4"),
		}
		got, err := collectOrderedMediaFiles(dir, stdoutOrder)
		require.NoError(t, err)
		assert.Equal(t, stdoutOrder, got)
	})

	t.Run("handles utf8 superscript byte 0xb2 without hanging", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		names := []string{"item_\xb2_10.jpg", "item_\xb2_2.jpg"}
		for _, n := range names {
			require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte("data"), 0o640))
		}

		got, err := collectOrderedMediaFiles(dir, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{
			filepath.Join(dir, "item_\xb2_2.jpg"),
			filepath.Join(dir, "item_\xb2_10.jpg"),
		}, got)
	})
}

func TestClassifyError(t *testing.T) {
	t.Parallel()

	baseErr := errors.New("exit status 1")
	tests := []struct {
		name   string
		stderr string
		want   error
	}{
		{
			name:   "login required",
			stderr: "[instagram][error] Login required to view this post",
			want:   ytdlp.ErrUpstreamBlocked,
		},
		{
			name:   "404 not found",
			stderr: "[twitter][error] HttpError: 404 Not Found",
			want:   ytdlp.ErrMediaNotFound,
		},
		{
			name:   "filesize exceeded",
			stderr: "File too large",
			want:   ytdlp.ErrMediaTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := classifyError(tt.stderr, baseErr)
			assert.ErrorIs(t, err, tt.want)
		})
	}
}
