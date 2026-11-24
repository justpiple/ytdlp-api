package handler

import (
	"log/slog"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"github.com/justpiple/ytdlp-api/internal/config"
	"github.com/justpiple/ytdlp-api/internal/service"
)

// NewRouter builds and configures the Gin HTTP router with all routes and middlewares.
func NewRouter(cfg config.Config, svc *service.Downloader, logger *slog.Logger) *gin.Engine {
	if cfg.GinMode != "" {
		gin.SetMode(cfg.GinMode)
	}

	router := gin.New()
	router.Use(
		RequestIDMiddleware(),
		SlogMiddleware(logger),
		CORSMiddleware(),
		gin.Recovery(),
	)

	systemH := NewSystemHandler(svc, logger)
	unifiedH := NewUnifiedHandler(cfg, svc, logger)
	platformH := NewPlatformHandler(cfg, svc, logger)
	jobH := NewJobHandler(svc, logger)

	router.GET("/healthz", systemH.Health)
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	v1 := router.Group("/api/v1")
	{
		v1.GET("/healthz", systemH.Health)
		v1.GET("/platforms", systemH.Platforms)

		// Unified auto-detect endpoints
		v1.POST("/info", unifiedH.Info)
		v1.POST("/download", unifiedH.Download)
		v1.GET("/search", unifiedH.Search)

		// Platform-specific endpoints
		yt := v1.Group("/youtube")
		{
			yt.GET("/search", platformH.YouTubeSearch)
			yt.POST("/video", platformH.YouTubeVideo)
			yt.POST("/audio", platformH.YouTubeAudio)
		}

		v1.POST("/tiktok/download", platformH.TikTokDownload)
		v1.POST("/instagram/download", platformH.InstagramDownload)
		v1.POST("/threads/download", platformH.ThreadsDownload)
		v1.POST("/twitter/download", platformH.TwitterDownload)
		v1.POST("/facebook/download", platformH.FacebookDownload)
		v1.POST("/pinterest/download", platformH.PinterestDownload)

		sc := v1.Group("/soundcloud")
		{
			sc.GET("/search", platformH.SoundCloudSearch)
			sc.POST("/download", platformH.SoundCloudDownload)
		}

		// Jobs, SSE & Temporary File Delivery
		v1.GET("/jobs/:job_id", jobH.GetJobStatus)
		v1.GET("/jobs/:job_id/events", jobH.StreamJobEvents)
		v1.GET("/files/:file_id", jobH.DownloadFile)
		v1.DELETE("/files/:file_id", jobH.DeleteFile)
	}

	return router
}
