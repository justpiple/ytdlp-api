package gallerydl

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/justpiple/ytdlp-api/internal/config"
	"github.com/justpiple/ytdlp-api/internal/model"
	"github.com/justpiple/ytdlp-api/internal/ytdlp"
)

// Options configures a gallery-dl download invocation.
type Options struct {
	Target      string
	Platform    model.Platform
	MaxFilesize string
	MaxItems    int
	OutputDir   string
}

// Runner executes gallery-dl downloads and version checks.
type Runner interface {
	Download(ctx context.Context, opts Options, onProgress ytdlp.ProgressCallback) (model.MediaMetadata, []string, error)
	Version(ctx context.Context) (string, error)
}

// ExecRunner implements Runner by invoking the local gallery-dl binary.
type ExecRunner struct {
	cfg config.Config
}

var _ Runner = (*ExecRunner)(nil)

// NewExecRunner creates a gallery-dl ExecRunner from configuration.
func NewExecRunner(cfg config.Config) *ExecRunner {
	return &ExecRunner{cfg: cfg}
}

// BuildArgs constructs CLI arguments for gallery-dl.
func BuildArgs(cfg config.Config, opts Options) []string {
	args := []string{
		"-D", opts.OutputDir,
		"--write-metadata",
		"--no-part",
		"--no-mtime",
	}

	maxItems := opts.MaxItems
	if maxItems <= 0 {
		maxItems = cfg.MaxPlaylistItems
	}
	if maxItems > 0 {
		args = append(args, "--range", fmt.Sprintf("1-%d", maxItems))
	}

	maxSize := strings.TrimSpace(opts.MaxFilesize)
	if maxSize == "" {
		maxSize = cfg.MaxFilesize
	}
	if maxSize != "" {
		args = append(args, "--filesize-max", maxSize)
	}

	if cfg.ProxyURL != "" {
		args = append(args, "--proxy", cfg.ProxyURL)
	}

	if cookiePath := ytdlp.ResolveCookiesFile(cfg, opts.Platform); cookiePath != "" {
		args = append(args, "-C", cookiePath)
	}

	args = append(args, opts.Target)
	return args
}

// Download runs gallery-dl into opts.OutputDir and returns normalized metadata and ordered file paths.
func (r *ExecRunner) Download(
	ctx context.Context,
	opts Options,
	onProgress ytdlp.ProgressCallback,
) (model.MediaMetadata, []string, error) {
	args := BuildArgs(r.cfg, opts)
	cmd := exec.CommandContext(ctx, r.cfg.GalleryDLPath, args...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return model.MediaMetadata{}, nil, fmt.Errorf("creating stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return model.MediaMetadata{}, nil, fmt.Errorf("creating stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return model.MediaMetadata{}, nil, fmt.Errorf("starting gallery-dl: %w", err)
	}

	var stderrBuf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&stderrBuf, stderrPipe)
	}()

	var downloadedOrder []string
	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 32*1024), 4*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Skip skipped-file prefix "# " if present, keeping path order if local.
		cleanLine := strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if strings.HasSuffix(strings.ToLower(cleanLine), ".json") {
			continue
		}
		if strings.Contains(cleanLine, opts.OutputDir) || !strings.Contains(cleanLine, " ") {
			downloadedOrder = append(downloadedOrder, cleanLine)
			if onProgress != nil {
				onProgress(model.ProgressEvent{
					Status:  model.JobStatusDownloading.String(),
					Message: fmt.Sprintf("downloaded item %d", len(downloadedOrder)),
				})
			}
		}
	}

	scanErr := scanner.Err()
	wg.Wait()
	waitErr := cmd.Wait()

	meta := extractAndCleanupMetadata(opts.OutputDir, opts.Platform, opts.Target)

	if scanErr != nil && waitErr == nil {
		return model.MediaMetadata{}, nil, fmt.Errorf("reading gallery-dl stdout: %w", scanErr)
	}
	if waitErr != nil {
		return model.MediaMetadata{}, nil, classifyError(stderrBuf.String(), waitErr)
	}

	files, err := collectOrderedMediaFiles(opts.OutputDir, downloadedOrder)
	if err != nil {
		return model.MediaMetadata{}, nil, fmt.Errorf("collecting output files: %w", err)
	}
	if len(files) == 0 {
		return model.MediaMetadata{}, nil, fmt.Errorf("%w: no output file produced", ytdlp.ErrMediaNotFound)
	}

	return meta, files, nil
}

