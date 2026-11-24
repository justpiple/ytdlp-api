package threads

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"github.com/justpiple/ytdlp-api/internal/model"
	"github.com/justpiple/ytdlp-api/internal/ytdlp"
)

type mediaItem struct {
	Kind      string // "image", "video", "audio"
	URL       string
	Width     int
	Height    int
	Extension string
}

type extractedPost struct {
	Metadata model.MediaMetadata
	Items    []mediaItem
}

type flexString string

func (f *flexString) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*f = ""
		return nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	*f = flexString(trimmed)
	return nil
}

type mediaDict struct {
	PK             flexString       `json:"pk"`
	ID             flexString       `json:"id"`
	Code           string           `json:"code"`
	Permalink      string           `json:"permalink"`
	MediaType      int              `json:"media_type"`
	LikeCount      int64            `json:"like_count"`
	OriginalWidth  int              `json:"original_width"`
	OriginalHeight int              `json:"original_height"`
	User           mediaUser        `json:"user"`
	Caption        *mediaCaption    `json:"caption"`
	ImageVersions  *imageVersions2  `json:"image_versions2"`
	VideoVersions  []videoCandidate `json:"video_versions"`
	CarouselMedia  []carouselItem   `json:"carousel_media"`
	Audio          *audioInfo       `json:"audio"`
	TextAppInfo    *textPostAppInfo `json:"text_post_app_info"`
}

type mediaUser struct {
	Username  string `json:"username"`
	FullName  string `json:"full_name"`
	IsPrivate bool   `json:"text_post_app_is_private"`
}

type mediaCaption struct {
	Text string `json:"text"`
}

type imageVersions2 struct {
	Candidates []mediaCandidate `json:"candidates"`
}

