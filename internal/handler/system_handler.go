package handler

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/bens-pa/ytdlp-api/internal/model"
	"github.com/bens-pa/ytdlp-api/internal/service"
)

// SystemHandler provides healthcheck and capability discovery endpoints.
type SystemHandler struct {
	service *service.Downloader
	logger  *slog.Logger
}

// NewSystemHandler creates a SystemHandler.
func NewSystemHandler(svc *service.Downloader, logger *slog.Logger) *SystemHandler {
	return &SystemHandler{
		service: svc,
		logger:  logger,
	}
}

// Health godoc
// @Summary      Healthcheck
// @Description  Checks API readiness, yt-dlp/ffmpeg binary availability, active concurrency, and temp file count.
// @Tags         system
// @Produce      json
// @Success      200  {object}  model.APIResponse{data=model.HealthResponse}
// @Failure      503  {object}  model.APIResponse{data=model.HealthResponse}
// @Router       /healthz [get]
func (h *SystemHandler) Health(c *gin.Context) {
	health, err := h.service.Health(c.Request.Context())
	if err != nil {
		h.logger.WarnContext(c.Request.Context(), "healthcheck degraded", "error", err)
		c.JSON(http.StatusServiceUnavailable, model.APIResponse{
			Success: false,
			Message: "degraded",
			Data:    health,
		})
		return
	}

	writeSuccess(c, http.StatusOK, health)
}

// Platforms godoc
// @Summary      List supported platforms
// @Description  Returns primary supported platforms and their capabilities/cookies hints.
// @Tags         system
// @Produce      json
// @Success      200  {object}  model.APIResponse{data=[]model.PlatformCapability}
// @Router       /platforms [get]
func (h *SystemHandler) Platforms(c *gin.Context) {
	writeSuccess(c, http.StatusOK, h.service.SupportedPlatforms())
}
