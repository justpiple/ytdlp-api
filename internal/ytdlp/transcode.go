package ytdlp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/justpiple/ytdlp-api/internal/model"
)

const (
	defaultFFmpegPreset       = "veryfast"
	transcodeProgressInterval = 500 * time.Millisecond
)

type probeOutput struct {
	Streams []probeStream `json:"streams"`
	Format  probeFormat   `json:"format"`
}

type probeFormat struct {
	Duration string `json:"duration"`
}

type probeStream struct {
	Index          int            `json:"index"`
	CodecType      string         `json:"codec_type"`
	CodecName      string         `json:"codec_name"`
	CodecTagString string         `json:"codec_tag_string"`
	PixFmt         string         `json:"pix_fmt"`
	Width          int            `json:"width"`
	Height         int            `json:"height"`
	Disposition    map[string]int `json:"disposition"`
}

type compatibilityPlan struct {
	needsWork          bool
	needVideoTranscode bool
	needAudioTranscode bool
	needsScale         bool
	hasAudio           bool
	videoStreamIndex   int
	audioStreamIndex   int
	preset             string
	durationSeconds    float64
}

// EnsureH264CompatibleFiles inspects downloaded video files and remuxes or transcodes
// any non-H.264/AAC or non-MP4 video files (such as VP9, AV1, HEVC hev1, or Opus-in-MP4)
// into QuickTime- and mobile-compatible H.264 (yuv420p) + AAC MP4 files.
func EnsureH264CompatibleFiles(
	ctx context.Context,
	ffmpegPath string,
	preset string,
	filePaths []string,
	onProgress ProgressCallback,
) ([]string, error) {
	ffmpegBin, ffprobeBin := resolveFFmpegAndFFprobe(ffmpegPath)
	if ffmpegBin == "" || ffprobeBin == "" {
		return filePaths, nil
	}

	resolvedPreset := strings.ToLower(strings.TrimSpace(preset))
	if resolvedPreset == "" {
		resolvedPreset = defaultFFmpegPreset
	}

	result := make([]string, len(filePaths))
	copy(result, filePaths)

	for i, path := range filePaths {
		if !isVideoContainerPath(path) {
			continue
		}

		updatedPath, err := ensureH264CompatibleFile(
			ctx,
			ffmpegBin,
			ffprobeBin,
			resolvedPreset,
			path,
			onProgress,
		)
		if err != nil {
			return nil, err
		}
		result[i] = updatedPath
	}

	return result, nil
}

func ensureH264CompatibleFile(
	ctx context.Context,
	ffmpegBin string,
	ffprobeBin string,
	preset string,
	filePath string,
	onProgress ProgressCallback,
) (string, error) {
	streams, durationSeconds, err := probeMedia(ctx, ffprobeBin, filePath)
	if err != nil {
		return "", fmt.Errorf("probing %q: %w", filepath.Base(filePath), err)
	}

	plan, isVideo := buildCompatibilityPlan(filePath, streams)
	if !isVideo || !plan.needsWork {
		return filePath, nil
	}
	plan.preset = preset
	plan.durationSeconds = durationSeconds

	if onProgress != nil {
		initialPct := 99.0
		msg := "remuxing video to H.264 MP4 with ffmpeg"
		if plan.needVideoTranscode {
			msg = "transcoding video to H.264 MP4 with ffmpeg"
			if durationSeconds > 0 {
				initialPct = 0
			}
		} else if plan.needAudioTranscode {
			msg = "transcoding audio to AAC MP4 with ffmpeg"
		}
		onProgress(model.ProgressEvent{
			Status:          model.JobStatusPostProcessing.String(),
			ProgressPercent: initialPct,
			Message:         msg,
		})
	}

	baseNoExt := strings.TrimSuffix(filePath, filepath.Ext(filePath))
	targetPath := baseNoExt + ".mp4"
	tmpOutPath := baseNoExt + ".h264.tmp.mp4"
	defer func() {
		_ = os.Remove(tmpOutPath)
	}()

	args := buildTranscodeArgs(filePath, tmpOutPath, plan)
	if err := runFFmpeg(ctx, ffmpegBin, durationSeconds, onProgress, args...); err != nil {
		return "", fmt.Errorf("transcoding %q to h264 mp4: %w", filepath.Base(filePath), err)
	}

	if err := os.Rename(tmpOutPath, targetPath); err != nil {
		return "", fmt.Errorf("replacing %q with transcoded mp4: %w", filepath.Base(targetPath), err)
	}

	if targetPath != filePath {
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("removing original video %q: %w", filepath.Base(filePath), err)
		}
	}

	return targetPath, nil
}