type mediaCandidate struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type videoCandidate struct {
	Type   int    `json:"type"`
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type carouselItem struct {
	MediaType     int              `json:"media_type"`
	ImageVersions *imageVersions2  `json:"image_versions2"`
	VideoVersions []videoCandidate `json:"video_versions"`
}

type audioInfo struct {
	AudioSrc string `json:"audio_src"`
}

type textPostAppInfo struct {
	IsPostUnavailable bool           `json:"is_post_unavailable"`
	TextFragments     *textFragments `json:"text_fragments"`
}

type textFragments struct {
	Fragments []textFragment `json:"fragments"`
}

type textFragment struct {
	Plaintext string `json:"plaintext"`
}

type htmlScanResult struct {
	JSONScripts [][]byte
	OGVideo     string
	OGImage     string
	OGTitle     string
	OGDesc      string
	PageTitle   string
}

func parsePostFromHTML(body []byte, targetURL string) (extractedPost, error) {
	scanned := scanHTML(body)
	shortcode := extractShortcode(targetURL)

	media, foundTarget, sawTargetNull := findTargetMedia(scanned.JSONScripts, shortcode)
	if media != nil {
		return buildExtractedPost(media, targetURL)
	}
	if sawTargetNull {
		return extractedPost{}, fmt.Errorf("%w: threads post is unavailable or deleted", ytdlp.ErrMediaNotFound)
	}

	if ytdlp.IsHTTPURL(scanned.OGVideo) {
		return buildOGFallbackPost(scanned, targetURL, shortcode), nil
	}

	if !foundTarget && looksLikeBlockedPage(scanned, body) {
		return extractedPost{}, fmt.Errorf("%w: threads requires authentication or blocked the request", ytdlp.ErrUpstreamBlocked)
	}

	return extractedPost{}, fmt.Errorf("%w: no downloadable media found in threads post", ytdlp.ErrMediaNotFound)
}

func scanHTML(body []byte) htmlScanResult {
	var res htmlScanResult
	tokenizer := html.NewTokenizer(bytes.NewReader(body))

	var inJSONScript bool
	var inTitle bool
	var textBuf bytes.Buffer

	for {
		tt := tokenizer.Next()
		switch tt {
		case html.ErrorToken:
			return res

		case html.StartTagToken:
			tn, hasAttr := tokenizer.TagName()
			tag := strings.ToLower(string(tn))
			switch tag {
			case "meta":
				if hasAttr {
					parseMetaTag(tokenizer, &res)
				}
			case "script":
				if hasAttr && isJSONScriptTag(tokenizer) {
					inJSONScript = true
					textBuf.Reset()
				}
			case "title":
				inTitle = true
				textBuf.Reset()
			}

		case html.SelfClosingTagToken:
			tn, hasAttr := tokenizer.TagName()
			if string(tn) == "meta" && hasAttr {
				parseMetaTag(tokenizer, &res)
			}

		case html.TextToken:
			if inJSONScript || inTitle {
				textBuf.Write(tokenizer.Text())
			}

		case html.EndTagToken:
			tn, _ := tokenizer.TagName()
			tag := strings.ToLower(string(tn))
			if tag == "script" && inJSONScript {
				trimmed := bytes.TrimSpace(textBuf.Bytes())
				if len(trimmed) > 0 {
					cp := make([]byte, len(trimmed))
					copy(cp, trimmed)
					res.JSONScripts = append(res.JSONScripts, cp)
				}
				inJSONScript = false
				textBuf.Reset()
			} else if tag == "title" && inTitle {
				res.PageTitle = strings.TrimSpace(textBuf.String())
				inTitle = false
				textBuf.Reset()
			}
		}
	}
}

func isJSONScriptTag(tokenizer *html.Tokenizer) bool {
	for {
		k, v, more := tokenizer.TagAttr()
		if strings.EqualFold(string(k), "type") && strings.EqualFold(strings.TrimSpace(string(v)), "application/json") {
			return true
		}
		if !more {
			return false
		}
	}
}

func parseMetaTag(tokenizer *html.Tokenizer, res *htmlScanResult) {
	var prop, name, content string
	for {
		k, v, more := tokenizer.TagAttr()
		key := strings.ToLower(string(k))
		val := strings.TrimSpace(string(v))
		switch key {
		case "property":
			prop = strings.ToLower(val)
		case "name":
			name = strings.ToLower(val)
		case "content":
			content = val
		}
		if !more {
			break
		}
	}

	if content == "" {
		return
	}
	switch {
	case prop == "og:video" || prop == "og:video:url" || prop == "og:video:secure_url":
		if res.OGVideo == "" {
			res.OGVideo = content
		}
	case prop == "og:image":
		if res.OGImage == "" {
			res.OGImage = content
		}
	case prop == "og:title":
		if res.OGTitle == "" {
			res.OGTitle = content
		}
	case prop == "og:description" || name == "description":
		if res.OGDesc == "" {
			res.OGDesc = content
		}
	}
}

func findTargetMedia(scripts [][]byte, shortcode string) (media *mediaDict, foundTarget bool, sawTargetNull bool) {
	for _, raw := range scripts {
		if !bytes.Contains(raw, []byte("BarcelonaPostPageTargetQuery")) &&
			!bytes.Contains(raw, []byte("BarcelonaPostPageDirectQuery")) {
			continue
		}

		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			continue
		}
		if m, matched, isNull := extractFromTargetPreloader(doc, shortcode); matched {
			return m, true, isNull
		}
	}

	if shortcode != "" {
		needle := []byte(shortcode)
		for _, raw := range scripts {
			if !bytes.Contains(raw, needle) {
				continue
			}
			var doc any
			if err := json.Unmarshal(raw, &doc); err != nil {
				continue
			}
			if m := findMediaByShortcode(doc, shortcode); m != nil {
				return m, true, false
			}
		}
	}

	return nil, false, false
}

