package profileimage

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"testing"
)

func TestNormalizesRasterPicturesAndCenterCrops(t *testing.T) {
	wide := image.NewRGBA(image.Rect(0, 0, 600, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 600; x++ {
			c := color.RGBA{R: 255, A: 255}
			if x >= 200 && x < 400 {
				c = color.RGBA{G: 255, A: 255}
			}
			wide.Set(x, y, c)
		}
	}
	for _, format := range []string{"jpeg", "png", "gif", "webp"} {
		t.Run(format, func(t *testing.T) {
			var upload bytes.Buffer
			switch format {
			case "jpeg":
				jpeg.Encode(&upload, wide, nil)
			case "png":
				png.Encode(&upload, wide)
			case "gif":
				gif.Encode(&upload, wide, nil)
			case "webp":
				data, err := os.ReadFile("testdata/profile.webp")
				if err != nil {
					t.Fatal(err)
				}
				upload.Write(data)
			}
			data, err := Normalize(context.Background(), upload.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			got, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if got.Bounds() != image.Rect(0, 0, 256, 256) {
				t.Fatal(got.Bounds())
			}
			r, g, _, _ := got.At(128, 128).RGBA()
			if g < 60000 || r > 5000 {
				t.Fatal("center of picture was not retained")
			}
		})
	}
}

func TestRejectsMalformedAndOversizedPictures(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("<svg/>"), []byte("\x89PNG\r\n\x1a\ninvalid"), make([]byte, MaxUpload+1)} {
		if _, err := Normalize(context.Background(), data); err != ErrInvalid {
			t.Fatalf("unexpected validation: %v", err)
		}
	}
	// A valid, highly compressed raster exceeding the decoded pixel budget.
	var huge bytes.Buffer
	if err := png.Encode(&huge, image.NewGray(image.Rect(0, 0, 6400, 6400))); err != nil {
		t.Fatal(err)
	}
	if _, err := Normalize(context.Background(), huge.Bytes()); err != ErrInvalid {
		t.Fatalf("pixel limit: %v", err)
	}
}