func buildCompatibilityPlan(filePath string, streams []probeStream) (compatibilityPlan, bool) {
	var hasVideo bool
	var video probeStream
	var hasAudio bool
	var audio probeStream

	for _, s := range streams {
		switch s.CodecType {
		case "video":
			if s.Disposition["attached_pic"] == 1 {
				continue
			}
			if !hasVideo {
				video = s
				hasVideo = true
			}
		case "audio":
			if !hasAudio {
				audio = s
				hasAudio = true
			}
		}
	}

	if !hasVideo {
		return compatibilityPlan{}, false
	}

	codec := strings.ToLower(strings.TrimSpace(video.CodecName))
	pixFmt := strings.ToLower(strings.TrimSpace(video.PixFmt))
	tag := strings.ToLower(strings.TrimSpace(video.CodecTagString))

	videoCodecOK := codec == "h264"
	pixFmtOK := pixFmt == "yuv420p" || pixFmt == "yuvj420p"
	videoTagOK := tag == "" || tag == "avc1" || tag == "avc3" || tag == "[0][0][0][0]"
	needVideoTranscode := !videoCodecOK || !pixFmtOK
	// Skip swscale filter pass when input dimensions are already known and even.
	needsScale := needVideoTranscode && (video.Width <= 0 || video.Height <= 0 || video.Width%2 != 0 || video.Height%2 != 0)

	audioCodec := strings.ToLower(strings.TrimSpace(audio.CodecName))
	needAudioTranscode := hasAudio && audioCodec != "aac"

	isMP4 := strings.EqualFold(filepath.Ext(filePath), ".mp4")
	needRemux := !isMP4 || !videoTagOK

	return compatibilityPlan{
		needsWork:          needVideoTranscode || needAudioTranscode || needRemux,
		needVideoTranscode: needVideoTranscode,
		needAudioTranscode: needAudioTranscode,
		needsScale:         needsScale,
		hasAudio:           hasAudio,
		videoStreamIndex:   video.Index,
		audioStreamIndex:   audio.Index,
	}, true
}

func buildTranscodeArgs(inputPath, outputPath string, plan compatibilityPlan) []string {
	preset := strings.TrimSpace(plan.preset)
	if preset == "" {
		preset = defaultFFmpegPreset
	}

	args := []string{
		"-y",
		"-v", "error",
		"-i", inputPath,
		"-map", fmt.Sprintf("0:%d", plan.videoStreamIndex),
	}

	if plan.hasAudio {
		args = append(args, "-map", fmt.Sprintf("0:%d", plan.audioStreamIndex))
	}

	if plan.needVideoTranscode {
		args = append(
			args,
			"-threads", "0",
			"-c:v", "libx264",
			"-preset", preset,
			"-crf", "22",
			"-pix_fmt", "yuv420p",
		)
		if plan.needsScale {
			// Scale to even dimensions because libx264 with yuv420p fails on odd widths or heights.
			args = append(args, "-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2")
		}
	} else {
		args = append(args, "-c:v", "copy")
	}

	if plan.hasAudio {
		if plan.needAudioTranscode {
			args = append(args, "-c:a", "aac", "-b:a", "192k")
		} else {
			args = append(args, "-c:a", "copy")
		}
	}

	args = append(
		args,
		"-movflags", "+faststart",
		"-sn",
		"-dn",
		outputPath,
	)

	return args
}

func probeMedia(ctx context.Context, ffprobeBin, filePath string) ([]probeStream, float64, error) {
	cmd := exec.CommandContext(
		ctx,
		ffprobeBin,
		"-v", "error",
		"-show_entries", "stream=index,codec_type,codec_name,codec_tag_string,pix_fmt,width,height:stream_disposition=attached_pic:format=duration",
		"-of", "json",
		filePath,
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := summarizeStderr(stderr.String())
		if msg != "" {
			return nil, 0, fmt.Errorf("%s: %w", msg, err)
		}
		return nil, 0, err
	}

	var parsed probeOutput
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		return nil, 0, fmt.Errorf("parsing ffprobe json: %w", err)
	}

	var durationSeconds float64
	if parsed.Format.Duration != "" {
		if d, parseErr := strconv.ParseFloat(strings.TrimSpace(parsed.Format.Duration), 64); parseErr == nil && d > 0 {
			durationSeconds = d
		}
	}
	return parsed.Streams, durationSeconds, nil
}

