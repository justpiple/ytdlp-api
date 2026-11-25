package threads

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/justpiple/ytdlp-api/internal/config"
	"github.com/justpiple/ytdlp-api/internal/model"
	"github.com/justpiple/ytdlp-api/internal/ytdlp"
)

func wrapThreadsRelayHTML(targetQueryDataJSON string) string {
	return fmt.Sprintf(`<!DOCTYPE html><html><head><title>Threads</title></head><body>
<script type="application/json">{"require":[["ScheduledServerJS","handle",null,[{"__bbox":{"require":[["RelayPrefetchedStreamCache@123","next",[],["adp_BarcelonaPostPageTargetQueryRelayPreloader_abc",{"__bbox":{"complete":true,"result":{"data":%s}}}]]]}}]]]}</script>
</body></html>`, targetQueryDataJSON)
}

func TestParsePostFromHTML(t *testing.T) {
	t.Parallel()

	t.Run("extracts highest resolution single image from threads.com SSR JSON", func(t *testing.T) {
		t.Parallel()
		htmlBody := wrapThreadsRelayHTML(`{
			"media": {
				"pk": "4036843056431095863",
				"code": "DgFv18ok-A3",
				"permalink": "https://www.threads.com/@nexorapublic/post/DgFv18ok-A3",
				"like_count": 168,
				"user": {"username": "nexorapublic", "full_name": "Lust"},
				"caption": {"text": "Sample post caption"},
				"image_versions2": {
					"candidates": [
						{"url": "https://cdn.example.com/thumb.jpg", "width": 320, "height": 207},
						{"url": "https://cdn.example.com/full.jpg", "width": 996, "height": 644}
					]
				}
			}
		}`)

		post, err := parsePostFromHTML([]byte(htmlBody), "https://www.threads.com/@nexorapublic/post/DgFv18ok-A3")
		require.NoError(t, err)
		assert.Equal(t, "DgFv18ok-A3", post.Metadata.ID)
		assert.Equal(t, "Sample post caption", post.Metadata.Title)
		assert.Equal(t, "nexorapublic", post.Metadata.Uploader)
		assert.Equal(t, "threads", post.Metadata.Platform)
		assert.Equal(t, int64(168), post.Metadata.LikeCount)
		require.Len(t, post.Items, 1)
		assert.Equal(t, model.MediaKindImage, post.Items[0].Kind)
		assert.Equal(t, "https://cdn.example.com/full.jpg", post.Items[0].URL)
		assert.Equal(t, "jpg", post.Items[0].Extension)
	})

	t.Run("extracts mixed carousel items in original order", func(t *testing.T) {
		t.Parallel()
		htmlBody := wrapThreadsRelayHTML(`{
			"media": {
				"pk": "111",
				"code": "Car01",
				"user": {"username": "creator"},
				"caption": {"text": "Mixed carousel"},
				"carousel_media": [
					{
						"media_type": 1,
						"image_versions2": {
							"candidates": [
								{"url": "https://cdn.example.com/slide1.webp", "width": 1260, "height": 2800}
							]
						}
					},
					{
						"media_type": 2,
						"image_versions2": {
							"candidates": [
								{"url": "https://cdn.example.com/slide2_poster.jpg", "width": 720, "height": 1280}
							]
						},
						"video_versions": [
							{"type": 102, "url": "https://cdn.example.com/slide2_low.mp4", "width": 480, "height": 854},
							{"type": 101, "url": "https://cdn.example.com/slide2_hd.mp4", "width": 720, "height": 1280}
						]
					}
				]
			}
		}`)

		post, err := parsePostFromHTML([]byte(htmlBody), "https://www.threads.net/@creator/post/Car01")
		require.NoError(t, err)
		require.Len(t, post.Items, 2)
		assert.Equal(t, model.MediaKindImage, post.Items[0].Kind)
		assert.Equal(t, "https://cdn.example.com/slide1.webp", post.Items[0].URL)
		assert.Equal(t, model.MediaKindVideo, post.Items[1].Kind)
		assert.Equal(t, "https://cdn.example.com/slide2_hd.mp4", post.Items[1].URL)
	})

	t.Run("ignores reply media in downward preloader when target post is unavailable", func(t *testing.T) {
		t.Parallel()
		htmlBody := `<!DOCTYPE html><html><body>
		<script type="application/json">{"require":[["ScheduledServerJS","handle",null,[{"__bbox":{"require":[["RelayPrefetchedStreamCache@1","next",[],["adp_BarcelonaPostPageTargetQueryRelayPreloader_1",{"__bbox":{"result":{"data":{"media":null}}}}]]]}}]]]}</script>
		<script type="application/json">{"require":[["ScheduledServerJS","handle",null,[{"__bbox":{"require":[["RelayPrefetchedStreamCache@2","next",[],["adp_BarcelonaPostPageDownwardQueryRelayPreloader_2",{"__bbox":{"result":{"data":{"media":{"code":"OTHER","image_versions2":{"candidates":[{"url":"https://cdn.example.com/reply.jpg","width":500,"height":500}]}}}}}}]]]}}]]]}</script>
		</body></html>`

		_, err := parsePostFromHTML([]byte(htmlBody), "https://www.threads.com/@u/post/DeletedPost")
		require.Error(t, err)
		assert.ErrorIs(t, err, ytdlp.ErrMediaNotFound)
	})

	t.Run("returns ErrUpstreamBlocked for private account without media", func(t *testing.T) {
		t.Parallel()
		htmlBody := wrapThreadsRelayHTML(`{
			"media": {
				"code": "Priv123",
				"user": {"username": "locked", "text_post_app_is_private": true}
			}
		}`)

		_, err := parsePostFromHTML([]byte(htmlBody), "https://www.threads.com/@locked/post/Priv123")
		require.Error(t, err)
		assert.ErrorIs(t, err, ytdlp.ErrUpstreamBlocked)
	})
}