// Version returns the installed gallery-dl version string.
func (r *ExecRunner) Version(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, r.cfg.GalleryDLPath, "--version")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("checking gallery-dl version: %w", err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func extractAndCleanupMetadata(dir string, platform model.Platform, targetURL string) model.MediaMetadata {
	meta := model.MediaMetadata{
		WebpageURL: targetURL,
		Platform:   platform.String(),
		Formats:    []model.FormatOption{},
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return meta
	}

	var parsedFirst bool
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		fullPath := filepath.Join(dir, name)
		if !parsedFirst {
			if data, readErr := os.ReadFile(fullPath); readErr == nil {
				if m, ok := parseMetadataJSON(data, platform, targetURL); ok {
					meta = m
					parsedFirst = true
				}
			}
		}
		_ = os.Remove(fullPath)
	}

	return meta
}

func parseMetadataJSON(data []byte, platform model.Platform, targetURL string) (model.MediaMetadata, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return model.MediaMetadata{}, false
	}

	id := firstStringField(raw, "post_shortcode", "shortcode", "tweet_id", "post_id", "media_id", "pin_id", "id")
	desc := firstStringField(raw, "description", "content", "caption", "title")
	title := firstStringField(raw, "title", "description", "content", "caption")
	uploader := firstStringField(raw, "username", "owner_username", "uploader", "nickname")
	if uploader == "" {
		uploader = firstNestedString(
			raw,
			[]string{"author", "name"},
			[]string{"author", "nick"},
			[]string{"user", "name"},
			[]string{"pinner", "username"},
			[]string{"pinner", "full_name"},
		)
	}
	webpage := firstStringField(raw, "post_url", "webpage_url", "url")
	if !ytdlp.IsHTTPURL(webpage) {
		webpage = targetURL
	}
	extractor := strings.ToLower(firstStringField(raw, "category"))

	resolvedPlatform := platform
	if resolvedPlatform == model.PlatformUnknown {
		resolvedPlatform = ytdlp.DetectPlatform(webpage)
	}

	return model.MediaMetadata{
		ID:          id,
		Title:       title,
		Description: desc,
		Uploader:    uploader,
		WebpageURL:  webpage,
		Extractor:   extractor,
		Platform:    resolvedPlatform.String(),
		Formats:     []model.FormatOption{},
	}, true
}

func firstStringField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch typed := v.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return strings.TrimSpace(typed)
			}
		case json.Number:
			if typed.String() != "" {
				return typed.String()
			}
		case float64:
			return strconv.FormatInt(int64(typed), 10)
		case int64:
			return strconv.FormatInt(typed, 10)
		}
	}
	return ""
}

