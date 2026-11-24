package ytdlp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/justpiple/ytdlp-api/internal/model"
)

func TestBuildCompatibilityPlan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		filePath           string
		streams            []probeStream
		wantIsVideo        bool
		wantNeedsWork      bool
		wantVideoTranscode bool
		wantAudioTranscode bool
		wantHasAudio       bool
		wantVideoIdx       int
	}{
		{
			name:     "compatible h264 avc1 + aac mp4",
			filePath: "/tmp/01_vid.mp4",
			streams: []probeStream{
				{Index: 0, CodecType: "video", CodecName: "h264", CodecTagString: "avc1", PixFmt: "yuv420p"},
				{Index: 1, CodecType: "audio", CodecName: "aac", CodecTagString: "mp4a"},
			},
			wantIsVideo:  true,
			wantHasAudio: true,
		},
		{
			name:     "compatible h264 video-only mp4",
			filePath: "/tmp/01_vid.mp4",
			streams: []probeStream{
				{Index: 0, CodecType: "video", CodecName: "h264", CodecTagString: "avc1", PixFmt: "yuvj420p"},
			},
			wantIsVideo: true,
		},
		{
			name:     "vp9 vp09 video-only mp4 requires video transcode",
			filePath: "/tmp/01_DgGv7HKztVz.mp4",
			streams: []probeStream{
				{Index: 0, CodecType: "video", CodecName: "vp9", CodecTagString: "vp09", PixFmt: "yuv420p"},
			},
			wantIsVideo:        true,
			wantNeedsWork:      true,
			wantVideoTranscode: true,
		},
		{
			name:     "av1 + opus mp4 requires both video and audio transcode",
			filePath: "/tmp/01_av1.mp4",
			streams: []probeStream{
				{Index: 0, CodecType: "video", CodecName: "av1", CodecTagString: "av01", PixFmt: "yuv420p"},
				{Index: 1, CodecType: "audio", CodecName: "opus", CodecTagString: "Opus"},
			},
			wantIsVideo:        true,
			wantNeedsWork:      true,
			wantVideoTranscode: true,
			wantAudioTranscode: true,
			wantHasAudio:       true,
		},
		{
			name:     "10-bit h264 mp4 requires video transcode",
			filePath: "/tmp/01_10bit.mp4",
			streams: []probeStream{
				{Index: 0, CodecType: "video", CodecName: "h264", CodecTagString: "avc1", PixFmt: "yuv420p10le"},
				{Index: 1, CodecType: "audio", CodecName: "aac", CodecTagString: "mp4a"},
			},
			wantIsVideo:        true,
			wantNeedsWork:      true,
			wantVideoTranscode: true,
			wantHasAudio:       true,
		},
		{
			name:     "hevc hev1 mp4 requires video transcode",
			filePath: "/tmp/01_hevc.mp4",
			streams: []probeStream{
				{Index: 0, CodecType: "video", CodecName: "hevc", CodecTagString: "hev1", PixFmt: "yuv420p"},
			},
			wantIsVideo:        true,
			wantNeedsWork:      true,
			wantVideoTranscode: true,
		},
		{
			name:     "h264 + opus mp4 only transcodes audio",
			filePath: "/tmp/01_opus.mp4",
			streams: []probeStream{
				{Index: 0, CodecType: "video", CodecName: "h264", CodecTagString: "avc1", PixFmt: "yuv420p"},
				{Index: 1, CodecType: "audio", CodecName: "opus", CodecTagString: "Opus"},
			},
			wantIsVideo:        true,
			wantNeedsWork:      true,
			wantVideoTranscode: false,
			wantAudioTranscode: true,
			wantHasAudio:       true,
		},
		{
			name:     "h264 + aac in webm remuxes to mp4",
			filePath: "/tmp/01_vid.webm",
			streams: []probeStream{
				{Index: 0, CodecType: "video", CodecName: "h264", CodecTagString: "[0][0][0][0]", PixFmt: "yuv420p"},
				{Index: 1, CodecType: "audio", CodecName: "aac"},
			},
			wantIsVideo:   true,
			wantNeedsWork: true,
			wantHasAudio:  true,
		},
		{
			name:     "skips attached_pic stream when finding primary video",
			filePath: "/tmp/01_thumb.mp4",
			streams: []probeStream{
				{Index: 0, CodecType: "video", CodecName: "mjpeg", Disposition: map[string]int{"attached_pic": 1}},
				{Index: 1, CodecType: "video", CodecName: "h264", CodecTagString: "avc1", PixFmt: "yuv420p"},
			},
			wantIsVideo:  true,
			wantVideoIdx: 1,
		},
		{
			name:     "audio with attached_pic only is not treated as video",
			filePath: "/tmp/01_audio.mp4",
			streams: []probeStream{
				{Index: 0, CodecType: "audio", CodecName: "aac"},
				{Index: 1, CodecType: "video", CodecName: "mjpeg", Disposition: map[string]int{"attached_pic": 1}},
			},
			wantIsVideo: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)
			plan, isVideo := buildCompatibilityPlan(tt.filePath, tt.streams)
			is.Equal(tt.wantIsVideo, isVideo)
			if !tt.wantIsVideo {
				return
			}
			is.Equal(tt.wantNeedsWork, plan.needsWork)
			is.Equal(tt.wantVideoTranscode, plan.needVideoTranscode)
			is.Equal(tt.wantAudioTranscode, plan.needAudioTranscode)
			is.Equal(tt.wantHasAudio, plan.hasAudio)
			is.Equal(tt.wantVideoIdx, plan.videoStreamIndex)
		})
	}
}

func TestBuildTranscodeArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		plan        compatibilityPlan
		wantAll     []string
		wantMissing []string
	}{
		{
			name: "vp9 video-only transcodes to libx264 yuv420p",
			plan: compatibilityPlan{
				needsWork:          true,
				needVideoTranscode: true,
				videoStreamIndex:   0,
			},
			wantAll:     []string{"-map", "0:0", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-movflags", "+faststart"},
			wantMissing: []string{"-c:a"},
		},
		{
			name: "h264 + opus copies video and transcodes audio to aac",
			plan: compatibilityPlan{
				needsWork:          true,
				needAudioTranscode: true,
				hasAudio:           true,
				videoStreamIndex:   0,
				audioStreamIndex:   1,
			},
			wantAll:     []string{"-map", "0:0", "-map", "0:1", "-c:v", "copy", "-c:a", "aac", "-b:a", "192k"},
			wantMissing: []string{"libx264"},
		},
		{
			name: "h264 + aac container remux copies both streams",
			plan: compatibilityPlan{
				needsWork:        true,
				hasAudio:         true,
				videoStreamIndex: 0,
				audioStreamIndex: 1,
			},
			wantAll:     []string{"-c:v", "copy", "-c:a", "copy", "-movflags", "+faststart"},
			wantMissing: []string{"libx264"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := assert.New(t)
			args := buildTranscodeArgs("/tmp/in.mp4", "/tmp/out.mp4", tt.plan)
			for _, flag := range tt.wantAll {
				is.Contains(args, flag)
			}
			for _, flag := range tt.wantMissing {
				is.NotContains(args, flag)
			}
			is.Equal("/tmp/out.mp4", args[len(args)-1])
		})
	}
}