func extractFromTargetPreloader(node any, shortcode string) (media *mediaDict, matched bool, sawNull bool) {
	arr, ok := node.([]any)
	if ok {
		if len(arr) >= 2 {
			if key, ok := arr[0].(string); ok &&
				(strings.Contains(key, "BarcelonaPostPageTargetQuery") || strings.Contains(key, "BarcelonaPostPageDirectQuery")) {
				if payload, ok := arr[1].(map[string]any); ok {
					return extractMediaFromPreloaderPayload(payload, shortcode)
				}
			}
		}
		for _, item := range arr {
			if m, found, isNull := extractFromTargetPreloader(item, shortcode); found {
				return m, true, isNull
			}
		}
		return nil, false, false
	}

	if obj, ok := node.(map[string]any); ok {
		for _, val := range obj {
			if m, found, isNull := extractFromTargetPreloader(val, shortcode); found {
				return m, true, isNull
			}
		}
	}
	return nil, false, false
}

func extractMediaFromPreloaderPayload(payload map[string]any, shortcode string) (media *mediaDict, matched bool, sawNull bool) {
	bbox, ok := payload["__bbox"].(map[string]any)
	if !ok {
		return nil, false, false
	}
	result, ok := bbox["result"].(map[string]any)
	if !ok {
		return nil, false, false
	}
	data, ok := result["data"].(map[string]any)
	if !ok {
		return nil, false, false
	}

	if rawMedia, exists := data["media"]; exists {
		if rawMedia == nil {
			return nil, true, true
		}
		if m := decodeMediaDict(rawMedia); m != nil {
			return m, true, false
		}
	}

	if m := findMediaByShortcode(data, shortcode); m != nil {
		return m, true, false
	}
	return nil, false, false
}

func findMediaByShortcode(node any, shortcode string) *mediaDict {
	switch typed := node.(type) {
	case map[string]any:
		if code, ok := typed["code"].(string); ok && code == shortcode {
			_, hasImage := typed["image_versions2"]
			_, hasVideo := typed["video_versions"]
			_, hasCarousel := typed["carousel_media"]
			_, hasUser := typed["user"]
			if hasImage || hasVideo || hasCarousel || hasUser {
				if m := decodeMediaDict(typed); m != nil {
					return m
				}
			}
		}
		for _, v := range typed {
			if m := findMediaByShortcode(v, shortcode); m != nil {
				return m
			}
		}
	case []any:
		for _, item := range typed {
			if m := findMediaByShortcode(item, shortcode); m != nil {
				return m
			}
		}
	}
	return nil
}

func decodeMediaDict(raw any) *mediaDict {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var m mediaDict
	if err := json.Unmarshal(encoded, &m); err != nil {
		return nil
	}
	return &m
}

func buildExtractedPost(m *mediaDict, targetURL string) (extractedPost, error) {
	items := extractMediaItems(m)
	if len(items) == 0 {
		if m.User.IsPrivate {
			return extractedPost{}, fmt.Errorf("%w: threads account is private", ytdlp.ErrUpstreamBlocked)
		}
		if m.TextAppInfo != nil && m.TextAppInfo.IsPostUnavailable {
			return extractedPost{}, fmt.Errorf("%w: threads post is unavailable", ytdlp.ErrMediaNotFound)
		}
		return extractedPost{}, fmt.Errorf("%w: threads post contains no downloadable media", ytdlp.ErrMediaNotFound)
	}

	caption := extractCaption(m)
	uploader := strings.TrimSpace(m.User.Username)
	if uploader == "" {
		uploader = strings.TrimSpace(m.User.FullName)
	}

	uploaderURL := ""
	if m.User.Username != "" {
		uploaderURL = "https://www.threads.com/@" + strings.TrimSpace(m.User.Username)
	}

	id := strings.TrimSpace(m.Code)
	if id == "" {
		id = strings.TrimSpace(string(m.PK))
	}
	if id == "" {
		id = strings.TrimSpace(string(m.ID))
	}

	webpage := strings.TrimSpace(m.Permalink)
	if !ytdlp.IsHTTPURL(webpage) {
		webpage = targetURL
	}

	title := caption
	if title == "" {
		if m.User.Username != "" {
			title = "Threads post by @" + m.User.Username
		} else {
			title = "Threads post"
		}
	}

	thumbnail := ""
	if m.ImageVersions != nil {
		if bestImg, ok := pickBestImage(m.ImageVersions.Candidates); ok {
			thumbnail = bestImg.URL
		}
	}

	formats := make([]model.FormatOption, 0, len(items))
	for i, item := range items {
		res := ""
		if item.Width > 0 && item.Height > 0 {
			res = fmt.Sprintf("%dx%d", item.Width, item.Height)
		}
		formats = append(formats, model.FormatOption{
			FormatID:   fmt.Sprintf("%s-%d", item.Kind, i+1),
			Extension:  item.Extension,
			Resolution: res,
			Width:      item.Width,
			Height:     item.Height,
			DirectURL:  item.URL,
		})
	}

	return extractedPost{
		Metadata: model.MediaMetadata{
			ID:          id,
			Title:       title,
			Description: caption,
			Uploader:    uploader,
			UploaderURL: uploaderURL,
			Thumbnail:   thumbnail,
			WebpageURL:  webpage,
			Extractor:   "threads",
			Platform:    model.PlatformThreads.String(),
			LikeCount:   m.LikeCount,
			Formats:     formats,
		},
		Items: items,
	}, nil
}

