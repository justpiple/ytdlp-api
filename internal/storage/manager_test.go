package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestManagerLifecycle(t *testing.T) {
	defer goleak.VerifyNone(t)

	baseDir := t.TempDir()
	mgr, err := NewManager(baseDir, 50*time.Millisecond)
	require.NoError(t, err)

	jobDir, err := mgr.CreateJobDir("job1")
	require.NoError(t, err)

	samplePath := filepath.Join(jobDir, "video.mp4")
	require.NoError(t, os.WriteFile(samplePath, []byte("hello-video"), 0o640))

	stored, err := mgr.RegisterFile(samplePath)
	require.NoError(t, err)
	assert.Equal(t, "video.mp4", stored.Filename)
	assert.Equal(t, "video/mp4", stored.MimeType)
	assert.Equal(t, int64(11), stored.SizeBytes)
	assert.Equal(t, 1, mgr.Count())

	got, err := mgr.GetFile(stored.ID)
	require.NoError(t, err)
	assert.Equal(t, stored.Path, got.Path)

	require.NoError(t, mgr.DeleteFile(stored.ID))
	assert.Equal(t, 0, mgr.Count())

	_, err = mgr.GetFile(stored.ID)
	assert.ErrorIs(t, err, ErrFileNotFound)
}

func TestManagerCleanupWorker(t *testing.T) {
	defer goleak.VerifyNone(t)

	baseDir := t.TempDir()
	mgr, err := NewManager(baseDir, 20*time.Millisecond)
	require.NoError(t, err)

	jobDir, err := mgr.CreateJobDir("job-expire")
	require.NoError(t, err)

	samplePath := filepath.Join(jobDir, "audio.mp3")
	require.NoError(t, os.WriteFile(samplePath, []byte("mp3-data"), 0o640))

	stored, err := mgr.RegisterFile(samplePath)
	require.NoError(t, err)
	assert.Equal(t, 1, mgr.Count())

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		mgr.StartCleanupWorker(ctx, 15*time.Millisecond)
	}()

	require.Eventually(t, func() bool {
		return mgr.Count() == 0
	}, time.Second, 10*time.Millisecond)

	cancel()
	<-done

	_, err = os.Stat(stored.Path)
	assert.True(t, os.IsNotExist(err))
}