func TestEnsureH264CompatibleFiles(t *testing.T) {
	t.Parallel()

	t.Run("skips when ffmpegPath is empty", func(t *testing.T) {
		t.Parallel()
		is := assert.New(t)
		req := require.New(t)

		paths := []string{"/tmp/01_vid.mp4"}
		got, err := EnsureH264CompatibleFiles(t.Context(), "", paths, nil)
		req.NoError(err)
		is.Equal(paths, got)
	})

	t.Run("skips images and already compatible h264 mp4 without invoking ffmpeg", func(t *testing.T) {
		t.Parallel()
		is := assert.New(t)
		req := require.New(t)

		binDir := t.TempDir()
		workDir := t.TempDir()
		ffmpegLog := filepath.Join(workDir, "ffmpeg.log")

		writeFakeScript(t, binDir, "ffprobe", `#!/bin/sh
printf '{"streams":[{"index":0,"codec_type":"video","codec_name":"h264","codec_tag_string":"avc1","pix_fmt":"yuv420p"},{"index":1,"codec_type":"audio","codec_name":"aac","codec_tag_string":"mp4a"}]}\n'
`)
		ffmpegBin := writeFakeScript(t, binDir, "ffmpeg", fmt.Sprintf(`#!/bin/sh
echo "CALLED" >> %q
exit 1
`, ffmpegLog))

		imgPath := filepath.Join(workDir, "01.jpg")
		mp4Path := filepath.Join(workDir, "02_ok.mp4")
		req.NoError(os.WriteFile(imgPath, []byte("jpg"), 0o640))
		req.NoError(os.WriteFile(mp4Path, []byte("h264"), 0o640))

		got, err := EnsureH264CompatibleFiles(t.Context(), ffmpegBin, []string{imgPath, mp4Path}, nil)
		req.NoError(err)
		is.Equal([]string{imgPath, mp4Path}, got)

		_, statErr := os.Stat(ffmpegLog)
		is.True(os.IsNotExist(statErr), "ffmpeg must not run when already compatible")
	})

	t.Run("transcodes vp9 mp4 in place and emits postprocessing progress", func(t *testing.T) {
		t.Parallel()
		is := assert.New(t)
		req := require.New(t)

		binDir := t.TempDir()
		workDir := t.TempDir()
		argsLog := filepath.Join(workDir, "ffmpeg-args.log")

		writeFakeScript(t, binDir, "ffprobe", `#!/bin/sh
printf '{"streams":[{"index":0,"codec_type":"video","codec_name":"vp9","codec_tag_string":"vp09","pix_fmt":"yuv420p"}]}\n'
`)
		ffmpegBin := writeFakeScript(t, binDir, "ffmpeg", fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" > %q
for last in "$@"; do :; done
printf 'transcoded-h264' > "$last"
`, argsLog))

		vp9Path := filepath.Join(workDir, "01_DgGv7HKztVz.mp4")
		req.NoError(os.WriteFile(vp9Path, []byte("vp9-payload"), 0o640))

		var events []model.ProgressEvent
		got, err := EnsureH264CompatibleFiles(t.Context(), ffmpegBin, []string{vp9Path}, func(ev model.ProgressEvent) {
			events = append(events, ev)
		})
		req.NoError(err)
		is.Equal([]string{vp9Path}, got)

		content, readErr := os.ReadFile(vp9Path)
		req.NoError(readErr)
		is.Equal("transcoded-h264", string(content))

		loggedArgs, logErr := os.ReadFile(argsLog)
		req.NoError(logErr)
		is.Contains(string(loggedArgs), "-c:v libx264")
		is.Contains(string(loggedArgs), "-pix_fmt yuv420p")

		req.Len(events, 1)
		is.Equal(model.JobStatusPostProcessing.String(), events[0].Status)
	})

	t.Run("remuxes webm to mp4 and removes original webm", func(t *testing.T) {
		t.Parallel()
		is := assert.New(t)
		req := require.New(t)

		binDir := t.TempDir()
		workDir := t.TempDir()

		writeFakeScript(t, binDir, "ffprobe", `#!/bin/sh
printf '{"streams":[{"index":0,"codec_type":"video","codec_name":"h264","codec_tag_string":"[0][0][0][0]","pix_fmt":"yuv420p"},{"index":1,"codec_type":"audio","codec_name":"aac"}]}\n'
`)
		ffmpegBin := writeFakeScript(t, binDir, "ffmpeg", `#!/bin/sh
for last in "$@"; do :; done
printf 'remuxed-mp4' > "$last"
`)

		webmPath := filepath.Join(workDir, "01_clip.webm")
		req.NoError(os.WriteFile(webmPath, []byte("webm-payload"), 0o640))

		got, err := EnsureH264CompatibleFiles(t.Context(), ffmpegBin, []string{webmPath}, nil)
		req.NoError(err)

		wantMP4 := filepath.Join(workDir, "01_clip.mp4")
		is.Equal([]string{wantMP4}, got)

		_, oldStatErr := os.Stat(webmPath)
		is.True(os.IsNotExist(oldStatErr), "original webm must be removed after remux")

		content, readErr := os.ReadFile(wantMP4)
		req.NoError(readErr)
		is.Equal("remuxed-mp4", string(content))
	})

	t.Run("cleans up temporary file when ffmpeg fails", func(t *testing.T) {
		t.Parallel()
		is := assert.New(t)
		req := require.New(t)

		binDir := t.TempDir()
		workDir := t.TempDir()

		writeFakeScript(t, binDir, "ffprobe", `#!/bin/sh
printf '{"streams":[{"index":0,"codec_type":"video","codec_name":"vp9","codec_tag_string":"vp09","pix_fmt":"yuv420p"}]}\n'
`)
		ffmpegBin := writeFakeScript(t, binDir, "ffmpeg", `#!/bin/sh
for last in "$@"; do :; done
printf 'partial' > "$last"
echo "encoder error" >&2
exit 1
`)

		vp9Path := filepath.Join(workDir, "01_fail.mp4")
		req.NoError(os.WriteFile(vp9Path, []byte("vp9-payload"), 0o640))

		_, err := EnsureH264CompatibleFiles(t.Context(), ffmpegBin, []string{vp9Path}, nil)
		req.Error(err)
		is.Contains(err.Error(), "encoder error")

		entries, readErr := os.ReadDir(workDir)
		req.NoError(readErr)
		for _, e := range entries {
			is.False(strings.Contains(e.Name(), ".h264.tmp.mp4"), "temp file must be cleaned up on failure")
		}
	})
}

func writeFakeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o755))
	return p
}
