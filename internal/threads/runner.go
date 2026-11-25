package threads

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/justpiple/ytdlp-api/internal/config"
	"github.com/justpiple/ytdlp-api/internal/model"
	"github.com/justpiple/ytdlp-api/internal/ytdlp"
)

const (
	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	maxHTMLBytes     = 16 * 1024 * 1024
	httpTimeout      = 30 * time.Second
)

// Options configures a native Threads metadata or media download invocation.
type Options struct {
	Target      string
	MediaType   string // "auto", "video", or "audio"
	AudioFormat string // "mp3", "m4a", "opus", or "wav"
	MaxFilesize string
	MaxItems    int
	OutputDir   string
}

// Runner extracts metadata and downloads media from Meta Threads posts.
type Runner interface {
	GetInfo(ctx context.Context, target string) (model.MediaMetadata, error)
	Download(ctx context.Context, opts Options, onProgress ytdlp.ProgressCallback) (model.MediaMetadata, []string, error)
}

// HTTPRunner implements Runner using direct SSR HTML/Relay JSON extraction over HTTP.
type HTTPRunner struct {
	cfg        config.Config
	httpClient *http.Client
}

var _ Runner = (*HTTPRunner)(nil)

// NewHTTPRunner creates a Threads HTTPRunner from configuration.
func NewHTTPRunner(cfg config.Config) *HTTPRunner {
	return &HTTPRunner{cfg: cfg}
}

// GetInfo fetches Threads post metadata and available media formats without downloading files.
func (r *HTTPRunner) GetInfo(ctx context.Context, target string) (model.MediaMetadata, error) {
	post, err := r.fetchPost(ctx, target)
	if err != nil {
		return model.MediaMetadata{}, err
	}
	return post.Metadata, nil
}

// Download fetches Threads post media files into opts.OutputDir and returns normalized metadata and file paths.
func (r *HTTPRunner) Download(
	ctx context.Context,
	opts Options,
	onProgress ytdlp.ProgressCallback,
) (model.MediaMetadata, []string, error) {
	post, err := r.fetchPost(ctx, opts.Target)
	if err != nil {
		return model.MediaMetadata{}, nil, err
	}

	maxItems := opts.MaxItems
	if maxItems <= 0 {
		maxItems = r.cfg.MaxPlaylistItems
	}

	items, err := filterMediaItems(post.Items, opts.MediaType, maxItems)
	if err != nil {
		return model.MediaMetadata{}, nil, err
	}

	maxSizeRaw := strings.TrimSpace(opts.MaxFilesize)
	if maxSizeRaw == "" {
		maxSizeRaw = r.cfg.MaxFilesize
	}
	maxBytes, hasMaxBytes := parseFilesizeBytes(maxSizeRaw)

	client, cleanup := r.clientForProxy()
	defer cleanup()

	cookiePath := ytdlp.ResolveCookiesFile(r.cfg, model.PlatformThreads)
	safeID := sanitizeFilenamePart(post.Metadata.ID)
	files := make([]string, 0, len(items))

	for i, item := range items {
		downloadedPath, err := r.downloadMediaItem(
			ctx,
			client,
			cookiePath,
			opts.OutputDir,
			safeID,
			i+1,
			item,
			maxBytes,
			hasMaxBytes,
		)
		if err != nil {
			for _, f := range files {
				_ = os.Remove(f)
			}
			return model.MediaMetadata{}, nil, err
		}

		finalPath := downloadedPath
		if strings.EqualFold(strings.TrimSpace(opts.MediaType), model.MediaKindAudio) && item.Kind == model.MediaKindVideo {
			extracted, audioErr := r.extractAudioFromVideo(ctx, downloadedPath, opts.AudioFormat, onProgress)
			if audioErr != nil {
				_ = os.Remove(downloadedPath)
				for _, f := range files {
					_ = os.Remove(f)
				}
				return model.MediaMetadata{}, nil, audioErr
			}
			finalPath = extracted
		}

		files = append(files, finalPath)
		if onProgress != nil {
			onProgress(model.ProgressEvent{
				Status:          model.JobStatusDownloading.String(),
				ProgressPercent: float64(i+1) * 100 / float64(len(items)),
				Message:         fmt.Sprintf("downloaded item %d of %d", i+1, len(items)),
			})
		}
	}

	return post.Metadata, files, nil
}

