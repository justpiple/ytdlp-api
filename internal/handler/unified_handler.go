package handler

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/justpiple/ytdlp-api/internal/config"
	"github.com/justpiple/ytdlp-api/internal/model"
	"github.com/justpiple/ytdlp-api/internal/service"
)

// UnifiedHandler exposes platform-agnostic endpoints (/info, /download, /search).
type UnifiedHandler struct {
	cfg     config.Config
	service *service.Downloader
	logger  *slog.Logger
}

// NewUnifiedHandler creates a UnifiedHandler.
func NewUnifiedHandler(cfg config.Config, svc *service.Downloader, logger *slog.Logger) *UnifiedHandler {
	return &UnifiedHandler{
		cfg:     cfg,
		service: svc,
		logger:  logger,
	}
}

// Info godoc
// @Summary      Extract media metadata
// @Description  Extracts metadata, thumbnail, and available formats/direct URLs without downloading media files.
// @Tags         unified
// @Accept       json
// @Produce      json
// @Param        request  body      model.InfoRequest  true  "Media URL and options"
// @Success      200      {object}  model.APIResponse{data=model.MediaMetadata}
// @Failure      400      {object}  model.ErrorResponse
// @Failure      404      {object}  model.ErrorResponse
// @Failure      502      {object}  model.ErrorResponse
// @Router       /info [post]
func (h *UnifiedHandler) Info(c *gin.Context) {
	var req model.InfoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	meta, err := h.service.ExtractInfo(c.Request.Context(), req)
	if err != nil {
		handleServiceError(c, h.logger, err)
		return
	}

	writeSuccess(c, http.StatusOK, meta)
}

// Download godoc
// @Summary      Universal media download
// @Description  Auto-detects platform and media type (video, audio, image, or carousel) from URL for non-YouTube sources; SoundCloud stays audio-only. Supports response_mode: url (default) or async.
// @Tags         unified
// @Accept       json
// @Produce      json
// @Param        request  body      model.DownloadRequest  true  "Download payload"
// @Success      200      {object}  model.APIResponse{data=model.DownloadResult}
// @Success      202      {object}  model.APIResponse{data=model.JobAcceptedResponse}
// @Failure      400      {object}  model.ErrorResponse
// @Failure      404      {object}  model.ErrorResponse
// @Failure      413      {object}  model.ErrorResponse
// @Failure      502      {object}  model.ErrorResponse
// @Router       /download [post]
func (h *UnifiedHandler) Download(c *gin.Context) {
	var req model.DownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	executeDownload(c, h.cfg, h.service, h.logger, req, model.PlatformUnknown)
}

// Search godoc
// @Summary      Cross-platform media search
// @Description  Searches YouTube or SoundCloud for tracks and videos.
// @Tags         unified
// @Produce      json
// @Param        platform  query     string  false  "Target platform (youtube or soundcloud)" default(youtube) Enums(youtube, soundcloud)
// @Param        q         query     string  true   "Search query"
// @Param        limit     query     int     false  "Maximum results (1-25)" default(5)
// @Param        proxy     query     string  false  "Optional proxy override"
// @Success      200       {object}  model.APIResponse{data=model.SearchResponse}
// @Failure      400       {object}  model.ErrorResponse
// @Failure      502       {object}  model.ErrorResponse
// @Router       /search [get]
func (h *UnifiedHandler) Search(c *gin.Context) {
	platformStr := strings.ToLower(strings.TrimSpace(c.DefaultQuery("platform", "youtube")))
	platform := model.ParsePlatform(platformStr)
	if platform != model.PlatformYouTube && platform != model.PlatformSoundCloud {
		writeError(c, http.StatusBadRequest, "UNSUPPORTED_PLATFORM", "platform must be youtube or soundcloud")
		return
	}

	query := strings.TrimSpace(c.Query("q"))
	if query == "" {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "query parameter q is required")
		return
	}

	limit := parseLimitQuery(c.Query("limit"), 5)
	proxy := strings.TrimSpace(c.Query("proxy"))

	resp, err := h.service.Search(c.Request.Context(), platform, query, limit, proxy)
	if err != nil {
		handleServiceError(c, h.logger, err)
		return
	}

	writeSuccess(c, http.StatusOK, resp)
}

func executeDownload(
	c *gin.Context,
	cfg config.Config,
	svc *service.Downloader,
	logger *slog.Logger,
	req model.DownloadRequest,
	expectedPlatform model.Platform,
) {
	if !isValidDownloadType(req.Type) {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "type must be auto, video, or audio")
		return
	}

	baseURL := resolveBaseURL(c, cfg.BaseURL)
	mode := strings.ToLower(strings.TrimSpace(req.ResponseMode))

	switch mode {
	case "async":
		accepted, err := svc.StartAsyncDownload(c.Request.Context(), baseURL, req, expectedPlatform)
		if err != nil {
			handleServiceError(c, logger, err)
			return
		}
		writeSuccess(c, http.StatusAccepted, accepted)

	case "", "url":
		result, err := svc.DownloadSync(c.Request.Context(), baseURL, req, expectedPlatform)
		if err != nil {
			handleServiceError(c, logger, err)
			return
		}
		writeSuccess(c, http.StatusOK, result)

	default:
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "response_mode must be url or async")
	}
}

func isValidDownloadType(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto", "video", "audio":
		return true
	default:
		return false
	}
}

func parseLimitQuery(raw string, fallback int) int {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return fallback
	}
	if n > 25 {
		return 25
	}
	return n
}
