package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/bens-pa/ytdlp-api/internal/model"
	"github.com/bens-pa/ytdlp-api/internal/service"
)

// JobHandler serves async job polling, live SSE progress streams, and temporary file downloads.
type JobHandler struct {
	service *service.Downloader
	logger  *slog.Logger
}

// NewJobHandler creates a JobHandler.
func NewJobHandler(svc *service.Downloader, logger *slog.Logger) *JobHandler {
	return &JobHandler{
		service: svc,
		logger:  logger,
	}
}

// GetJobStatus godoc
// @Summary      Get async job status
// @Description  Returns the current progress snapshot and final DownloadResult (when completed) for an async job.
// @Tags         jobs
// @Produce      json
// @Param        job_id  path      string  true  "Job ID"
// @Success      200     {object}  model.APIResponse{data=model.ProgressEvent}
// @Failure      404     {object}  model.ErrorResponse
// @Router       /jobs/{job_id} [get]
func (h *JobHandler) GetJobStatus(c *gin.Context) {
	jobID := c.Param("job_id")
	ev, err := h.service.GetJob(jobID)
	if err != nil {
		handleServiceError(c, h.logger, err)
		return
	}

	writeSuccess(c, http.StatusOK, ev)
}

// StreamJobEvents godoc
// @Summary      Stream async job progress via SSE
// @Description  Opens a Server-Sent Events (text/event-stream) stream pushing real-time download progress, postprocessing status, and completion payload.
// @Tags         jobs
// @Produce      text/event-stream
// @Param        job_id  path      string  true  "Job ID"
// @Success      200     {string}  string  "SSE progress stream"
// @Failure      404     {object}  model.ErrorResponse
// @Router       /jobs/{job_id}/events [get]
func (h *JobHandler) StreamJobEvents(c *gin.Context) {
	jobID := c.Param("job_id")
	ch, unsubscribe, err := h.service.SubscribeJob(jobID)
	if err != nil {
		handleServiceError(c, h.logger, err)
		return
	}
	defer unsubscribe()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			payload, marshalErr := json.Marshal(ev)
			if marshalErr != nil {
				return
			}
			c.SSEvent(ev.Status, string(payload))
			c.Writer.Flush()
		}
	}
}

// DownloadFile godoc
// @Summary      Download hosted temporary file
// @Description  Streams a previously downloaded file. Pass ?delete_after=true to automatically remove the file after transfer completes (ideal for WhatsApp bots).
// @Tags         files
// @Produce      octet-stream
// @Param        file_id       path      string  true   "File ID"
// @Param        delete_after  query     bool    false  "Delete file immediately after download finishes" default(false)
// @Success      200           {file}    binary
// @Failure      404           {object}  model.ErrorResponse
// @Router       /files/{file_id} [get]
func (h *JobHandler) DownloadFile(c *gin.Context) {
	fileID := c.Param("file_id")
	record, err := h.service.GetFile(fileID)
	if err != nil {
		handleServiceError(c, h.logger, err)
		return
	}

	deleteAfter := false
	if parsed, parseErr := strconv.ParseBool(c.DefaultQuery("delete_after", "false")); parseErr == nil {
		deleteAfter = parsed
	}
	if deleteAfter {
		defer func() {
			if delErr := h.service.DeleteFile(fileID); delErr != nil {
				h.logger.WarnContext(c.Request.Context(), "failed to delete file after download", "file_id", fileID, "error", delErr)
			}
		}()
	}

	c.Header("Content-Type", record.MimeType)
	c.FileAttachment(record.Path, record.Filename)
}

// DeleteFile godoc
// @Summary      Delete hosted temporary file
// @Description  Manually deletes a temporary file from disk before its TTL expires.
// @Tags         files
// @Produce      json
// @Param        file_id  path      string  true  "File ID"
// @Success      200      {object}  model.APIResponse{data=model.DeleteFileResponse}
// @Failure      404      {object}  model.ErrorResponse
// @Router       /files/{file_id} [delete]
func (h *JobHandler) DeleteFile(c *gin.Context) {
	fileID := c.Param("file_id")
	if err := h.service.DeleteFile(fileID); err != nil {
		handleServiceError(c, h.logger, err)
		return
	}

	writeSuccess(c, http.StatusOK, model.DeleteFileResponse{
		Deleted: fileID,
	})
}
