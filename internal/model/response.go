package model

// APIResponse is the standard JSON envelope for successful API responses.
type APIResponse struct {
	Success bool   `json:"success" example:"true"`
	Message string `json:"message,omitempty" example:"ok"`
	Data    any    `json:"data"`
}

// DeleteFileResponse confirms deletion of a temporary file.
type DeleteFileResponse struct {
	Deleted string `json:"deleted" example:"f8b3a1c27d4e4f1a9b2c123456789abc"`
}

// ErrorDetail describes an error returned by the API.
type ErrorDetail struct {
	Code    string `json:"code" example:"INVALID_REQUEST"`
	Message string `json:"message" example:"url is required"`
}

// ErrorResponse is the standard JSON envelope for error responses.
type ErrorResponse struct {
	Success bool        `json:"success" example:"false"`
	Error   ErrorDetail `json:"error"`
}

// DownloadResult contains extracted metadata and hosted download URLs.
type DownloadResult struct {
	Platform  string           `json:"platform" example:"instagram"`
	MediaType string           `json:"media_type" enums:"video,audio,image,carousel" example:"carousel"`
	Count     int              `json:"count" example:"3"`
	Engine    string           `json:"engine" enums:"yt-dlp,gallery-dl" example:"gallery-dl"`
	Metadata  MediaMetadata    `json:"metadata"`
	Files     []DownloadedFile `json:"files"`
}

// SearchResponse contains search results for a given query and platform.
type SearchResponse struct {
	Platform string         `json:"platform" example:"youtube"`
	Query    string         `json:"query" example:"rick astley"`
	Count    int            `json:"count" example:"5"`
	Results  []SearchResult `json:"results"`
}

// HealthResponse reports the service and dependency readiness status.
type HealthResponse struct {
	Status           string `json:"status" example:"ok"`
	YTDLPVersion     string `json:"ytdlp_version" example:"2025.01.15"`
	GalleryDLVersion string `json:"gallerydl_version" example:"1.32.21"`
	FFmpegVersion    string `json:"ffmpeg_version" example:"ffmpeg version 6.1.1"`
	ActiveJobs       int    `json:"active_jobs" example:"0"`
	MaxConcurrent    int    `json:"max_concurrent" example:"4"`
	StoredFiles      int    `json:"stored_files" example:"2"`
}
