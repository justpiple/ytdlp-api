package ytdlp

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/justpiple/ytdlp-api/internal/model"
)

var (
	// ErrInvalidURL indicates the provided URL is malformed or uses an unsupported scheme.
	ErrInvalidURL = errors.New("ytdlp: invalid url")
	// ErrUnsupportedPlatform indicates the requested platform is not supported for the operation.
	ErrUnsupportedPlatform = errors.New("ytdlp: unsupported platform")
	// ErrMediaNotFound indicates no downloadable media or search results were found.
	ErrMediaNotFound = errors.New("ytdlp: media not found")
	// ErrMediaTooLarge indicates the target media exceeds the configured max filesize.
	ErrMediaTooLarge = errors.New("ytdlp: media exceeds max filesize")
	// ErrUpstreamBlocked indicates the upstream platform blocked or requires authentication/cookies.
	ErrUpstreamBlocked = errors.New("ytdlp: upstream requires authentication or blocked request")
)

// ValidateURL ensures rawURL is a safe http/https URL and cannot be interpreted as a CLI flag.
func ValidateURL(rawURL string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", fmt.Errorf("%w: empty url", ErrInvalidURL)
	}
	if strings.HasPrefix(trimmed, "-") {
		return "", fmt.Errorf("%w: url must not start with dash", ErrInvalidURL)
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("%w: scheme must be http or https", ErrInvalidURL)
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("%w: missing hostname", ErrInvalidURL)
	}

	return trimmed, nil
}

// IsHTTPURL reports whether input looks like an http or https URL.
func IsHTTPURL(input string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(input))
	return strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://")
}

// DetectPlatform determines the media platform from a URL hostname.
func DetectPlatform(rawURL string) model.Platform {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return model.PlatformUnknown
	}

	host := strings.ToLower(parsed.Hostname())
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "m.")
	host = strings.TrimPrefix(host, "mobile.")

	switch {
	case host == "youtube.com" || host == "youtu.be" || strings.HasSuffix(host, ".youtube.com"):
		return model.PlatformYouTube
	case host == "tiktok.com" || strings.HasSuffix(host, ".tiktok.com"):
		return model.PlatformTikTok
	case host == "instagram.com" || host == "instagr.am" || strings.HasSuffix(host, ".instagram.com"):
		return model.PlatformInstagram
	case host == "threads.net" || host == "threads.com" || strings.HasSuffix(host, ".threads.net") || strings.HasSuffix(host, ".threads.com"):
		return model.PlatformThreads
	case host == "twitter.com" || host == "x.com" || host == "t.co" || strings.HasSuffix(host, ".twitter.com") || strings.HasSuffix(host, ".x.com"):
		return model.PlatformTwitter
	case host == "soundcloud.com" || host == "on.soundcloud.com" || strings.HasSuffix(host, ".soundcloud.com"):
		return model.PlatformSoundCloud
	case host == "facebook.com" || host == "fb.watch" || host == "fb.com" || strings.HasSuffix(host, ".facebook.com"):
		return model.PlatformFacebook
	case isPinterestHost(host):
		return model.PlatformPinterest
	case host != "":
		return model.PlatformGeneric
	default:
		return model.PlatformUnknown
	}
}

func isPinterestHost(host string) bool {
	if host == "pin.it" || host == "pinterest.com" || strings.HasSuffix(host, ".pinterest.com") {
		return true
	}
	switch host {
	case "pinterest.at", "pinterest.be", "pinterest.ca", "pinterest.ch",
		"pinterest.cl", "pinterest.co.kr", "pinterest.co.uk", "pinterest.com.au",
		"pinterest.com.mx", "pinterest.de", "pinterest.dk", "pinterest.es",
		"pinterest.fr", "pinterest.ie", "pinterest.info", "pinterest.it",
		"pinterest.jp", "pinterest.nl", "pinterest.nz", "pinterest.ph",
		"pinterest.pt", "pinterest.ru", "pinterest.se":
		return true
	default:
		return false
	}
}
