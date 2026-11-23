package job

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/justpiple/ytdlp-api/internal/model"
)

func TestManagerConcurrencyLimit(t *testing.T) {
	defer goleak.VerifyNone(t)

	mgr := NewManager(2)
	require.NoError(t, mgr.Acquire(t.Context()))
	require.NoError(t, mgr.Acquire(t.Context()))
	assert.Equal(t, 2, mgr.ActiveCount())

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	err := mgr.Acquire(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	mgr.Release()
	assert.Equal(t, 1, mgr.ActiveCount())
	mgr.Release()
	assert.Equal(t, 0, mgr.ActiveCount())
}

func TestManagerJobAndSSE(t *testing.T) {
	defer goleak.VerifyNone(t)

	mgr := NewManager(2)
	id := mgr.Create()

	ch, unsub, err := mgr.Subscribe(id)
	require.NoError(t, err)
	defer unsub()

	initial := <-ch
	assert.Equal(t, model.JobStatusQueued.String(), initial.Status)

	mgr.Update(id, model.ProgressEvent{
		Status:          model.JobStatusDownloading.String(),
		ProgressPercent: 50,
		Message:         "downloading",
	})
	prog := <-ch
	assert.Equal(t, 50.0, prog.ProgressPercent)

	mgr.Update(id, model.ProgressEvent{
		Status:          model.JobStatusCompleted.String(),
		ProgressPercent: 100,
		Message:         "done",
	})
	doneEv := <-ch
	assert.Equal(t, model.JobStatusCompleted.String(), doneEv.Status)

	_, ok := <-ch
	assert.False(t, ok, "channel should close after terminal event")
}
