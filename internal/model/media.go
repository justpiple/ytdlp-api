package model

import "time"

// Platform represents a supported media source platform.
type Platform int

const (
	PlatformUnknown Platform = iota
	PlatformYouTube
	PlatformTikTok
	PlatformInstagram
	PlatformThreads
	PlatformTwitter
	PlatformSoundCloud
	PlatformFacebook
	PlatformGeneric
)

// String returns the lowercase canonical name for a Platform.
func (p Platform) String() string {
	switch p {
	case PlatformYouTube:
		return "youtube"
	case PlatformTikTok:
		return "tiktok"
	case PlatformInstagram:
		return "instagram"
	case PlatformThreads:
		return "threads"
	case PlatformTwitter:
		return "twitter"
	case PlatformSoundCloud:
		return "soundcloud"
	case PlatformFacebook:
		return "facebook"
	case PlatformGeneric:
		return "generic"
	default:
		return "unknown"
	}
}

// ParsePlatform converts a platform string into a Platform enum.
func ParsePlatform(raw string) Platform {
	switch raw {
	case "youtube", "yt":
		return PlatformYouTube
	case "tiktok", "tt":
		return PlatformTikTok
	case "instagram", "ig":
		return PlatformInstagram
	case "threads":
		return PlatformThreads
	case "twitter", "x":
		return PlatformTwitter
	case "soundcloud", "sc":
		return PlatformSoundCloud
	case "facebook", "fb":
		return PlatformFacebook
	case "generic":
		return PlatformGeneric
	default:
		return PlatformUnknown
	}
}

// FormatOption describes an available media stream format returned by yt-dlp.
type FormatOption struct {
	FormatID   string  `json:"format_id" example:"18"`
	Extension  string  `json:"extension" example:"mp4"`
	Resolution string  `json:"resolution,omitempty" example:"1280x720"`
	Width      int     `json:"width,omitempty" example:"1280"`
	Height     int     `json:"height,omitempty" example:"720"`
	FPS        float64 `json:"fps,omitempty" example:"30"`
	VCodec     string  `json:"vcodec,omitempty" example:"avc1.64001F"`
	ACodec     string  `json:"acodec,omitempty" example:"mp4a.40.2"`
	Filesize   int64   `json:"filesize,omitempty" example:"12582912"`
	TBR        float64 `json:"tbr_kbps,omitempty" example:"1200.5"`
	DirectURL  string  `json:"direct_url,omitempty"`
}

// MediaMetadata holds extracted metadata for a media item.
type MediaMetadata struct {
	ID          string         `json:"id" example:"dQw4w9WgXcQ"`
	Title       string         `json:"title" example:"Rick Astley - Never Gonna Give You Up"`
	Description string         `json:"description,omitempty"`
	Uploader    string         `json:"uploader,omitempty" example:"Rick Astley"`
	UploaderURL string         `json:"uploader_url,omitempty" example:"https://www.youtube.com/@RickAstleyYT"`
	Duration    float64        `json:"duration_seconds,omitempty" example:"212"`
	Thumbnail   string         `json:"thumbnail,omitempty" example:"https://i.ytimg.com/vi/dQw4w9WgXcQ/maxresdefault.jpg"`
	WebpageURL  string         `json:"webpage_url" example:"https://www.youtube.com/watch?v=dQw4w9WgXcQ"`
	Extractor   string         `json:"extractor" example:"youtube"`
	Platform    string         `json:"platform" example:"youtube"`
	ViewCount   int64          `json:"view_count,omitempty" example:"1500000000"`
	LikeCount   int64          `json:"like_count,omitempty" example:"18000000"`
	UploadDate  string         `json:"upload_date,omitempty" example:"20091025"`
	IsLive      bool           `json:"is_live" example:"false"`
	Formats     []FormatOption `json:"formats,omitempty"`
}

// SearchResult represents a single item returned from YouTube or SoundCloud search.
type SearchResult struct {
	ID        string  `json:"id" example:"dQw4w9WgXcQ"`
	Title     string  `json:"title" example:"Rick Astley - Never Gonna Give You Up"`
	URL       string  `json:"url" example:"https://www.youtube.com/watch?v=dQw4w9WgXcQ"`
	Uploader  string  `json:"uploader,omitempty" example:"Rick Astley"`
	Duration  float64 `json:"duration_seconds,omitempty" example:"212"`
	Thumbnail string  `json:"thumbnail,omitempty"`
	ViewCount int64   `json:"view_count,omitempty" example:"1500000000"`
	Platform  string  `json:"platform" example:"youtube"`
}

// DownloadedFile represents a processed media file stored temporarily for retrieval.
type DownloadedFile struct {
	FileID      string    `json:"file_id" example:"f8b3a1c2-7d4e-4f1a-9b2c-123456789abc"`
	Filename    string    `json:"filename" example:"Rick_Astley_-_Never_Gonna_Give_You_Up.mp3"`
	Extension   string    `json:"extension" example:"mp3"`
	MimeType    string    `json:"mime_type" example:"audio/mpeg"`
	SizeBytes   int64     `json:"size_bytes" example:"5124300"`
	DownloadURL string    `json:"download_url" example:"http://localhost:8080/api/v1/files/f8b3a1c2-7d4e-4f1a-9b2c-123456789abc"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// PlatformCapability describes a platform's supported operations for discovery.
type PlatformCapability struct {
	Name       string   `json:"name" example:"youtube"`
	Display    string   `json:"display" example:"YouTube"`
	Features   []string `json:"features"`
	CookiesKey string   `json:"cookies_file_hint" example:"youtube.txt"`
}
