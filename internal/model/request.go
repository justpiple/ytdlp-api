package model

// InfoRequest represents a request to extract metadata and direct formats without downloading.
type InfoRequest struct {
	URL            string `json:"url" binding:"required" example:"https://www.youtube.com/watch?v=dQw4w9WgXcQ"`
	IncludeFormats bool   `json:"include_formats" example:"true"`
	Proxy          string `json:"proxy,omitempty" example:""`
}

// DownloadRequest represents a unified or platform-specific media download request.
type DownloadRequest struct {
	URL            string `json:"url" binding:"required" example:"https://www.instagram.com/p/C0000000000/"`
	Type           string `json:"type,omitempty" enums:"auto,video,audio" example:"auto"`
	Quality        string `json:"quality,omitempty" enums:"best,1080p,720p,480p,360p,worst" example:"720p"`
	AudioFormat    string `json:"audio_format,omitempty" enums:"mp3,m4a,opus,wav" example:"mp3"`
	ResponseMode   string `json:"response_mode,omitempty" enums:"url,async" example:"url"`
	H264Compatible *bool  `json:"h264_compatible,omitempty" example:"true"`
	EmbedThumbnail *bool  `json:"embed_thumbnail,omitempty" example:"true"`
	IsSearch       bool   `json:"is_search,omitempty" example:"false"`
	MaxFilesize    string `json:"max_filesize,omitempty" example:"100M"`
	Proxy          string `json:"proxy,omitempty" example:""`
}

// YouTubeVideoRequest represents a YouTube video download request (!ytmp4).
type YouTubeVideoRequest struct {
	URL            string `json:"url" binding:"required" example:"https://www.youtube.com/watch?v=dQw4w9WgXcQ"`
	Quality        string `json:"quality,omitempty" enums:"best,1080p,720p,480p,360p,worst" example:"720p"`
	ResponseMode   string `json:"response_mode,omitempty" enums:"url,async" example:"url"`
	H264Compatible *bool  `json:"h264_compatible,omitempty" example:"true"`
	MaxFilesize    string `json:"max_filesize,omitempty" example:"100M"`
	Proxy          string `json:"proxy,omitempty"`
}

// YouTubeAudioRequest represents a YouTube audio download or !play request (!ytmp3 / !play).
// Query accepts either a YouTube URL or a search phrase when IsSearch is true (or auto-detected).
type YouTubeAudioRequest struct {
	URL            string `json:"url" binding:"required" example:"https://www.youtube.com/watch?v=dQw4w9WgXcQ"`
	AudioFormat    string `json:"audio_format,omitempty" enums:"mp3,m4a,opus,wav" example:"mp3"`
	ResponseMode   string `json:"response_mode,omitempty" enums:"url,async" example:"url"`
	EmbedThumbnail *bool  `json:"embed_thumbnail,omitempty" example:"true"`
	IsSearch       bool   `json:"is_search,omitempty" example:"false"`
	MaxFilesize    string `json:"max_filesize,omitempty" example:"100M"`
	Proxy          string `json:"proxy,omitempty"`
}

// TikTokDownloadRequest represents a TikTok video, audio, or photo slideshow download request.
type TikTokDownloadRequest struct {
	URL            string `json:"url" binding:"required" example:"https://www.tiktok.com/@tiktok/video/7106594312292453675"`
	Type           string `json:"type,omitempty" enums:"auto,video,audio" example:"auto"`
	AudioFormat    string `json:"audio_format,omitempty" enums:"mp3,m4a,opus" example:"mp3"`
	ResponseMode   string `json:"response_mode,omitempty" enums:"url,async" example:"url"`
	H264Compatible *bool  `json:"h264_compatible,omitempty" example:"true"`
	MaxFilesize    string `json:"max_filesize,omitempty"`
	Proxy          string `json:"proxy,omitempty"`
}

// SocialMediaRequest represents a download request for Instagram, Threads, X/Twitter, or Facebook.
type SocialMediaRequest struct {
	URL            string `json:"url" binding:"required" example:"https://www.instagram.com/reel/C0000000000/"`
	Type           string `json:"type,omitempty" enums:"auto,video,audio" example:"auto"`
	Quality        string `json:"quality,omitempty" enums:"best,1080p,720p,480p,360p" example:"720p"`
	AudioFormat    string `json:"audio_format,omitempty" enums:"mp3,m4a,opus,wav" example:"mp3"`
	ResponseMode   string `json:"response_mode,omitempty" enums:"url,async" example:"url"`
	H264Compatible *bool  `json:"h264_compatible,omitempty" example:"true"`
	MaxFilesize    string `json:"max_filesize,omitempty"`
	Proxy          string `json:"proxy,omitempty"`
}

// SoundCloudDownloadRequest represents a SoundCloud track download request (!scdl).
// URL accepts a SoundCloud link or a search query when IsSearch is true (or auto-detected).
type SoundCloudDownloadRequest struct {
	URL            string `json:"url" binding:"required" example:"https://soundcloud.com/artist/track"`
	AudioFormat    string `json:"audio_format,omitempty" enums:"mp3,m4a,opus,wav" example:"mp3"`
	ResponseMode   string `json:"response_mode,omitempty" enums:"url,async" example:"url"`
	EmbedThumbnail *bool  `json:"embed_thumbnail,omitempty" example:"true"`
	IsSearch       bool   `json:"is_search,omitempty" example:"false"`
	MaxFilesize    string `json:"max_filesize,omitempty"`
	Proxy          string `json:"proxy,omitempty"`
}