func (r *HTTPRunner) fetchPost(ctx context.Context, target string) (extractedPost, error) {
	client, cleanup := r.clientForProxy()
	defer cleanup()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(target), nil)
	if err != nil {
		return extractedPost{}, fmt.Errorf("%w: %v", ytdlp.ErrInvalidURL, err)
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")

	if cookiePath := ytdlp.ResolveCookiesFile(r.cfg, model.PlatformThreads); cookiePath != "" {
		for _, ck := range loadMatchingCookies(cookiePath, req.URL) {
			req.AddCookie(ck)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return extractedPost{}, fmt.Errorf("fetching threads page: %w", err)
	}
	defer resp.Body.Close()

	if resp.Request != nil && resp.Request.URL != nil {
		finalPath := strings.ToLower(resp.Request.URL.Path)
		if strings.Contains(finalPath, "/login") || strings.Contains(finalPath, "/accounts/login") {
			return extractedPost{}, fmt.Errorf("%w: threads redirected to login", ytdlp.ErrUpstreamBlocked)
		}
	}

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return extractedPost{}, fmt.Errorf("%w: threads post returned HTTP 404", ytdlp.ErrMediaNotFound)
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return extractedPost{}, fmt.Errorf("%w: threads returned HTTP %d", ytdlp.ErrUpstreamBlocked, resp.StatusCode)
	default:
		if resp.StatusCode >= 400 {
			return extractedPost{}, fmt.Errorf("threads returned HTTP %d", resp.StatusCode)
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHTMLBytes))
	if err != nil {
		return extractedPost{}, fmt.Errorf("reading threads response: %w", err)
	}

	resolvedURL := target
	if resp.Request != nil && resp.Request.URL != nil {
		resolvedURL = resp.Request.URL.String()
	}
	return parsePostFromHTML(body, resolvedURL)
}

func (r *HTTPRunner) downloadMediaItem(
	ctx context.Context,
	client *http.Client,
	cookiePath string,
	outputDir string,
	safeID string,
	index int,
	item mediaItem,
	maxBytes int64,
	hasMaxBytes bool,
) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, item.URL, nil)
	if err != nil {
		return "", fmt.Errorf("building media request: %w", err)
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Referer", "https://www.threads.com/")
	if cookiePath != "" {
		for _, ck := range loadMatchingCookies(cookiePath, req.URL) {
			req.AddCookie(ck)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading threads media: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", fmt.Errorf("%w: media asset returned HTTP 404", ytdlp.ErrMediaNotFound)
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return "", fmt.Errorf("%w: media asset returned HTTP %d", ytdlp.ErrUpstreamBlocked, resp.StatusCode)
	default:
		if resp.StatusCode >= 400 {
			return "", fmt.Errorf("downloading threads media returned HTTP %d", resp.StatusCode)
		}
	}

	if hasMaxBytes && resp.ContentLength > maxBytes {
		return "", fmt.Errorf("%w: content length %d exceeds max filesize %d", ytdlp.ErrMediaTooLarge, resp.ContentLength, maxBytes)
	}

	ext := extFromContentType(resp.Header.Get("Content-Type"), item.Extension)
	outPath := filepath.Join(outputDir, fmt.Sprintf("%02d_%s.%s", index, safeID, ext))

	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return "", fmt.Errorf("creating output file %q: %w", filepath.Base(outPath), err)
	}

	var reader io.Reader = resp.Body
	if hasMaxBytes {
		reader = io.LimitReader(resp.Body, maxBytes+1)
	}

	written, copyErr := io.Copy(f, reader)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(outPath)
		return "", fmt.Errorf("writing media file: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(outPath)
		return "", fmt.Errorf("closing media file: %w", closeErr)
	}
	if hasMaxBytes && written > maxBytes {
		_ = os.Remove(outPath)
		return "", fmt.Errorf("%w: downloaded size %d exceeds max filesize %d", ytdlp.ErrMediaTooLarge, written, maxBytes)
	}
	if written == 0 {
		_ = os.Remove(outPath)
		return "", fmt.Errorf("%w: downloaded empty media file", ytdlp.ErrMediaNotFound)
	}

	return outPath, nil
}

