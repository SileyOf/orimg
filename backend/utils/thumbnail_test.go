package utils

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestThumbnailsSizesAndOrder(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2000, 1000))
	thumbs, err := Thumbnails(encodePNG(t, src), []int{320, 720, 1440})
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]int{{320, 160}, {720, 360}, {1440, 720}}
	for i, thumb := range thumbs {
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(thumb))
		if err != nil || cfg.Width != want[i][0] || cfg.Height != want[i][1] {
			t.Fatalf("thumb %d: %v %dx%d", i, err, cfg.Width, cfg.Height)
		}
	}
}

func TestThumbnailsFlattenTransparencyOnWhite(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 400, 400)) // fully transparent
	thumbs, err := Thumbnails(encodePNG(t, src), []int{100})
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(thumbs[0]))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(50, 50).RGBA()
	if r>>8 < 250 || g>>8 < 250 || b>>8 < 250 {
		t.Fatalf("transparent pixels not flattened on white: %d %d %d", r>>8, g>>8, b>>8)
	}
}

func TestThumbnailsAverageFineDetail(t *testing.T) {
	// A one-pixel checkerboard must average to grey instead of aliasing to
	// pure black or white as nearest-neighbour sampling would.
	src := image.NewGray(image.Rect(0, 0, 800, 800))
	for y := 0; y < 800; y++ {
		for x := 0; x < 800; x++ {
			if (x+y)%2 == 0 {
				src.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
	thumbs, err := Thumbnails(encodePNG(t, src), []int{100})
	if err != nil {
		t.Fatal(err)
	}
	img, _ := jpeg.Decode(bytes.NewReader(thumbs[0]))
	r, _, _, _ := img.At(50, 50).RGBA()
	if v := r >> 8; v < 110 || v > 145 {
		t.Fatalf("expected mid grey, got %d", v)
	}
}

// jpegWithOrientation builds a JPEG carrying an APP1 EXIF orientation tag.
func jpegWithOrientation(t *testing.T, w, h, orientation int) []byte {
	t.Helper()
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w/4; x++ {
			src.Set(x, y, color.RGBA{R: 255, A: 255}) // red stripe on the left
		}
	}
	var plain bytes.Buffer
	if err := jpeg.Encode(&plain, src, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	tiff := []byte("MM\x00\x2a\x00\x00\x00\x08\x00\x01")
	entry := make([]byte, 12)
	binary.BigEndian.PutUint16(entry[0:], 0x0112)
	binary.BigEndian.PutUint16(entry[2:], 3)
	binary.BigEndian.PutUint32(entry[4:], 1)
	binary.BigEndian.PutUint16(entry[8:], uint16(orientation))
	tiff = append(append(tiff, entry...), 0, 0, 0, 0)
	payload := append([]byte("Exif\x00\x00"), tiff...)
	app1 := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(app1[2:], uint16(len(payload)+2))
	app1 = append(app1, payload...)
	body := plain.Bytes()
	return append(append(append([]byte{}, body[:2]...), app1...), body[2:]...)
}

func TestThumbnailsApplyExifOrientation(t *testing.T) {
	body := jpegWithOrientation(t, 400, 200, 6)
	if got := jpegOrientation(body); got != 6 {
		t.Fatalf("orientation parsed as %d", got)
	}
	thumbs, err := Thumbnails(body, []int{100})
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(thumbs[0]))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 50 || b.Dy() != 100 {
		t.Fatalf("rotated thumbnail has size %dx%d", b.Dx(), b.Dy())
	}
	// Orientation 6 rotates 90° clockwise: the left stripe ends up on top.
	if r, g, _, _ := img.At(25, 5).RGBA(); r>>8 < 200 || g>>8 > 80 {
		t.Fatalf("expected red at top after rotation, got r=%d g=%d", r>>8, g>>8)
	}
	if r, g, _, _ := img.At(25, 95).RGBA(); r>>8 > 80 && g>>8 < 80 {
		t.Fatal("red stripe found at bottom; rotation direction wrong")
	}
}

func TestJPEGOrientationIgnoresMalformedData(t *testing.T) {
	for _, body := range [][]byte{nil, []byte("not a jpeg"), {0xff, 0xd8, 0xff, 0xe1, 0xff, 0xff}} {
		if jpegOrientation(body) != 1 {
			t.Fatalf("malformed input %v not ignored", body)
		}
	}
}

func BenchmarkThumbnails(b *testing.B) {
	src := image.NewRGBA(image.Rect(0, 0, 4000, 3000))
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, src, nil)
	body := buf.Bytes()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Thumbnails(body, []int{320, 720, 1440}); err != nil {
			b.Fatal(err)
		}
	}
}