func extractMediaItems(m *mediaDict) []mediaItem {
	var items []mediaItem

	if len(m.CarouselMedia) > 0 {
		for _, c := range m.CarouselMedia {
			if vid, ok := pickBestVideo(c.VideoVersions); ok {
				items = append(items, vid)
				continue
			}
			if c.ImageVersions != nil {
				if img, ok := pickBestImage(c.ImageVersions.Candidates); ok {
					items = append(items, img)
				}
			}
		}
		return items
	}

	if vid, ok := pickBestVideo(m.VideoVersions); ok {
		return []mediaItem{vid}
	}
	if m.Audio != nil && ytdlp.IsHTTPURL(m.Audio.AudioSrc) {
		ext := inferExtFromURL(m.Audio.AudioSrc, "m4a")
		return []mediaItem{{
			Kind:      model.MediaKindAudio,
			URL:       strings.TrimSpace(m.Audio.AudioSrc),
			Extension: ext,
		}}
	}
	if m.ImageVersions != nil {
		if img, ok := pickBestImage(m.ImageVersions.Candidates); ok {
			return []mediaItem{img}
		}
	}
	return nil
}

func pickBestVideo(candidates []videoCandidate) (mediaItem, bool) {
	bestArea := -1
	var best videoCandidate
	for _, c := range candidates {
		if !ytdlp.IsHTTPURL(c.URL) {
			continue
		}
		area := c.Width * c.Height
		if area > bestArea {
			bestArea = area
			best = c
		}
	}
	if bestArea < 0 {
		return mediaItem{}, false
	}
	return mediaItem{
		Kind:      model.MediaKindVideo,
		URL:       strings.TrimSpace(best.URL),
		Width:     best.Width,
		Height:    best.Height,
		Extension: inferExtFromURL(best.URL, "mp4"),
	}, true
}

func pickBestImage(candidates []mediaCandidate) (mediaItem, bool) {
	bestArea := -1
	var best mediaCandidate
	for _, c := range candidates {
		if !ytdlp.IsHTTPURL(c.URL) {
			continue
		}
		area := c.Width * c.Height
		if area > bestArea {
			bestArea = area
			best = c
		}
	}
	if bestArea < 0 {
		return mediaItem{}, false
	}
	return mediaItem{
		Kind:      model.MediaKindImage,
		URL:       strings.TrimSpace(best.URL),
		Width:     best.Width,
		Height:    best.Height,
		Extension: inferExtFromURL(best.URL, "jpg"),
	}, true
}

func extractCaption(m *mediaDict) string {
	if m.Caption != nil && strings.TrimSpace(m.Caption.Text) != "" {
		return strings.TrimSpace(m.Caption.Text)
	}
	if m.TextAppInfo != nil && m.TextAppInfo.TextFragments != nil {
		var parts []string
		for _, frag := range m.TextAppInfo.TextFragments.Fragments {
			if p := strings.TrimSpace(frag.Plaintext); p != "" {
				parts = append(parts, p)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, " ")
		}
	}
	return ""
}