func TestHTTPRunner_DownloadAndGetInfo(t *testing.T) {
	defer goleak.VerifyNone(t)

	var sawSecFetchMode atomic.Bool
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/@nexorapublic/post/DgFv18ok-A3":
			if r.Header.Get("Sec-Fetch-Mode") == "navigate" {
				sawSecFetchMode.Store(true)
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, wrapThreadsRelayHTML(fmt.Sprintf(`{
				"media": {
					"pk": "4036843056431095863",
					"code": "DgFv18ok-A3",
					"permalink": "%s/@nexorapublic/post/DgFv18ok-A3",
					"user": {"username": "nexorapublic"},
					"caption": {"text": "Threads photo and video"},
					"carousel_media": [
						{"media_type": 1, "image_versions2": {"candidates": [{"url": "%s/media/1", "width": 996, "height": 644}]}},
						{"media_type": 2, "video_versions": [{"url": "%s/media/2", "width": 720, "height": 1280}]}
					]
				}
			}`, srv.URL, srv.URL, srv.URL)))
		case "/media/1":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("jpeg-payload"))
		case "/media/2":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("mp4-payload"))
		case "/404":
			w.WriteHeader(http.StatusNotFound)
		case "/login":
			w.WriteHeader(http.StatusOK)
		case "/redirect-login":
			http.Redirect(w, r, "/login", http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := config.Config{
		MaxFilesize:      "10M",
		MaxPlaylistItems: 10,
	}
	runner := NewHTTPRunner(cfg)
	runner.httpClient = srv.Client()

	t.Run("GetInfo returns metadata and formats", func(t *testing.T) {
		meta, err := runner.GetInfo(t.Context(), srv.URL+"/@nexorapublic/post/DgFv18ok-A3")
		require.NoError(t, err)
		assert.True(t, sawSecFetchMode.Load())
		assert.Equal(t, "DgFv18ok-A3", meta.ID)
		assert.Equal(t, "threads", meta.Platform)
		require.Len(t, meta.Formats, 2)
		assert.Equal(t, "996x644", meta.Formats[0].Resolution)
		assert.Equal(t, "720x1280", meta.Formats[1].Resolution)
	})

	t.Run("Download saves carousel items in order and reports progress", func(t *testing.T) {
		outDir := t.TempDir()
		var progressCount int
		meta, files, err := runner.Download(t.Context(), Options{
			Target:    srv.URL + "/@nexorapublic/post/DgFv18ok-A3",
			MediaType: "auto",
			OutputDir: outDir,
		}, func(ev model.ProgressEvent) {
			progressCount++
		})
		require.NoError(t, err)
		assert.Equal(t, "DgFv18ok-A3", meta.ID)
		assert.Equal(t, 2, progressCount)
		require.Len(t, files, 2)
		assert.Equal(t, filepath.Join(outDir, "01_DgFv18ok-A3.jpg"), files[0])
		assert.Equal(t, filepath.Join(outDir, "02_DgFv18ok-A3.mp4"), files[1])

		b1, err := os.ReadFile(files[0])
		require.NoError(t, err)
		assert.Equal(t, "jpeg-payload", string(b1))
	})

	t.Run("Download enforces MaxFilesize", func(t *testing.T) {
		outDir := t.TempDir()
		_, _, err := runner.Download(t.Context(), Options{
			Target:      srv.URL + "/@nexorapublic/post/DgFv18ok-A3",
			MediaType:   "auto",
			MaxFilesize: "5B",
			OutputDir:   outDir,
		}, nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ytdlp.ErrMediaTooLarge)
	})

	t.Run("404 maps to ErrMediaNotFound and login redirect maps to ErrUpstreamBlocked", func(t *testing.T) {
		_, _, err := runner.Download(t.Context(), Options{
			Target:    srv.URL + "/404",
			OutputDir: t.TempDir(),
		}, nil)
		assert.ErrorIs(t, err, ytdlp.ErrMediaNotFound)

		_, _, err = runner.Download(t.Context(), Options{
			Target:    srv.URL + "/redirect-login",
			OutputDir: t.TempDir(),
		}, nil)
		assert.ErrorIs(t, err, ytdlp.ErrUpstreamBlocked)
	})
}
