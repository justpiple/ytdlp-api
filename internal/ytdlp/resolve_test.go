package ytdlp

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/goleak"
)

func TestIsShortLink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "tiktok vt short", url: "https://vt.tiktok.com/ZSN123/", want: true},
		{name: "tiktok vm short", url: "https://vm.tiktok.com/ZSN123/", want: true},
		{name: "tiktok t path", url: "https://www.tiktok.com/t/ZTN123/", want: true},
		{name: "tiktok regular video", url: "https://www.tiktok.com/@user/video/123", want: false},
		{name: "instagram share link", url: "https://www.instagram.com/share/reel/abc/", want: true},
		{name: "instagram regular post", url: "https://www.instagram.com/p/abc/", want: false},
		{name: "fb watch", url: "https://fb.watch/abc123/", want: true},
		{name: "twitter t.co", url: "https://t.co/xyz123", want: true},
		{name: "youtube url", url: "https://www.youtube.com/watch?v=123", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, IsShortLink(tt.url))
		})
	}
}

func TestIsTikTokPhotoURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "tiktok photo url", url: "https://www.tiktok.com/@user/photo/7345678901234", want: true},
		{name: "tiktok video url", url: "https://www.tiktok.com/@user/video/7345678901234", want: false},
		{name: "non-tiktok photo path", url: "https://example.com/@user/photo/123", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, IsTikTokPhotoURL(tt.url))
		})
	}
}

func TestResolveShortURL(t *testing.T) {
	defer goleak.VerifyNone(t)

	t.Run("follows redirect to final photo url", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/short" {
				http.Redirect(w, r, "/@user/photo/73456789", http.StatusFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		got := resolveShortURL(t.Context(), srv.URL+"/short", "", true)
		assert.Equal(t, srv.URL+"/@user/photo/73456789", got)
	})

	t.Run("stops and returns original on too many redirects", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hop := 0
			if r.URL.Path != "/start" {
				hop, _ = strconv.Atoi(r.URL.Path[1:])
			}
			http.Redirect(w, r, "/"+strconv.Itoa(hop+1), http.StatusFound)
		}))
		defer srv.Close()

		start := srv.URL + "/start"
		got := resolveShortURL(t.Context(), start, "", true)
		assert.Equal(t, start, got)
	})

	t.Run("non-short url returns untouched without network call", func(t *testing.T) {
		raw := "https://www.instagram.com/p/C0abc/"
		assert.Equal(t, raw, ResolveShortURL(t.Context(), raw, ""))
	})
}