func (r *HTTPRunner) extractAudioFromVideo(
	ctx context.Context,
	videoPath string,
	requestedFormat string,
	onProgress ytdlp.ProgressCallback,
) (string, error) {
	ffmpegBin := ytdlp.ResolveFFmpegLocation(r.cfg.FFmpegPath)
	if ffmpegBin == "" {
		return "", errors.New("ffmpeg is required to extract audio from threads video")
	}

	if onProgress != nil {
		onProgress(model.ProgressEvent{
			Status:          model.JobStatusPostProcessing.String(),
			ProgressPercent: 95,
			Message:         "extracting audio with ffmpeg",
		})
	}

	ext, codecArgs := audioEncodeSpec(requestedFormat)
	baseNoExt := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
	outPath := baseNoExt + "." + ext

	args := append([]string{"-y", "-v", "error", "-i", videoPath, "-vn"}, codecArgs...)
	args = append(args, outPath)

	cmd := exec.CommandContext(ctx, ffmpegBin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(outPath)
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("extracting audio (%s): %w", msg, err)
		}
		return "", fmt.Errorf("extracting audio: %w", err)
	}

	_ = os.Remove(videoPath)
	return outPath, nil
}

func audioEncodeSpec(format string) (string, []string) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "m4a":
		return "m4a", []string{"-c:a", "aac", "-b:a", "192k"}
	case "opus":
		return "opus", []string{"-c:a", "libopus", "-b:a", "128k"}
	case "wav":
		return "wav", []string{"-c:a", "pcm_s16le"}
	default:
		return "mp3", []string{"-c:a", "libmp3lame", "-q:a", "2"}
	}
}

func (r *HTTPRunner) clientForProxy() (*http.Client, func()) {
	if r.httpClient != nil {
		return r.httpClient, func() {}
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true

	proxyStr := strings.TrimSpace(r.cfg.ProxyURL)
	if proxyStr != "" {
		if parsedProxy, err := url.Parse(proxyStr); err == nil {
			transport.Proxy = http.ProxyURL(parsedProxy)
		}
	}

	client := &http.Client{
		Timeout:   httpTimeout,
		Transport: transport,
	}
	return client, transport.CloseIdleConnections
}

func extFromContentType(contentType, fallback string) string {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil {
		return fallback
	}
	switch strings.ToLower(mediaType) {
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "image/heic":
		return "heic"
	case "image/avif":
		return "avif"
	case "video/mp4":
		return "mp4"
	case "video/webm":
		return "webm"
	case "video/quicktime":
		return "mov"
	case "audio/mp4", "audio/x-m4a":
		return "m4a"
	case "audio/mpeg":
		return "mp3"
	case "audio/ogg", "audio/opus":
		return "ogg"
	case "audio/wav", "audio/x-wav":
		return "wav"
	default:
		return fallback
	}
}

func loadMatchingCookies(cookiePath string, targetURL *url.URL) []*http.Cookie {
	if targetURL == nil {
		return nil
	}
	f, err := os.Open(cookiePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	host := strings.ToLower(targetURL.Hostname())
	reqPath := targetURL.Path
	if reqPath == "" {
		reqPath = "/"
	}
	isHTTPS := strings.EqualFold(targetURL.Scheme, "https")
	nowUnix := time.Now().Unix()

	var cookies []*http.Cookie
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}

		domain := strings.ToLower(strings.TrimSpace(fields[0]))
		cleanDomain := strings.TrimPrefix(domain, ".")
		if cleanDomain == "" {
			continue
		}
		if host != cleanDomain && !strings.HasSuffix(host, "."+cleanDomain) {
			continue
		}

		cookiePathPrefix := strings.TrimSpace(fields[2])
		if cookiePathPrefix != "" && !strings.HasPrefix(reqPath, cookiePathPrefix) {
			continue
		}

		secureOnly := strings.EqualFold(strings.TrimSpace(fields[3]), "TRUE")
		if secureOnly && !isHTTPS {
			continue
		}

		if exp, err := strconv.ParseInt(strings.TrimSpace(fields[4]), 10, 64); err == nil && exp > 0 && exp <= nowUnix {
			continue
		}

		name := strings.TrimSpace(fields[5])
		value := strings.TrimSpace(fields[6])
		if name == "" {
			continue
		}
		cookies = append(cookies, &http.Cookie{
			Name:  name,
			Value: value,
		})
	}

	return cookies
}
