package imagefile

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestNormalizeDownscalesAndReencodes(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 0, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	res, err := Normalize(&buf, Options{MaxPixels: 30000, JPEGQuality: 80})
	if err != nil {
		t.Fatal(err)
	}
	if res.Width*res.Height > 30000 || res.Width != 200 {
		t.Fatalf("size %dx%d", res.Width, res.Height)
	}
	if _, format, err := image.Decode(bytes.NewReader(res.JPEG)); err != nil || format != "jpeg" {
		t.Fatalf("output format %q err %v", format, err)
	}
}

func TestOrientRotates90(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	out := orient(img, 6)
	if out.Bounds().Dx() != 2 || out.Bounds().Dy() != 4 {
		t.Fatalf("bounds %v", out.Bounds())
	}
	if r, _, _, _ := out.At(1, 0).RGBA(); r == 0 {
		t.Fatal("top-left pixel should move to top-right after 90° CW")
	}
}
