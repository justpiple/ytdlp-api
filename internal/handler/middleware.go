package handler

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

const requestIDKey = "request_id"

// RequestIDMiddleware attaches a unique X-Request-ID header to each request.
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		reqID := c.GetHeader("X-Request-ID")
		if reqID == "" {
			var b [8]byte
			_, _ = rand.Read(b[:])
			reqID = hex.EncodeToString(b[:])
		}
		c.Set(requestIDKey, reqID)
		c.Header("X-Request-ID", reqID)
		c.Next()
	}
}

// SlogMiddleware logs each completed HTTP request using structured slog output.
func SlogMiddleware(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}

		reqID, _ := c.Get(requestIDKey)
		status := c.Writer.Status()
		duration := time.Since(start)

		level := slog.LevelInfo
		if status >= http.StatusInternalServerError {
			level = slog.LevelError
		} else if status >= http.StatusBadRequest {
			level = slog.LevelWarn
		}

		logger.Log(
			c.Request.Context(),
			level,
			"http request",
			"request_id", reqID,
			"method", c.Request.Method,
			"route", route,
			"status", status,
			"duration_ms", duration.Milliseconds(),
			"client_ip", c.ClientIP(),
		)
	}
}

// CORSMiddleware allows cross-origin requests for Swagger UI and internal tools.
func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, X-Request-ID")
		c.Header("Access-Control-Expose-Headers", "Content-Disposition, X-Media-Title, X-Media-Platform")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
