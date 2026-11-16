package utils

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	"sort"
)

const thumbnailPixelBudget = 16_000_000

// Thumbnails decodes the image once and returns one JPEG per requested longest
// side, in the order given. Output is flattened on white, area-averaged (so
// fine detail does not alias) and rotated according to JPEG EXIF orientation,
// matching how browsers display the original.
func Thumbnails(body []byte, maxSides []int) ([][]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > thumbnailPixelBudget {
		return nil, errors.New("thumbnail exceeds decode budget")
	}
	thumbnailDecodeSlot <- struct{}{}
	defer func() { <-thumbnailDecodeSlot }()
	src, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	bounds := src.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return nil, errors.New("invalid image dimensions")
	}
	flat := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(flat, flat.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), src, bounds.Min, draw.Over)
	src = nil
	orientation := jpegOrientation(body)

	// Scale largest first and derive smaller sizes from the previous result:
	// each step reads fewer pixels, and box filtering composes well.
	order := make([]int, len(maxSides))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return maxSides[order[a]] > maxSides[order[b]] })
	out := make([][]byte, len(maxSides))
	current := flat
	for _, index := range order {
		side := maxSides[index]
		if side <= 0 {
			side = 320
		}
		w, h := fitSize(current.Rect.Dx(), current.Rect.Dy(), side)
		if w != current.Rect.Dx() || h != current.Rect.Dy() {
			current = boxResize(current, w, h)
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, orient(current, orientation), &jpeg.Options{Quality: 84}); err != nil {
			return nil, err
		}
		out[index] = buf.Bytes()
	}
	return out, nil
}

// boxResize downsamples by averaging every source pixel that falls inside each
// destination pixel. Callers only use it to shrink.
func boxResize(src *image.RGBA, dstW, dstH int) *image.RGBA {
	srcW, srcH := src.Rect.Dx(), src.Rect.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	sums := make([]uint64, dstW*3)
	counts := make([]uint64, dstW)
	xStart := make([]int, dstW+1)
	for x := 0; x <= dstW; x++ {
		xStart[x] = x * srcW / dstW
	}
	for y := 0; y < dstH; y++ {
		y0, y1 := y*srcH/dstH, (y+1)*srcH/dstH
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for i := range sums {
			sums[i] = 0
		}
		for i := range counts {
			counts[i] = 0
		}
		for sy := y0; sy < y1 && sy < srcH; sy++ {
			row := src.Pix[sy*src.Stride:]
			for x := 0; x < dstW; x++ {
				x0, x1 := xStart[x], xStart[x+1]
				if x1 <= x0 {
					x1 = x0 + 1
				}
				var r, g, b uint64
				for sx := x0; sx < x1 && sx < srcW; sx++ {
					p := row[sx*4 : sx*4+3 : sx*4+3]
					r += uint64(p[0])
					g += uint64(p[1])
					b += uint64(p[2])
				}
				sums[x*3] += r
				sums[x*3+1] += g
				sums[x*3+2] += b
				counts[x] += uint64(min(x1, srcW) - x0)
			}
		}
		row := dst.Pix[y*dst.Stride:]
		for x := 0; x < dstW; x++ {
			n := counts[x]
			if n == 0 {
				n = 1
			}
			row[x*4] = uint8((sums[x*3] + n/2) / n)
			row[x*4+1] = uint8((sums[x*3+1] + n/2) / n)
			row[x*4+2] = uint8((sums[x*3+2] + n/2) / n)
			row[x*4+3] = 0xff
		}
	}
	return dst
}

// orient applies an EXIF orientation (1-8) to an opaque RGBA image.
func orient(src *image.RGBA, orientation int) *image.RGBA {
	if orientation < 2 || orientation > 8 {
		return src
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dw, dh := w, h
	if orientation >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch orientation {
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
			copy(dst.Pix[dy*dst.Stride+dx*4:dy*dst.Stride+dx*4+4], src.Pix[y*src.Stride+x*4:y*src.Stride+x*4+4])
		}
	}
	return dst
}

// jpegOrientation reads the EXIF orientation tag from a JPEG, returning 1
// (no transform) for other formats or malformed metadata.
func jpegOrientation(body []byte) int {
	if len(body) < 4 || body[0] != 0xff || body[1] != 0xd8 {
		return 1
	}
	pos := 2
	for pos+4 <= len(body) {
		if body[pos] != 0xff {
			return 1
		}
		marker := body[pos+1]
		if marker == 0xd8 || marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7) {
			pos += 2
			continue
		}
		if marker == 0xda || marker == 0xd9 {
			return 1
		}
		length := int(binary.BigEndian.Uint16(body[pos+2:]))
		if length < 2 || pos+2+length > len(body) {
			return 1
		}
		segment := body[pos+4 : pos+2+length]
		if marker == 0xe1 && len(segment) > 6 && string(segment[:6]) == "Exif\x00\x00" {
			return exifOrientation(segment[6:])
		}
		pos += 2 + length
	}
	return 1
}

func exifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	ifd := int(order.Uint32(tiff[4:]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 1
	}
	entries := int(order.Uint16(tiff[ifd:]))
	for i := 0; i < entries; i++ {
		entry := ifd + 2 + i*12
		if entry+12 > len(tiff) {
			return 1
		}
		if order.Uint16(tiff[entry:]) == 0x0112 && order.Uint16(tiff[entry+2:]) == 3 {
			if value := int(order.Uint16(tiff[entry+8:])); value >= 1 && value <= 8 {
				return value
			}
			return 1
		}
	}
	return 1
}
