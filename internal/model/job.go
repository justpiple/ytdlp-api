package model

import "time"

// JobStatus represents the lifecycle state of an async download job.
type JobStatus int

const (
	JobStatusUnknown JobStatus = iota
	JobStatusQueued
	JobStatusDownloading
	JobStatusPostProcessing
	JobStatusCompleted
	JobStatusFailed
)

// String returns the lowercase string representation of JobStatus.
func (s JobStatus) String() string {
	switch s {
	case JobStatusQueued:
		return "queued"
	case JobStatusDownloading:
		return "downloading"
	case JobStatusPostProcessing:
		return "postprocessing"
	case JobStatusCompleted:
		return "completed"
	case JobStatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// ProgressEvent represents a real-time progress update broadcast over SSE or polled via GET /jobs/:id.
type ProgressEvent struct {
	JobID           string          `json:"job_id" example:"a1b2c3d4-e5f6-7890-abcd-ef1234567890"`
	Status          string          `json:"status" example:"downloading"`
	ProgressPercent float64         `json:"progress_percent" example:"45.5"`
	DownloadedBytes int64           `json:"downloaded_bytes,omitempty" example:"5242880"`
	TotalBytes      int64           `json:"total_bytes,omitempty" example:"11534336"`
	Speed           string          `json:"speed,omitempty" example:"2.45MiB/s"`
	ETA             string          `json:"eta,omitempty" example:"00:03"`
	Message         string          `json:"message,omitempty" example:"downloading media"`
	Result          *DownloadResult `json:"result,omitempty"`
	Error           string          `json:"error,omitempty"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// JobAcceptedResponse is returned immediately when response_mode is "async".
type JobAcceptedResponse struct {
	JobID     string `json:"job_id" example:"a1b2c3d4-e5f6-7890-abcd-ef1234567890"`
	Status    string `json:"status" example:"queued"`
	StatusURL string `json:"status_url" example:"http://localhost:8080/api/v1/jobs/a1b2c3d4-e5f6-7890-abcd-ef1234567890"`
	SSEURL    string `json:"sse_url" example:"http://localhost:8080/api/v1/jobs/a1b2c3d4-e5f6-7890-abcd-ef1234567890/events"`
}
