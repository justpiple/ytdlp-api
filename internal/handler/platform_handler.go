package handler

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/justpiple/ytdlp-api/internal/config"
	"github.com/justpiple/ytdlp-api/internal/model"
	"github.com/justpiple/ytdlp-api/internal/service"
)

// PlatformHandler exposes dedicated endpoints per social/media platform.
type PlatformHandler struct {
	cfg     config.Config
	service *service.Downloader
	logger  *slog.Logger
}

// NewPlatformHandler creates a PlatformHandler.
func NewPlatformHandler(cfg config.Config, svc *service.Downloader, logger *slog.Logger) *PlatformHandler {
	return &PlatformHandler{
		cfg:     cfg,
		service: svc,
		logger:  logger,
	}
}

// YouTubeSearch godoc
// @Summary      Search YouTube
// @Description  Searches YouTube videos/tracks by keyword. Useful for !ytsearch and !play bot commands.
// @Tags         youtube
// @Produce      json
// @Param        q      query     string  true   "Search query"
// @Param        limit  query     int     false  "Maximum results (1-25)" default(5)
// @Param        proxy  query     string  false  "Optional proxy override"
// @Success      200    {object}  model.APIResponse{data=model.SearchResponse}
// @Failure      400    {object}  model.ErrorResponse
// @Failure      502    {object}  model.ErrorResponse
// @Router       /youtube/search [get]
func (h *PlatformHandler) YouTubeSearch(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	if query == "" {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "query parameter q is required")
		return
	}

	limit := parseLimitQuery(c.Query("limit"), 5)
	proxy := strings.TrimSpace(c.Query("proxy"))

	resp, err := h.service.Search(c.Request.Context(), model.PlatformYouTube, query, limit, proxy)
	if err != nil {
		handleServiceError(c, h.logger, err)
		return
	}

	writeSuccess(c, http.StatusOK, resp)
}

// YouTubeVideo godoc
// @Summary      Download YouTube video
// @Description  Downloads YouTube video in MP4 (H.264/AAC compatible with WhatsApp by default).
// @Tags         youtube
// @Accept       json
// @Produce      json
// @Param        request  body      model.YouTubeVideoRequest  true  "YouTube video request"
// @Success      200      {object}  model.APIResponse{data=model.DownloadResult}
// @Success      202      {object}  model.APIResponse{data=model.JobAcceptedResponse}
// @Failure      400      {object}  model.ErrorResponse
// @Failure      404      {object}  model.ErrorResponse
// @Failure      413      {object}  model.ErrorResponse
// @Router       /youtube/video [post]
func (h *PlatformHandler) YouTubeVideo(c *gin.Context) {
	var req model.YouTubeVideoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	dlReq := model.DownloadRequest{
		URL:                req.URL,
		Type:               "video",
		Quality:            req.Quality,
		ResponseMode:       req.ResponseMode,
		WhatsAppCompatible: req.WhatsAppCompatible,
		MaxFilesize:        req.MaxFilesize,
		Proxy:              req.Proxy,
	}
	executeDownload(c, h.cfg, h.service, h.logger, dlReq, model.PlatformYouTube)
}

// YouTubeAudio godoc
// @Summary      Download YouTube audio (!ytmp3 / !play)
// @Description  Downloads YouTube audio as MP3/M4A/Opus with optional embedded thumbnail. Accepts a YouTube URL or a search keyword (auto-plays top result).
// @Tags         youtube
// @Accept       json
// @Produce      json
// @Param        request  body      model.YouTubeAudioRequest  true  "YouTube audio or !play request"
// @Success      200      {object}  model.APIResponse{data=model.DownloadResult}
// @Success      202      {object}  model.APIResponse{data=model.JobAcceptedResponse}
// @Failure      400      {object}  model.ErrorResponse
// @Failure      404      {object}  model.ErrorResponse
// @Failure      413      {object}  model.ErrorResponse
// @Router       /youtube/audio [post]
func (h *PlatformHandler) YouTubeAudio(c *gin.Context) {
	var req model.YouTubeAudioRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	dlReq := model.DownloadRequest{
		URL:            req.URL,
		Type:           "audio",
		AudioFormat:    req.AudioFormat,
		ResponseMode:   req.ResponseMode,
		EmbedThumbnail: req.EmbedThumbnail,
		IsSearch:       req.IsSearch,
		MaxFilesize:    req.MaxFilesize,
		Proxy:          req.Proxy,
	}
	executeDownload(c, h.cfg, h.service, h.logger, dlReq, model.PlatformYouTube)
}

