package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	_ "github.com/justpiple/ytdlp-api/docs"
	"github.com/justpiple/ytdlp-api/internal/config"
	"github.com/justpiple/ytdlp-api/internal/gallerydl"
	"github.com/justpiple/ytdlp-api/internal/handler"
	"github.com/justpiple/ytdlp-api/internal/job"
	"github.com/justpiple/ytdlp-api/internal/service"
	"github.com/justpiple/ytdlp-api/internal/storage"
	"github.com/justpiple/ytdlp-api/internal/ytdlp"
)

// @title           yt-dlp Media Downloader & Search API
// @version         1.0
// @description     Internal REST API wrapping yt-dlp and ffmpeg for downloading and searching media from YouTube, TikTok, Instagram, Threads, X (Twitter), SoundCloud, Facebook, and more.
// @BasePath        /api/v1
func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	storageMgr, err := storage.NewManager(cfg.TempDir, cfg.FileTTL)
	if err != nil {
		return fmt.Errorf("initializing storage manager: %w", err)
	}

	jobMgr := job.NewManager(cfg.MaxConcurrentDownloads)
	runner := ytdlp.NewExecRunner(cfg)
	galleryRunner := gallerydl.NewExecRunner(cfg)
	svc := service.NewDownloader(rootCtx, cfg, runner, galleryRunner, storageMgr, jobMgr)

	var workersWG sync.WaitGroup
	workersWG.Add(2)
	go func() {
		defer workersWG.Done()
		storageMgr.StartCleanupWorker(rootCtx, cfg.CleanupInterval)
	}()
	go func() {
		defer workersWG.Done()
		jobMgr.StartCleanupWorker(rootCtx, cfg.CleanupInterval, cfg.FileTTL*2)
	}()

	router := handler.NewRouter(cfg, svc, logger)
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      cfg.DownloadTimeout + 30*time.Second,
		IdleTimeout:       120 * time.Second,
	}

	serverErrCh := make(chan error, 1)
	go func() {
		logger.Info(
			"starting ytdlp-api server",
			"port", cfg.Port,
			"temp_dir", cfg.TempDir,
			"max_concurrent", cfg.MaxConcurrentDownloads,
			"proxy_configured", cfg.ProxyURL != "",
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- err
		}
		close(serverErrCh)
	}()

	select {
	case err := <-serverErrCh:
		stop()
		svc.Wait()
		workersWG.Wait()
		if err != nil {
			return fmt.Errorf("http server failed: %w", err)
		}
		return nil
	case <-rootCtx.Done():
		logger.Info("shutting down gracefully")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown error", "error", err)
	}

	svc.Wait()
	workersWG.Wait()
	logger.Info("shutdown complete")
	return nil
}

func newLogger(levelStr string) *slog.Logger {
	level := slog.LevelInfo
	switch levelStr {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})
	return slog.New(h)
}
