// Package imagefile turns an uploaded image (JPEG/PNG/TIFF) into the JPEG
// page image the OCR engine expects: it applies EXIF orientation, caps the
// pixel count and re-encodes. Only the first frame of a TIFF is read.
package imagefile

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // register decoder
	"io"
	"math"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/tiff" // register decoder
)

// Options tunes normalization.
type Options struct {
	MaxPixels   int
	JPEGQuality int
}

// Result is a normalized page image.
type Result struct {
	JPEG          []byte
	Width, Height int
}

// Normalize decodes, orients, downsizes and re-encodes an image.
func Normalize(r io.Reader, opt Options) (*Result, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("imagefile: decode: %w", err)
	}
	if format == "jpeg" {
		img = orient(img, exifOrientation(raw))
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if opt.MaxPixels > 0 && w*h > opt.MaxPixels {
		scale := math.Sqrt(float64(opt.MaxPixels) / float64(w*h))
		nw, nh := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
		img, w, h = dst, nw, nh
	} else if format == "jpeg" && exifOrientation(raw) <= 1 {
		// Already a JPEG within limits: keep the original bytes.
		return &Result{JPEG: raw, Width: w, Height: h}, nil
	}
	q := opt.JPEGQuality
	if q <= 0 {
		q = 85
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
		return nil, fmt.Errorf("imagefile: encode: %w", err)
	}
	return &Result{JPEG: buf.Bytes(), Width: w, Height: h}, nil
}

// exifOrientation returns the EXIF orientation tag (1..8) of a JPEG, or 1.
func exifOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			return 1
		}
		marker := b[i+1]
		size := int(binary.BigEndian.Uint16(b[i+2:]))
		if marker == 0xE1 && i+4+size-2 <= len(b) && size > 8 && bytes.HasPrefix(b[i+4:], []byte("Exif\x00\x00")) {
			return tiffOrientation(b[i+10 : i+2+size])
		}
		if marker == 0xDA { // start of scan: no more metadata
			return 1
		}
		i += 2 + size
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	off := int(bo.Uint32(t[4:]))
	if off+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[off:]))
	for k := 0; k < n; k++ {
		e := off + 2 + k*12
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			v := int(bo.Uint16(t[e+8:]))
			if v >= 1 && v <= 8 {
				return v
			}
		}
	}
	return 1
}

// orient applies an EXIF orientation transform.
func orient(src image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	var dst *image.RGBA
	if o >= 5 {
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
	} else {
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