// TikTokDownload godoc
// @Summary      Download TikTok media
// @Description  Downloads TikTok video (watermark-free when available), audio, or multi-slide items.
// @Tags         tiktok
// @Accept       json
// @Produce      json
// @Param        request  body      model.TikTokDownloadRequest  true  "TikTok request"
// @Success      200      {object}  model.APIResponse{data=model.DownloadResult}
// @Success      202      {object}  model.APIResponse{data=model.JobAcceptedResponse}
// @Failure      400      {object}  model.ErrorResponse
// @Failure      404      {object}  model.ErrorResponse
// @Router       /tiktok/download [post]
func (h *PlatformHandler) TikTokDownload(c *gin.Context) {
	var req model.TikTokDownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	dlReq := model.DownloadRequest{
		URL:                req.URL,
		Type:               req.Type,
		AudioFormat:        req.AudioFormat,
		ResponseMode:       req.ResponseMode,
		WhatsAppCompatible: req.WhatsAppCompatible,
		MaxFilesize:        req.MaxFilesize,
		Proxy:              req.Proxy,
	}
	executeDownload(c, h.cfg, h.service, h.logger, dlReq, model.PlatformTikTok)
}

// InstagramDownload godoc
// @Summary      Download Instagram media
// @Description  Downloads Instagram Reels, Posts, Carousels, or Stories (supports cookies/instagram.txt).
// @Tags         instagram
// @Accept       json
// @Produce      json
// @Param        request  body      model.SocialMediaRequest  true  "Instagram request"
// @Success      200      {object}  model.APIResponse{data=model.DownloadResult}
// @Success      202      {object}  model.APIResponse{data=model.JobAcceptedResponse}
// @Failure      400      {object}  model.ErrorResponse
// @Failure      404      {object}  model.ErrorResponse
// @Router       /instagram/download [post]
func (h *PlatformHandler) InstagramDownload(c *gin.Context) {
	h.handleSocialMedia(c, model.PlatformInstagram)
}

// ThreadsDownload godoc
// @Summary      Download Threads media
// @Description  Downloads Threads videos and carousel posts.
// @Tags         threads
// @Accept       json
// @Produce      json
// @Param        request  body      model.SocialMediaRequest  true  "Threads request"
// @Success      200      {object}  model.APIResponse{data=model.DownloadResult}
// @Success      202      {object}  model.APIResponse{data=model.JobAcceptedResponse}
// @Failure      400      {object}  model.ErrorResponse
// @Failure      404      {object}  model.ErrorResponse
// @Router       /threads/download [post]
func (h *PlatformHandler) ThreadsDownload(c *gin.Context) {
	h.handleSocialMedia(c, model.PlatformThreads)
}

// TwitterDownload godoc
// @Summary      Download X (Twitter) media
// @Description  Downloads videos, GIFs, and multi-video posts from X / Twitter.
// @Tags         twitter
// @Accept       json
// @Produce      json
// @Param        request  body      model.SocialMediaRequest  true  "X / Twitter request"
// @Success      200      {object}  model.APIResponse{data=model.DownloadResult}
// @Success      202      {object}  model.APIResponse{data=model.JobAcceptedResponse}
// @Failure      400      {object}  model.ErrorResponse
// @Failure      404      {object}  model.ErrorResponse
// @Router       /twitter/download [post]
func (h *PlatformHandler) TwitterDownload(c *gin.Context) {
	h.handleSocialMedia(c, model.PlatformTwitter)
}

