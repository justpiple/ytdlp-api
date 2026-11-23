package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/justpiple/ytdlp-api/internal/job"
	"github.com/justpiple/ytdlp-api/internal/model"
	"github.com/justpiple/ytdlp-api/internal/storage"
	"github.com/justpiple/ytdlp-api/internal/ytdlp"
)

func writeSuccess(c *gin.Context, status int, data any) {
	c.JSON(status, model.APIResponse{
		Success: true,
		Message: "ok",
		Data:    data,
	})
}

func writeError(c *gin.Context, status int, code, message string) {
	c.JSON(status, model.ErrorResponse{
		Success: false,
		Error: model.ErrorDetail{
			Code:    code,
			Message: message,
		},
	})
}

// handleServiceError logs the error once at the HTTP boundary and maps it to a clean JSON response.
func handleServiceError(c *gin.Context, logger *slog.Logger, err error) {
	reqID, _ := c.Get(requestIDKey)

	status := http.StatusInternalServerError
	code := "INTERNAL_ERROR"
	msg := "failed to process media request"

	switch {
	case errors.Is(err, ytdlp.ErrInvalidURL):
		status = http.StatusBadRequest
		code = "INVALID_URL"
		msg = err.Error()
	case errors.Is(err, ytdlp.ErrUnsupportedPlatform):
		status = http.StatusBadRequest
		code = "UNSUPPORTED_PLATFORM"
		msg = err.Error()
	case errors.Is(err, ytdlp.ErrMediaNotFound), errors.Is(err, storage.ErrFileNotFound), errors.Is(err, job.ErrJobNotFound):
		status = http.StatusNotFound
		code = "NOT_FOUND"
		msg = err.Error()
	case errors.Is(err, ytdlp.ErrMediaTooLarge):
		status = http.StatusRequestEntityTooLarge
		code = "MEDIA_TOO_LARGE"
		msg = err.Error()
	case errors.Is(err, ytdlp.ErrUpstreamBlocked):
		status = http.StatusBadGateway
		code = "UPSTREAM_BLOCKED"
		msg = err.Error()
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
		code = "TIMEOUT"
		msg = "media processing timed out"
	}

	logger.ErrorContext(
		c.Request.Context(),
		"request failed",
		"request_id", reqID,
		"route", c.FullPath(),
		"error_code", code,
		"error", err.Error(),
	)

	writeError(c, status, code, msg)
}

// resolveBaseURL computes the base URL for download_url and status_url links.
func resolveBaseURL(c *gin.Context, configuredBaseURL string) string {
	if configuredBaseURL != "" {
		return strings.TrimRight(configuredBaseURL, "/")
	}

	scheme := "http"
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		scheme = strings.Split(proto, ",")[0]
	} else if c.Request.TLS != nil {
		scheme = "https"
	}

	host := c.GetHeader("X-Forwarded-Host")
	if host == "" {
		host = c.Request.Host
	}
	if host == "" {
		return ""
	}
	return fmt.Sprintf("%s://%s", strings.TrimSpace(scheme), strings.TrimSpace(host))
}
