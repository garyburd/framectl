package main

import (
	"encoding/binary"
	"image"
)

// exifOrientation reads JPEG IFD0 tag 0x0112. Missing or malformed data returns
// normal orientation; PNG is not handled.
func exifOrientation(data []byte) int {
	const normal = 1
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 { // not a JPEG (SOI)
		return normal
	}
	p := 2
	for p+4 <= len(data) {
		if data[p] != 0xFF {
			return normal
		}
		marker := data[p+1]
		// Standalone markers carry no length payload.
		if marker == 0xD8 || marker == 0xD9 || (marker >= 0xD0 && marker <= 0xD7) {
			p += 2
			continue
		}
		segLen := int(binary.BigEndian.Uint16(data[p+2 : p+4]))
		if segLen < 2 || p+2+segLen > len(data) {
			return normal
		}
		if marker == 0xDA { // start of scan: pixel data follows, no more headers
			return normal
		}
		if marker == 0xE1 { // APP1 — may hold the Exif TIFF block
			if o, ok := orientationFromAPP1(data[p+4 : p+2+segLen]); ok {
				return o
			}
		}
		p += 2 + segLen
	}
	return normal
}

// orientationFromAPP1 reads a valid orientation from an Exif TIFF block.
func orientationFromAPP1(seg []byte) (int, bool) {
	const exifHdr = "Exif\x00\x00"
	if len(seg) < len(exifHdr) || string(seg[:len(exifHdr)]) != exifHdr {
		return 0, false
	}
	tiff := seg[len(exifHdr):]
	if len(tiff) < 8 {
		return 0, false
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0, false
	}
	if bo.Uint16(tiff[2:4]) != 0x002A {
		return 0, false
	}
	ifd := int(bo.Uint32(tiff[4:8]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 0, false
	}
	n := int(bo.Uint16(tiff[ifd : ifd+2]))
	for entry := ifd + 2; n > 0; n, entry = n-1, entry+12 {
		if entry+12 > len(tiff) {
			return 0, false
		}
		if bo.Uint16(tiff[entry:entry+2]) != 0x0112 { // Orientation
			continue
		}
		if bo.Uint16(tiff[entry+2:entry+4]) != 3 { // SHORT, inline in the value field
			return 0, false
		}
		if bo.Uint32(tiff[entry+4:entry+8]) != 1 { // exactly one value
			return 0, false
		}
		if o := int(bo.Uint16(tiff[entry+8 : entry+10])); o >= 1 && o <= 8 {
			return o, true
		}
		return 0, false
	}
	return 0, false
}

// applyOrientation applies EXIF values 2–8; values 5–8 transpose dimensions.
func applyOrientation(src image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if o >= 5 {
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
	}
	for sy := 0; sy < h; sy++ {
		for sx := 0; sx < w; sx++ {
			var dx, dy int
			switch o {
			case 2: // flip horizontal
				dx, dy = w-1-sx, sy
			case 3: // rotate 180
				dx, dy = w-1-sx, h-1-sy
			case 4: // flip vertical
				dx, dy = sx, h-1-sy
			case 5: // transpose
				dx, dy = sy, sx
			case 6: // rotate 90° CW
				dx, dy = h-1-sy, sx
			case 7: // transverse
				dx, dy = h-1-sy, w-1-sx
			case 8: // rotate 90° CCW
				dx, dy = sy, w-1-sx
			}
			dst.Set(dx, dy, src.At(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return dst
}
