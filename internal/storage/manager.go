package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrFileNotFound is returned when a temporary file ID does not exist or has expired.
var ErrFileNotFound = errors.New("storage: file not found")

// StoredFile tracks metadata for a temporary file hosted by the API.
type StoredFile struct {
	ID        string
	Path      string
	Filename  string
	Extension string
	MimeType  string
	SizeBytes int64
	ExpiresAt time.Time
}

// Manager manages temporary download directories, file registration, and TTL expiration.
type Manager struct {
	baseDir string
	ttl     time.Duration

	mu    sync.RWMutex
	files map[string]StoredFile
}

// NewManager creates a temp storage Manager and ensures baseDir exists.
func NewManager(baseDir string, ttl time.Duration) (*Manager, error) {
	if err := os.MkdirAll(baseDir, 0o750); err != nil {
		return nil, fmt.Errorf("creating temp dir %q: %w", baseDir, err)
	}

	return &Manager{
		baseDir: baseDir,
		ttl:     ttl,
		files:   make(map[string]StoredFile),
	}, nil
}

// CreateJobDir allocates an isolated temporary directory for a single download execution.
func (m *Manager) CreateJobDir(jobID string) (string, error) {
	if jobID == "" {
		jobID = NewID()
	}
	dir := filepath.Join(m.baseDir, jobID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("creating job dir %q: %w", dir, err)
	}
	return dir, nil
}

// RemoveDir deletes a temporary job directory and all its contents.
func (m *Manager) RemoveDir(dir string) {
	if dir == "" {
		return
	}
	_ = os.RemoveAll(dir)
}

// RegisterFile registers a local file for HTTP retrieval until TTL expires.
func (m *Manager) RegisterFile(path string) (StoredFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return StoredFile{}, fmt.Errorf("stat file %q: %w", path, err)
	}
	if info.IsDir() {
		return StoredFile{}, fmt.Errorf("path %q is a directory", path)
	}

	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	mimeType := detectMimeType(ext)
	id := NewID()

	record := StoredFile{
		ID:        id,
		Path:      path,
		Filename:  filepath.Base(path),
		Extension: ext,
		MimeType:  mimeType,
		SizeBytes: info.Size(),
		ExpiresAt: time.Now().UTC().Add(m.ttl),
	}

	m.mu.Lock()
	m.files[id] = record
	m.mu.Unlock()

	return record, nil
}

// GetFile returns a registered file if it exists and has not expired.
func (m *Manager) GetFile(fileID string) (StoredFile, error) {
	m.mu.RLock()
	record, ok := m.files[fileID]
	m.mu.RUnlock()

	if !ok {
		return StoredFile{}, ErrFileNotFound
	}
	if time.Now().UTC().After(record.ExpiresAt) {
		_ = m.DeleteFile(fileID)
		return StoredFile{}, ErrFileNotFound
	}
	if _, err := os.Stat(record.Path); err != nil {
		_ = m.DeleteFile(fileID)
		return StoredFile{}, ErrFileNotFound
	}

	return record, nil
}

// DeleteFile unregisters and removes the file (and its empty parent job dir if applicable).
func (m *Manager) DeleteFile(fileID string) error {
	m.mu.Lock()
	record, ok := m.files[fileID]
	if ok {
		delete(m.files, fileID)
	}
	m.mu.Unlock()

	if !ok {
		return ErrFileNotFound
	}

	if err := os.Remove(record.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing file %q: %w", record.Path, err)
	}

	parent := filepath.Clean(filepath.Dir(record.Path))
	baseDir := filepath.Clean(m.baseDir)
	if parent != baseDir && strings.HasPrefix(parent, baseDir+string(filepath.Separator)) {
		entries, err := os.ReadDir(parent)
		if err == nil && len(entries) == 0 {
			_ = os.Remove(parent)
		}
	}

	return nil
}

// Count returns the number of currently registered active files.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.files)
}

// StartCleanupWorker runs periodic TTL expiration until ctx is cancelled.
func (m *Manager) StartCleanupWorker(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.CleanupExpired()
		}
	}
}

// CleanupExpired deletes expired files from the registry and disk.
func (m *Manager) CleanupExpired() int {
	now := time.Now().UTC()

	m.mu.RLock()
	expiredIDs := make([]string, 0)
	for id, record := range m.files {
		if now.After(record.ExpiresAt) {
			expiredIDs = append(expiredIDs, id)
		}
	}
	m.mu.RUnlock()

	removed := 0
	for _, id := range expiredIDs {
		if err := m.DeleteFile(id); err == nil {
			removed++
		}
	}
	return removed
}

// NewID generates a random 16-byte hex identifier safe for URLs and filenames.
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func detectMimeType(ext string) string {
	switch ext {
	case "mp4":
		return "video/mp4"
	case "m4v":
		return "video/x-m4v"
	case "webm":
		return "video/webm"
	case "mkv":
		return "video/x-matroska"
	case "mov":
		return "video/quicktime"
	case "avi":
		return "video/x-msvideo"
	case "flv":
		return "video/x-flv"
	case "ts":
		return "video/mp2t"
	case "3gp":
		return "video/3gpp"
	case "mp3":
		return "audio/mpeg"
	case "m4a":
		return "audio/mp4"
	case "aac":
		return "audio/aac"
	case "flac":
		return "audio/flac"
	case "opus":
		return "audio/opus"
	case "ogg":
		return "audio/ogg"
	case "wav":
		return "audio/wav"
	case "weba":
		return "audio/webm"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	case "heic":
		return "image/heic"
	case "avif":
		return "image/avif"
	default:
		if mt := mime.TypeByExtension("." + ext); mt != "" {
			return mt
		}
		return "application/octet-stream"
	}
}

// MediaKindFromMIME maps a MIME type string to a high-level media kind ("video", "audio", "image", or "other").
func MediaKindFromMIME(mimeType string) string {
	lower := strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.HasPrefix(lower, "video/"):
		return "video"
	case strings.HasPrefix(lower, "audio/"):
		return "audio"
	case strings.HasPrefix(lower, "image/"):
		return "image"
	default:
		return "other"
	}
}
