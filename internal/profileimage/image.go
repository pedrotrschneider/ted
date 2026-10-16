// Package profileimage validates and normalizes uploaded account pictures using
// the existing FFmpeg runtime. Only raster image decoders can consume uploads.
package profileimage

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"os/exec"
	"time"
)

const MaxUpload = 20 << 20
const MaxImage = 512 << 10

var ErrInvalid = errors.New("invalid profile image")

// Normalize keeps the first frame, center crops it, and removes original
// metadata. Decoder, pixel, allocation, output and execution limits also apply
// to authenticated uploads. No filenames, URLs or protocols come from input.
func Normalize(ctx context.Context, data []byte) ([]byte, error) {
	if len(data) == 0 || len(data) > MaxUpload {
		return nil, ErrInvalid
	}
	codec := ""
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		codec = "png"
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		codec = "mjpeg"
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		codec = "gif"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		codec = "webp"
	default:
		return nil, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin",
		"-max_alloc", "268435456", "-threads", "1", "-max_pixels", "40000000",
		"-protocol_whitelist", "pipe", "-f", "image2pipe", "-c:v", codec, "-i", "pipe:0",
		"-frames:v", "1", "-vf", "scale=256:256:force_original_aspect_ratio=increase,crop=256:256,setsar=1",
		"-map_metadata", "-1", "-an", "-threads", "1", "-c:v", "png", "-f", "image2pipe", "pipe:1")
	cmd.Stdin = bytes.NewReader(data)
	var output limitedBuffer
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrInvalid
	}
	image, err := png.Decode(bytes.NewReader(output.Bytes()))
	if err != nil || image.Bounds().Dx() != 256 || image.Bounds().Dy() != 256 {
		return nil, ErrInvalid
	}
	return output.Bytes(), nil
}

type limitedBuffer struct{ buffer bytes.Buffer }

func (b *limitedBuffer) Bytes() []byte { return b.buffer.Bytes() }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > MaxImage {
		return 0, ErrInvalid
	}
	return b.buffer.Write(p)
}
