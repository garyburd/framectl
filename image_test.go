package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// writeImage encodes a w×h image to a temp file using enc and returns its path.
func writeImage(t *testing.T, name string, w, h int, enc func(*os.File, image.Image) error) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := enc(f, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return path
}

func decodeDims(t *testing.T, b []byte) (int, int, string) {
	t.Helper()
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return cfg.Width, cfg.Height, format
}

// prepareImage always renders onto the full 3840x2160 Frame canvas (fit +
// blurred-fill), whatever the source aspect ratio.
func TestPrepareImageFullCanvas(t *testing.T) {
	cases := []struct {
		name string
		w, h int
		enc  func(*os.File, image.Image) error
	}{
		{"landscape.png", 6000, 3000, func(f *os.File, m image.Image) error { return png.Encode(f, m) }},
		{"portrait.jpg", 1080, 1920, func(f *os.File, m image.Image) error { return jpeg.Encode(f, m, nil) }},
		{"small.jpg", 800, 600, func(f *os.File, m image.Image) error { return jpeg.Encode(f, m, nil) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := prepareImage(writeImage(t, c.name, c.w, c.h, c.enc))
			if err != nil {
				t.Fatal(err)
			}
			w, h, format := decodeDims(t, out)
			if w != maxWidth || h != maxHeight {
				t.Errorf("got %dx%d, want %dx%d (full canvas)", w, h, maxWidth, maxHeight)
			}
			if format != "jpeg" {
				t.Errorf("format = %q, want jpeg", format)
			}
		})
	}
}

// writeBlueJPEG encodes a solid-blue w×h JPEG and returns its path.
func writeBlueJPEG(t *testing.T, w, h int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "blue.jpg")
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(m, m.Bounds(), &image.Uniform{color.RGBA{0, 0, 200, 255}}, image.Point{}, draw.Src)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(f, m, nil); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return path
}

// barPixel decodes a prepared image and asserts the pixel is blue-ish: filled
// from the (blue, darkened) background, not left black.
func barPixel(t *testing.T, out []byte, x, y int) {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(x, y).RGBA()
	if b>>8 < 60 || b <= r || b <= g {
		t.Errorf("pixel (%d,%d) not blue-ish (bar not filled from image): r=%d g=%d b=%d",
			x, y, r>>8, g>>8, b>>8)
	}
}

// A portrait photo gets pillarbox bars; they must show the blurred, darkened
// image (here, blue), not black.
func TestPrepareImageFillsBars(t *testing.T) {
	out, err := prepareImage(writeBlueJPEG(t, 1000, 2000))
	if err != nil {
		t.Fatal(err)
	}
	// Top-left corner is in a pillarbox bar for a portrait centered in 16:9.
	barPixel(t, out, 10, 10)
}

// Rounding can fit a photo one pixel short of the canvas with no left bar at
// all (3839x2160 fits at scale 1); the lone right-hand 1px bar must still be
// filled, not left black.
func TestPrepareImageOneSidedBar(t *testing.T) {
	out, err := prepareImage(writeBlueJPEG(t, maxWidth-1, maxHeight))
	if err != nil {
		t.Fatal(err)
	}
	barPixel(t, out, maxWidth-1, maxHeight/2)
}