func buildOGFallbackPost(scanned htmlScanResult, targetURL, shortcode string) extractedPost {
	ext := inferExtFromURL(scanned.OGVideo, "mp4")
	title := scanned.OGDesc
	if title == "" {
		title = scanned.OGTitle
	}
	return extractedPost{
		Metadata: model.MediaMetadata{
			ID:          shortcode,
			Title:       title,
			Description: scanned.OGDesc,
			Thumbnail:   scanned.OGImage,
			WebpageURL:  targetURL,
			Extractor:   "threads",
			Platform:    model.PlatformThreads.String(),
			Formats: []model.FormatOption{
				{
					FormatID:  "video-1",
					Extension: ext,
					DirectURL: scanned.OGVideo,
				},
			},
		},
		Items: []mediaItem{
			{
				Kind:      model.MediaKindVideo,
				URL:       scanned.OGVideo,
				Extension: ext,
			},
		},
	}
}

func looksLikeBlockedPage(scanned htmlScanResult, body []byte) bool {
	lowerTitle := strings.ToLower(scanned.PageTitle)
	if strings.Contains(lowerTitle, "login") || strings.Contains(lowerTitle, "sign in") {
		return true
	}
	lowerBody := bytes.ToLower(body)
	return bytes.Contains(lowerBody, []byte("/accounts/login")) && len(scanned.JSONScripts) == 0
}

func extractShortcode(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i := 0; i < len(segments)-1; i++ {
		if segments[i] == "post" || segments[i] == "t" {
			return strings.TrimSpace(segments[i+1])
		}
	}
	return ""
}

func inferExtFromURL(rawURL, fallback string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fallback
	}
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(parsed.Path)), ".")
	if ext == "" || len(ext) > 5 {
		return fallback
	}
	if ext == "jpeg" {
		return "jpg"
	}
	return ext
}

func parseFilesizeBytes(raw string) (int64, bool) {
	s := strings.ToUpper(strings.TrimSpace(raw))
	if s == "" {
		return 0, false
	}

	unitMultipliers := []struct {
		suffix string
		mult   int64
	}{
		{"GIB", 1024 * 1024 * 1024},
		{"GB", 1000 * 1000 * 1000},
		{"G", 1024 * 1024 * 1024},
		{"MIB", 1024 * 1024},
		{"MB", 1000 * 1000},
		{"M", 1024 * 1024},
		{"KIB", 1024},
		{"KB", 1000},
		{"K", 1024},
		{"B", 1},
	}

	mult := int64(1)
	numPart := s
	for _, u := range unitMultipliers {
		if strings.HasSuffix(s, u.suffix) {
			mult = u.mult
			numPart = strings.TrimSpace(strings.TrimSuffix(s, u.suffix))
			break
		}
	}

	val, err := strconv.ParseFloat(numPart, 64)
	if err != nil || val <= 0 {
		return 0, false
	}
	return int64(val * float64(mult)), true
}

func sanitizeFilenamePart(s string) string {
	var b strings.Builder
	for _, r := range s {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if isAlphaNum || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		return "threads"
	}
	if len(out) > 48 {
		return out[:48]
	}
	return out
}

func filterMediaItems(items []mediaItem, mediaType string, maxItems int) ([]mediaItem, error) {
	normalized := strings.ToLower(strings.TrimSpace(mediaType))
	var filtered []mediaItem

	switch normalized {
	case model.MediaKindVideo:
		for _, it := range items {
			if it.Kind == model.MediaKindVideo {
				filtered = append(filtered, it)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("%w: threads post contains no video", ytdlp.ErrMediaNotFound)
		}
	case model.MediaKindAudio:
		for _, it := range items {
			if it.Kind == model.MediaKindAudio || it.Kind == model.MediaKindVideo {
				filtered = append(filtered, it)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("%w: threads post contains no video or audio stream", ytdlp.ErrMediaNotFound)
		}
	default:
		filtered = slices.Clone(items)
	}

	if maxItems > 0 && len(filtered) > maxItems {
		filtered = filtered[:maxItems]
	}
	return filtered, nil
}