func firstNestedString(m map[string]any, paths ...[]string) string {
	for _, p := range paths {
		curr := m
		for i, key := range p {
			val, ok := curr[key]
			if !ok || val == nil {
				break
			}
			if i == len(p)-1 {
				if s, ok := val.(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
				break
			}
			next, ok := val.(map[string]any)
			if !ok {
				break
			}
			curr = next
		}
	}
	return ""
}

func collectOrderedMediaFiles(dir string, stdoutOrder []string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading output dir: %w", err)
	}

	orderIndex := make(map[string]int, len(stdoutOrder))
	for i, p := range stdoutOrder {
		abs := filepath.Clean(p)
		if _, exists := orderIndex[abs]; !exists {
			orderIndex[abs] = i
		}
		base := filepath.Base(abs)
		if _, exists := orderIndex[base]; !exists {
			orderIndex[base] = i
		}
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".json" || ext == ".part" || ext == ".ytdl" || ext == ".temp" {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}

	slices.SortStableFunc(files, func(a, b string) int {
		aAudio := isAudioFile(a)
		bAudio := isAudioFile(b)
		if aAudio != bAudio {
			if aAudio {
				return 1
			}
			return -1
		}

		idxA, okA := lookupOrderIndex(orderIndex, a)
		idxB, okB := lookupOrderIndex(orderIndex, b)
		if okA && okB && idxA != idxB {
			return cmp.Compare(idxA, idxB)
		}
		if okA != okB {
			if okA {
				return -1
			}
			return 1
		}
		return compareNatural(filepath.Base(a), filepath.Base(b))
	})

	return files, nil
}

func lookupOrderIndex(index map[string]int, path string) (int, bool) {
	if idx, ok := index[filepath.Clean(path)]; ok {
		return idx, true
	}
	if idx, ok := index[filepath.Base(path)]; ok {
		return idx, true
	}
	return 0, false
}

func isAudioFile(path string) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	switch ext {
	case "mp3", "m4a", "opus", "ogg", "wav", "aac", "flac", "weba":
		return true
	default:
		return false
	}
}

func compareNatural(a, b string) int {
	for len(a) > 0 && len(b) > 0 {
		aDigit := isASCIIDigit(a[0])
		bDigit := isASCIIDigit(b[0])
		if aDigit && bDigit {
			aNum, aRest := takeDigits(a)
			bNum, bRest := takeDigits(b)
			if len(aNum) != len(bNum) {
				return cmp.Compare(len(aNum), len(bNum))
			}
			if aNum != bNum {
				return cmp.Compare(aNum, bNum)
			}
			a, b = aRest, bRest
			continue
		}
		if a[0] != b[0] {
			return cmp.Compare(a[0], b[0])
		}
		a, b = a[1:], b[1:]
	}
	return cmp.Compare(len(a), len(b))
}

func isASCIIDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func takeDigits(s string) (string, string) {
	i := 0
	for i < len(s) && isASCIIDigit(s[i]) {
		i++
	}
	trimmed := strings.TrimLeft(s[:i], "0")
	return trimmed, s[i:]
}

func classifyError(stderr string, err error) error {
	lower := strings.ToLower(stderr)
	switch {
	case strings.Contains(lower, "filesize") && strings.Contains(lower, "exceed"),
		strings.Contains(lower, "too large"):
		return fmt.Errorf("%w: %s", ytdlp.ErrMediaTooLarge, summarizeStderr(stderr))
	case strings.Contains(lower, "401"),
		strings.Contains(lower, "403"),
		strings.Contains(lower, "429"),
		strings.Contains(lower, "login"),
		strings.Contains(lower, "cookies"),
		strings.Contains(lower, "sign in"),
		strings.Contains(lower, "unauthorized"),
		strings.Contains(lower, "private"),
		strings.Contains(lower, "rate limit"),
		strings.Contains(lower, "challenge"),
		strings.Contains(lower, "checkpoint"):
		return fmt.Errorf("%w: %s", ytdlp.ErrUpstreamBlocked, summarizeStderr(stderr))
	case strings.Contains(lower, "404"),
		strings.Contains(lower, "not found"),
		strings.Contains(lower, "no results"),
		strings.Contains(lower, "unsupported url"),
		strings.Contains(lower, "does not exist"),
		strings.Contains(lower, "unavailable"),
		strings.Contains(lower, "nothing to download"):
		return fmt.Errorf("%w: %s", ytdlp.ErrMediaNotFound, summarizeStderr(stderr))
	default:
		msg := summarizeStderr(stderr)
		if msg == "" {
			return fmt.Errorf("gallery-dl execution failed: %w", err)
		}
		return fmt.Errorf("gallery-dl execution failed (%s): %w", msg, err)
	}
}

func summarizeStderr(stderr string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line
		}
	}
	return ""
}
