package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// jpegWithOrientation encodes a 2×1 JPEG carrying an EXIF APP1 segment whose
// Orientation tag is o (0 means "emit no Orientation tag at all").
func jpegWithOrientation(t *testing.T, o int) []byte {
	t.Helper()
	var base bytes.Buffer
	if err := jpeg.Encode(&base, image.NewRGBA(image.Rect(0, 0, 2, 1)), nil); err != nil {
		t.Fatal(err)
	}
	b := base.Bytes()

	// Build a little-endian TIFF block with a single IFD0 entry.
	var tiff bytes.Buffer
	tiff.WriteString("II")
	le := binary.LittleEndian
	put16 := func(v uint16) { binary.Write(&tiff, le, v) }
	put32 := func(v uint32) { binary.Write(&tiff, le, v) }
	put16(0x002A)
	put32(8) // IFD0 immediately follows the 8-byte header
	count := uint16(0)
	if o != 0 {
		count = 1
	}
	put16(count)
	if o != 0 {
		put16(0x0112)    // Orientation
		put16(3)         // SHORT
		put32(1)         // count
		put16(uint16(o)) // inline value
		put16(0)         // pad to 4 bytes
	}
	put32(0) // next-IFD offset

	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	app1 := make([]byte, 0, 4+len(payload))
	app1 = append(app1, 0xFF, 0xE1)
	app1 = binary.BigEndian.AppendUint16(app1, uint16(len(payload)+2))
	app1 = append(app1, payload...)

	// Splice the APP1 segment in right after the SOI marker.
	out := make([]byte, 0, len(b)+len(app1))
	out = append(out, b[:2]...)
	out = append(out, app1...)
	out = append(out, b[2:]...)
	return out
}

func TestExifOrientation(t *testing.T) {
	for o := 1; o <= 8; o++ {
		if got := exifOrientation(jpegWithOrientation(t, o)); got != o {
			t.Errorf("orientation %d: got %d", o, got)
		}
	}
	if got := exifOrientation(jpegWithOrientation(t, 0)); got != 1 {
		t.Errorf("no tag: got %d, want 1 (normal)", got)
	}
	if got := exifOrientation([]byte("not a jpeg")); got != 1 {
		t.Errorf("non-jpeg: got %d, want 1 (normal)", got)
	}
}

func TestApplyOrientation(t *testing.T) {
	// A 2×1 image with a red left pixel; check where red lands and that
	// axis-swapping orientations transpose the dimensions.
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	src.Set(0, 0, color.RGBA{255, 0, 0, 255})
	src.Set(1, 0, color.RGBA{0, 0, 255, 255})

	for _, tc := range []struct {
		o          int
		w, h       int
		redX, redY int
	}{
		{1, 2, 1, 0, 0}, // unchanged
		{2, 2, 1, 1, 0}, // flip horizontal: red moves right
		{6, 1, 2, 0, 0}, // rotate 90° CW: 2×1 -> 1×2
		{8, 1, 2, 0, 1}, // rotate 90° CCW
	} {
		dst := applyOrientation(src, tc.o)
		b := dst.Bounds()
		if b.Dx() != tc.w || b.Dy() != tc.h {
			t.Errorf("o=%d: dims %dx%d, want %dx%d", tc.o, b.Dx(), b.Dy(), tc.w, tc.h)
		}
		r, _, _, _ := dst.At(tc.redX, tc.redY).RGBA()
		if r>>8 != 255 {
			t.Errorf("o=%d: expected red at (%d,%d)", tc.o, tc.redX, tc.redY)
		}
	}
}
