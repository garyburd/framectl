package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // register the PNG decoder for source files
	"io"
	"math"
	"os"

	"golang.org/x/image/draw"
)

// The Frame's screen is 3840x2160; images are scaled to fit within it.
const (
	maxWidth    = 3840
	maxHeight   = 2160
	jpegQuality = 90
)

// prepareImage reads and prepares an image for the Frame.
func prepareImage(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return prepareImageReader(f)
}

// prepareImageReader applies EXIF orientation and fits the image without
// cropping on a 3840x2160 JPEG canvas. Bars show a heavily blurred, darkened
// fill-crop of the image itself.
func prepareImageReader(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	src = applyOrientation(src, exifOrientation(raw))
	b := src.Bounds()
	canvas := image.NewRGBA(image.Rect(0, 0, maxWidth, maxHeight))

	scale := math.Min(float64(maxWidth)/float64(b.Dx()), float64(maxHeight)/float64(b.Dy()))
	fw := int(math.Round(float64(b.Dx()) * scale))
	fh := int(math.Round(float64(b.Dy()) * scale))
	ox, oy := (maxWidth-fw)/2, (maxHeight-fh)/2
	photo := image.Rect(ox, oy, ox+fw, oy+fh)

	// A 16:9 photo covers the canvas and needs no background.
	if photo != canvas.Bounds() {
		fillBackground(canvas, src)
	}
	draw.CatmullRom.Scale(canvas, photo, src, b, draw.Src, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	return buf.Bytes(), nil
}

// fillBackground covers the canvas with a heavily blurred, darkened
// center-crop of src scaled to fill it, so bars continue the photo with no
// seam or recognizable structure.
func fillBackground(canvas *image.RGBA, src image.Image) {
	bnd := canvas.Bounds()
	b := src.Bounds()

	// Downscale a fill-crop of src into a tiny image and enlarge it back to
	// the canvas; the round trip is a cheap, very heavy blur.
	const shrink = 60
	small := image.NewRGBA(image.Rect(0, 0, max(1, bnd.Dx()/shrink), max(1, bnd.Dy()/shrink)))
	sb := small.Bounds()
	scale := math.Max(float64(sb.Dx())/float64(b.Dx()), float64(sb.Dy())/float64(b.Dy()))
	fw := int(math.Round(float64(b.Dx()) * scale))
	fh := int(math.Round(float64(b.Dy()) * scale))
	// The fill rect overflows small on one axis; Scale clips to its bounds.
	ox, oy := (sb.Dx()-fw)/2, (sb.Dy()-fh)/2
	draw.BiLinear.Scale(small, image.Rect(ox, oy, ox+fw, oy+fh), src, b, draw.Src, nil)

	// Darken so the photo stands out against its background.
	const dark = 0.55
	for i := 0; i < len(small.Pix); i += 4 {
		small.Pix[i+0] = u8(float64(small.Pix[i+0]) * dark)
		small.Pix[i+1] = u8(float64(small.Pix[i+1]) * dark)
		small.Pix[i+2] = u8(float64(small.Pix[i+2]) * dark)
	}

	draw.CatmullRom.Scale(canvas, bnd, small, sb, draw.Src, nil)

	// Interpolating 60x blocks yields gradients that band visibly after JPEG
	// encoding; dither with subtle noise.
	for y := bnd.Min.Y; y < bnd.Max.Y; y++ {
		for x := bnd.Min.X; x < bnd.Max.X; x++ {
			canvas.SetRGBA(x, y, noisy(canvas.RGBAAt(x, y), x, y))
		}
	}
}

// noisy adds deterministic ±4-channel paper-like texture.
func noisy(c color.RGBA, x, y int) color.RGBA {
	h := uint32(x)*73856093 ^ uint32(y)*19349663
	h *= 2654435761
	n := float64(int(h>>24)%9 - 4)
	return color.RGBA{u8(float64(c.R) + n), u8(float64(c.G) + n), u8(float64(c.B) + n), 255}
}

func u8(v float64) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	}
	return uint8(v + 0.5)
}
