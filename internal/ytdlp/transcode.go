package ytdlp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/justpiple/ytdlp-api/internal/model"
)

type probeOutput struct {
	Streams []probeStream `json:"streams"`
}

type probeStream struct {
	Index          int            `json:"index"`
	CodecType      string         `json:"codec_type"`
	CodecName      string         `json:"codec_name"`
	CodecTagString string         `json:"codec_tag_string"`
	PixFmt         string         `json:"pix_fmt"`
	Disposition    map[string]int `json:"disposition"`
}

type compatibilityPlan struct {
	needsWork          bool
	needVideoTranscode bool
	needAudioTranscode bool
	hasAudio           bool
	videoStreamIndex   int
	audioStreamIndex   int
}

// EnsureH264CompatibleFiles inspects downloaded video files and remuxes or transcodes
// any non-H.264/AAC or non-MP4 video files (such as VP9, AV1, HEVC hev1, or Opus-in-MP4)
// into QuickTime- and mobile-compatible H.264 (yuv420p) + AAC MP4 files.
func EnsureH264CompatibleFiles(
	ctx context.Context,
	ffmpegPath string,
	filePaths []string,
	onProgress ProgressCallback,
) ([]string, error) {
	ffmpegBin, ffprobeBin := resolveFFmpegAndFFprobe(ffmpegPath)
	if ffmpegBin == "" || ffprobeBin == "" {
		return filePaths, nil
	}

	result := make([]string, len(filePaths))
	copy(result, filePaths)

	for i, path := range filePaths {
		if !isVideoContainerPath(path) {
			continue
		}

		updatedPath, err := ensureH264CompatibleFile(ctx, ffmpegBin, ffprobeBin, path, onProgress)
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
	filePath string,
	onProgress ProgressCallback,
) (string, error) {
	streams, err := probeStreams(ctx, ffprobeBin, filePath)
	if err != nil {
		return "", fmt.Errorf("probing %q: %w", filepath.Base(filePath), err)
	}

	plan, isVideo := buildCompatibilityPlan(filePath, streams)
	if !isVideo || !plan.needsWork {
		return filePath, nil
	}

	if onProgress != nil {
		onProgress(model.ProgressEvent{
			Status:          model.JobStatusPostProcessing.String(),
			ProgressPercent: 99,
			Message:         "transcoding video to H.264 MP4 with ffmpeg",
		})
	}

	baseNoExt := strings.TrimSuffix(filePath, filepath.Ext(filePath))
	targetPath := baseNoExt + ".mp4"
	tmpOutPath := baseNoExt + ".h264.tmp.mp4"
	defer func() {
		_ = os.Remove(tmpOutPath)
	}()

	args := buildTranscodeArgs(filePath, tmpOutPath, plan)
	if err := runFFmpeg(ctx, ffmpegBin, args...); err != nil {
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

	audioCodec := strings.ToLower(strings.TrimSpace(audio.CodecName))
	needAudioTranscode := hasAudio && audioCodec != "aac"

	isMP4 := strings.EqualFold(filepath.Ext(filePath), ".mp4")
	needRemux := !isMP4 || !videoTagOK

	return compatibilityPlan{
		needsWork:          needVideoTranscode || needAudioTranscode || needRemux,
		needVideoTranscode: needVideoTranscode,
		needAudioTranscode: needAudioTranscode,
		hasAudio:           hasAudio,
		videoStreamIndex:   video.Index,
		audioStreamIndex:   audio.Index,
	}, true
}

func buildTranscodeArgs(inputPath, outputPath string, plan compatibilityPlan) []string {
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
			"-c:v", "libx264",
			"-preset", "fast",
			"-crf", "22",
			"-pix_fmt", "yuv420p",
			// Scale to even dimensions because libx264 with yuv420p fails on odd widths or heights.
			"-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2",
		)
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

func probeStreams(ctx context.Context, ffprobeBin, filePath string) ([]probeStream, error) {
	cmd := exec.CommandContext(
		ctx,
		ffprobeBin,
		"-v", "error",
		"-show_entries", "stream=index,codec_type,codec_name,codec_tag_string,pix_fmt:stream_disposition=attached_pic",
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
			return nil, fmt.Errorf("%s: %w", msg, err)
		}
		return nil, err
	}

	var parsed probeOutput
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		return nil, fmt.Errorf("parsing ffprobe json: %w", err)
	}
	return parsed.Streams, nil
}

func runFFmpeg(ctx context.Context, ffmpegBin string, args ...string) error {
	cmd := exec.CommandContext(ctx, ffmpegBin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := summarizeStderr(stderr.String())
		if msg != "" {
			return fmt.Errorf("%s: %w", msg, err)
		}
		return err
	}
	return nil
}

func resolveFFmpegAndFFprobe(ffmpegPath string) (string, string) {
	ffmpegLoc := resolveFFmpegLocation(ffmpegPath)
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
