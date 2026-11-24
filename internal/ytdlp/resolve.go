package ytdlp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	shortLinkTimeout  = 5 * time.Second
	maxShortLinkHops  = 5
	resolverUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

var errInvalidRedirect = errors.New("ytdlp: invalid redirect")

// IsShortLink reports whether rawURL uses a known short or share redirect host/path.
func IsShortLink(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}

	host := strings.ToLower(parsed.Hostname())
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "m.")
	host = strings.TrimPrefix(host, "mobile.")
	path := strings.ToLower(parsed.Path)

	switch {
	case host == "vt.tiktok.com" || host == "vm.tiktok.com":
		return true
	case (host == "tiktok.com" || strings.HasSuffix(host, ".tiktok.com")) && strings.HasPrefix(path, "/t/"):
		return true
	case host == "instagram.com" && strings.HasPrefix(path, "/share/"):
		return true
	case (host == "threads.com" || host == "threads.net" || strings.HasSuffix(host, ".threads.com") || strings.HasSuffix(host, ".threads.net")) &&
		(strings.HasPrefix(path, "/t/") || strings.HasPrefix(path, "/share/")):
		return true
	case host == "fb.watch" || host == "t.co" || host == "pin.it":
		return true
	default:
		return false
	}
}

// IsTikTokPhotoURL reports whether rawURL points to a TikTok photo slideshow page.
func IsTikTokPhotoURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}

	host := strings.ToLower(parsed.Hostname())
	host = strings.TrimPrefix(host, "www.")
	if host != "tiktok.com" && !strings.HasSuffix(host, ".tiktok.com") {
		return false
	}

	return strings.Contains(strings.ToLower(parsed.Path), "/photo/")
}

// ResolveShortURL expands known short/share URLs (such as vt.tiktok.com or t.co) by following
// HTTP redirects up to maxShortLinkHops. On any failure, it returns rawURL unchanged.
func ResolveShortURL(ctx context.Context, rawURL, proxyURL string) string {
	return resolveShortURL(ctx, rawURL, proxyURL, false)
}

func resolveShortURL(ctx context.Context, rawURL, proxyURL string, forceResolve bool) string {
	trimmed := strings.TrimSpace(rawURL)
	if !forceResolve && !IsShortLink(trimmed) {
		return trimmed
	}

	reqCtx, cancel := context.WithTimeout(ctx, shortLinkTimeout)
	defer cancel()

	client, cleanup := buildRedirectClient(proxyURL)
	defer cleanup()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, trimmed, nil)
	if err != nil {
		return trimmed
	}
	req.Header.Set("User-Agent", resolverUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return trimmed
	}
	defer resp.Body.Close()

	if resp.Request == nil || resp.Request.URL == nil {
		return trimmed
	}

	resolved := resp.Request.URL.String()
	scheme := strings.ToLower(resp.Request.URL.Scheme)
	if (scheme != "http" && scheme != "https") || resolved == "" {
		return trimmed
	}
	return resolved
}

func buildRedirectClient(proxyURL string) (*http.Client, func()) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	if trimmedProxy := strings.TrimSpace(proxyURL); trimmedProxy != "" {
		if parsedProxy, err := url.Parse(trimmedProxy); err == nil {
			transport.Proxy = http.ProxyURL(parsedProxy)
		}
	}

	client := &http.Client{
		Timeout:   shortLinkTimeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxShortLinkHops {
				return errInvalidRedirect
			}
			scheme := strings.ToLower(req.URL.Scheme)
			if scheme != "http" && scheme != "https" {
				return errInvalidRedirect
			}
			return nil
		},
	}
	return client, transport.CloseIdleConnections
}