func runFFmpeg(
	ctx context.Context,
	ffmpegBin string,
	durationSeconds float64,
	onProgress ProgressCallback,
	args ...string,
) error {
	ffmpegArgs := make([]string, 0, len(args)+3)
	ffmpegArgs = append(ffmpegArgs, "-progress", "pipe:1", "-nostats")
	ffmpegArgs = append(ffmpegArgs, args...)

	cmd := exec.CommandContext(ctx, ffmpegBin, ffmpegArgs...)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("creating ffmpeg stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting ffmpeg: %w", err)
	}

	scanner := bufio.NewScanner(stdoutPipe)
	var outTimeUS int64
	var speedStr string
	lastEmit := time.Time{}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		switch key {
		case "out_time_us":
			if parsed, parseErr := strconv.ParseInt(strings.TrimSpace(val), 10, 64); parseErr == nil {
				outTimeUS = parsed
			}
		case "speed":
			speedStr = cleanPlaceholder(val)
		case "progress":
			if val != "continue" || onProgress == nil || durationSeconds <= 0 || outTimeUS <= 0 {
				continue
			}
			now := time.Now()
			if !lastEmit.IsZero() && now.Sub(lastEmit) < transcodeProgressInterval {
				continue
			}
			lastEmit = now

			elapsedSec := float64(outTimeUS) / 1_000_000.0
			pct := math.Min((elapsedSec/durationSeconds)*100.0, 99.0)
			onProgress(model.ProgressEvent{
				Status:          model.JobStatusPostProcessing.String(),
				ProgressPercent: math.Round(pct*10) / 10,
				Speed:           speedStr,
				ETA:             formatTranscodeETA(durationSeconds, elapsedSec, speedStr),
				Message:         "transcoding video to H.264 MP4 with ffmpeg",
			})
		}
	}

	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if scanErr != nil && waitErr == nil {
		return fmt.Errorf("reading ffmpeg progress: %w", scanErr)
	}
	if waitErr != nil {
		msg := summarizeStderr(stderr.String())
		if msg != "" {
			return fmt.Errorf("%s: %w", msg, waitErr)
		}
		return waitErr
	}
	return nil
}

func formatTranscodeETA(totalSec, elapsedSec float64, speedStr string) string {
	remainingSec := totalSec - elapsedSec
	if remainingSec <= 0 {
		return ""
	}
	speedVal, ok := parseSpeedMultiplier(speedStr)
	if !ok {
		return ""
	}
	etaSec := int(math.Ceil(remainingSec / speedVal))
	if etaSec < 0 {
		return ""
	}
	hours := etaSec / 3600
	minutes := (etaSec % 3600) / 60
	seconds := etaSec % 60
	if hours > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%02d:%02d", minutes, seconds)
}

func parseSpeedMultiplier(speedStr string) (float64, bool) {
	trimmed := strings.TrimSpace(strings.TrimSuffix(speedStr, "x"))
	if trimmed == "" {
		return 0, false
	}
	val, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || val <= 0 {
		return 0, false
	}
	return val, true
}

func resolveFFmpegAndFFprobe(ffmpegPath string) (string, string) {
	ffmpegLoc := ResolveFFmpegLocation(ffmpegPath)
	if ffmpegLoc == "" {
		return "", ""
	}

	ext := ""
	if strings.HasSuffix(strings.ToLower(ffmpegLoc), ".exe") {
		ext = ".exe"
	}

	sibling := filepath.Join(filepath.Dir(ffmpegLoc), "ffprobe"+ext)
	if info, err := os.Stat(sibling); err == nil && !info.IsDir() {
		return ffmpegLoc, sibling
	}

	ffprobeLoc, err := exec.LookPath("ffprobe")
	if err != nil {
		return ffmpegLoc, ""
	}
	return ffmpegLoc, ffprobeLoc
}

func isVideoContainerPath(path string) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	switch ext {
	case "mp4", "mov", "webm", "mkv", "m4v", "avi", "flv", "ts", "3gp":
		return true
	default:
		return false
	}
}