// FacebookDownload godoc
// @Summary      Download Facebook media
// @Description  Downloads Facebook Reels, Watch, and public videos.
// @Tags         facebook
// @Accept       json
// @Produce      json
// @Param        request  body      model.SocialMediaRequest  true  "Facebook request"
// @Success      200      {object}  model.APIResponse{data=model.DownloadResult}
// @Success      202      {object}  model.APIResponse{data=model.JobAcceptedResponse}
// @Failure      400      {object}  model.ErrorResponse
// @Failure      404      {object}  model.ErrorResponse
// @Router       /facebook/download [post]
func (h *PlatformHandler) FacebookDownload(c *gin.Context) {
	h.handleSocialMedia(c, model.PlatformFacebook)
}

// SoundCloudSearch godoc
// @Summary      Search SoundCloud
// @Description  Searches SoundCloud tracks by keyword.
// @Tags         soundcloud
// @Produce      json
// @Param        q      query     string  true   "Search query"
// @Param        limit  query     int     false  "Maximum results (1-25)" default(5)
// @Param        proxy  query     string  false  "Optional proxy override"
// @Success      200    {object}  model.APIResponse{data=model.SearchResponse}
// @Failure      400    {object}  model.ErrorResponse
// @Failure      502    {object}  model.ErrorResponse
// @Router       /soundcloud/search [get]
func (h *PlatformHandler) SoundCloudSearch(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	if query == "" {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "query parameter q is required")
		return
	}

	limit := parseLimitQuery(c.Query("limit"), 5)
	proxy := strings.TrimSpace(c.Query("proxy"))

	resp, err := h.service.Search(c.Request.Context(), model.PlatformSoundCloud, query, limit, proxy)
	if err != nil {
		handleServiceError(c, h.logger, err)
		return
	}

	writeSuccess(c, http.StatusOK, resp)
}

// SoundCloudDownload godoc
// @Summary      Download SoundCloud audio
// @Description  Downloads SoundCloud track as MP3/M4A/Opus with optional embedded cover art. Accepts a SoundCloud URL or search query.
// @Tags         soundcloud
// @Accept       json
// @Produce      json
// @Param        request  body      model.SoundCloudDownloadRequest  true  "SoundCloud request"
// @Success      200      {object}  model.APIResponse{data=model.DownloadResult}
// @Success      202      {object}  model.APIResponse{data=model.JobAcceptedResponse}
// @Failure      400      {object}  model.ErrorResponse
// @Failure      404      {object}  model.ErrorResponse
// @Router       /soundcloud/download [post]
func (h *PlatformHandler) SoundCloudDownload(c *gin.Context) {
	var req model.SoundCloudDownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	dlReq := model.DownloadRequest{
		URL:            req.URL,
		Type:           "audio",
		AudioFormat:    req.AudioFormat,
		ResponseMode:   req.ResponseMode,
		EmbedThumbnail: req.EmbedThumbnail,
		IsSearch:       req.IsSearch,
		MaxFilesize:    req.MaxFilesize,
		Proxy:          req.Proxy,
	}
	executeDownload(c, h.cfg, h.service, h.logger, dlReq, model.PlatformSoundCloud)
}

func (h *PlatformHandler) handleSocialMedia(c *gin.Context, platform model.Platform) {
	var req model.SocialMediaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	dlReq := model.DownloadRequest{
		URL:                req.URL,
		Type:               req.Type,
		Quality:            req.Quality,
		ResponseMode:       req.ResponseMode,
		WhatsAppCompatible: req.WhatsAppCompatible,
		MaxFilesize:        req.MaxFilesize,
		Proxy:              req.Proxy,
	}
	executeDownload(c, h.cfg, h.service, h.logger, dlReq, platform)
}
