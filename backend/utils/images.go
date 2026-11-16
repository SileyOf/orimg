package utils

import (
	"bytes"
	"encoding/xml"
	"errors"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
)

var mimeToExt = map[string]string{
	"image/jpeg":                 ".jpg",
	"image/png":                  ".png",
	"image/gif":                  ".gif",
	"image/webp":                 ".webp",
	"image/avif":                 ".avif",
	"image/bmp":                  ".bmp",
	"image/tiff":                 ".tiff",
	"image/x-icon":               ".ico",
	"image/vnd.microsoft.icon":   ".ico",
	"image/svg+xml":              ".svg",
	"image/heic":                 ".heic",
	"image/heif":                 ".heif",
	"image/jxl":                  ".jxl",
	"image/vnd.adobe.photoshop":  ".psd",
	"application/octet-stream":   "",
	"application/xml":            ".svg",
	"text/xml; charset=utf-8":    ".svg",
	"text/plain; charset=utf-8":  ".svg",
	"text/plain; charset=utf-16": ".svg",
}

var extToMime = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".avif": "image/avif",
	".bmp":  "image/bmp",
	".tif":  "image/tiff",
	".tiff": "image/tiff",
	".ico":  "image/x-icon",
	".svg":  "image/svg+xml",
	".heic": "image/heic",
	".heif": "image/heif",
	".jxl":  "image/jxl",
	".psd":  "image/vnd.adobe.photoshop",
}

func DetectImage(header *multipart.FileHeader) (string, int, int, error) {
	file, err := header.Open()
	if err != nil {
		return "", 0, 0, err
	}
	defer file.Close()
	return DetectImageStream(file, header.Filename)
}

func DetectImageStream(file io.ReadSeeker, filename string) (string, int, int, error) {
	sniff := make([]byte, 512)
	n, err := io.ReadFull(file, sniff)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", 0, 0, err
	}
	sniff = sniff[:n]
	mimeType := sniffImageMime(sniff, http.DetectContentType(sniff), filename)
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", 0, 0, err
	}
	cfg, _, err := image.DecodeConfig(file)
	if err != nil {
		return mimeType, 0, 0, nil
	}
	return mimeType, cfg.Width, cfg.Height, nil
}

func ReadMultipart(header *multipart.FileHeader, maxBytes int64) ([]byte, error) {
	file, err := header.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, maxBytes+1))
}

func DetectImageBytes(body []byte, filename string) (string, int, int, error) {
	sniff := body
	if len(sniff) > 512 {
		sniff = sniff[:512]
	}
	mimeType := sniffImageMime(sniff, http.DetectContentType(sniff), filename)

	cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err == nil {
		return mimeType, cfg.Width, cfg.Height, nil
	}
	return mimeType, 0, 0, nil
}

func sniffImageMime(buf []byte, detected string, filename string) string {
	if len(buf) >= 2 && string(buf[:2]) == "BM" {
		return "image/bmp"
	}
	if len(buf) >= 4 && (bytes.Equal(buf[:4], []byte{'I', 'I', '*', 0}) || bytes.Equal(buf[:4], []byte{'M', 'M', 0, '*'})) {
		return "image/tiff"
	}
	if len(buf) >= 4 && bytes.Equal(buf[:4], []byte{0, 0, 1, 0}) {
		return "image/x-icon"
	}
	if len(buf) >= 4 && string(buf[:4]) == "8BPS" {
		return "image/vnd.adobe.photoshop"
	}
	if len(buf) >= 2 && bytes.Equal(buf[:2], []byte{0xff, 0x0a}) {
		return "image/jxl"
	}
	if len(buf) >= 12 && string(buf[0:4]) == "RIFF" && string(buf[8:12]) == "WEBP" {
		return "image/webp"
	}
	if len(buf) >= 12 && string(buf[4:8]) == "ftyp" {
		brand := string(buf[8:12])
		if brand == "avif" || brand == "avis" {
			return "image/avif"
		}
		switch brand {
		case "heic", "heix", "hevc", "hevx":
			return "image/heic"
		case "mif1", "msf1":
			return "image/heif"
		case "jxl ":
			return "image/jxl"
		}
	}
	trimmed := bytes.TrimSpace(buf)
	if bytes.Contains(bytes.ToLower(trimmed), []byte("<svg")) {
		return "image/svg+xml"
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if mimeType, ok := extToMime[ext]; ok && (detected == "application/octet-stream" || strings.HasPrefix(detected, "text/") || strings.Contains(detected, "xml")) {
		return mimeType
	}
	return detected
}

func NormalizeImageExt(originalName string, mimeType string) string {
	if ext, ok := mimeToExt[mimeType]; ok {
		return ext
	}
	ext := strings.ToLower(filepath.Ext(originalName))
	if ext == ".jpeg" {
		return ".jpg"
	}
	return ext
}

func AllowedMime(mimeType string, allowedTypes string) bool {
	allowedExts := map[string]bool{}
	for _, item := range strings.Split(allowedTypes, ",") {
		item = strings.TrimSpace(strings.ToLower(item))
		if item == "" {
			continue
		}
		if !strings.HasPrefix(item, ".") {
			item = "." + item
		}
		allowedExts[item] = true
		if item == ".jpeg" {
			allowedExts[".jpg"] = true
		}
		if item == ".jpg" {
			allowedExts[".jpeg"] = true
		}
		if item == ".tif" {
			allowedExts[".tiff"] = true
		}
		if item == ".tiff" {
			allowedExts[".tif"] = true
		}
	}
	ext := mimeToExt[mimeType]
	if ext == "" {
		return false
	}
	return allowedExts[ext]
}

func SanitizeSVG(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return nil, errors.New("empty svg")
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = true
	var out bytes.Buffer
	encoder := xml.NewEncoder(&out)
	foundRoot := false
	blockedElements := map[string]bool{
		"script": true, "foreignobject": true, "iframe": true, "object": true,
		"embed": true, "audio": true, "video": true, "set": true, "animate": true,
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("invalid svg xml")
		}
		switch current := token.(type) {
		case xml.StartElement:
			name := strings.ToLower(current.Name.Local)
			if !foundRoot {
				if name != "svg" {
					return nil, errors.New("invalid svg root")
				}
				foundRoot = true
			}
			if blockedElements[name] {
				return nil, errors.New("unsafe svg element blocked")
			}
			for _, attr := range current.Attr {
				attrName := strings.ToLower(attr.Name.Local)
				value := strings.TrimSpace(attr.Value)
				lowerValue := strings.ToLower(value)
				if strings.HasPrefix(attrName, "on") {
					return nil, errors.New("unsafe svg event handler blocked")
				}
				if attrName == "href" && value != "" && !strings.HasPrefix(value, "#") && !safeSVGDataImage(lowerValue) {
					return nil, errors.New("external svg resource blocked")
				}
				if attrName == "style" && (strings.Contains(lowerValue, "url(") || strings.Contains(lowerValue, "@import") || strings.Contains(lowerValue, "javascript:")) {
					return nil, errors.New("unsafe svg style blocked")
				}
			}
		case xml.ProcInst:
			if strings.ToLower(current.Target) != "xml" {
				return nil, errors.New("unsafe svg processing instruction blocked")
			}
		case xml.Directive:
			return nil, errors.New("unsafe svg directive blocked")
		}
		if err := encoder.EncodeToken(token); err != nil {
			return nil, errors.New("failed to sanitize svg")
		}
	}
	if !foundRoot {
		return nil, errors.New("invalid svg")
	}
	if err := encoder.Flush(); err != nil {
		return nil, errors.New("failed to sanitize svg")
	}
	return bytes.TrimSpace(out.Bytes()), nil
}

func safeSVGDataImage(value string) bool {
	return strings.HasPrefix(value, "data:image/png;base64,") ||
		strings.HasPrefix(value, "data:image/jpeg;base64,") ||
		strings.HasPrefix(value, "data:image/gif;base64,") ||
		strings.HasPrefix(value, "data:image/webp;base64,")
}

func IsAnimatedImage(body []byte, mimeType string) bool {
	switch mimeType {
	case "image/gif":
		return gifMultipleFrames(body)
	case "image/webp":
		return bytes.Contains(body, []byte("ANIM"))
	case "image/png":
		return bytes.Contains(body, []byte("acTL"))
	default:
		return false
	}
}

func IsAnimatedMultipart(header *multipart.FileHeader, mimeType string) bool {
	if mimeType != "image/gif" && mimeType != "image/webp" && mimeType != "image/png" {
		return false
	}
	const animationInspectLimit = 32 * 1024 * 1024
	if header.Size > animationInspectLimit {
		return true
	}
	body, err := ReadMultipart(header, animationInspectLimit)
	return err == nil && IsAnimatedImage(body, mimeType)
}

func ThumbnailJPEG(body []byte, maxSide int) ([]byte, int, int, error) {
	out, err := Thumbnails(body, []int{maxSide})
	if err != nil {
		return nil, 0, 0, err
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(out[0]))
	if err != nil {
		return nil, 0, 0, err
	}
	return out[0], cfg.Width, cfg.Height, nil
}

var thumbnailDecodeSlot = make(chan struct{}, 1)

// GIF sub-blocks are length-prefixed. Counting image descriptors avoids decoding
// (and retaining) every frame just to decide whether the image is animated.
func gifMultipleFrames(body []byte) bool {
	if len(body) < 13 || (string(body[:6]) != "GIF89a" && string(body[:6]) != "GIF87a") {
		return true
	}
	pos := 13
	if body[10]&0x80 != 0 {
		pos += 3 * (1 << ((body[10] & 7) + 1))
	}
	skipBlocks := func() bool {
		for pos < len(body) {
			n := int(body[pos])
			pos++
			if n == 0 {
				return true
			}
			if n > len(body)-pos {
				return false
			}
			pos += n
		}
		return false
	}
	frames := 0
	for pos < len(body) {
		kind := body[pos]
		pos++
		switch kind {
		case 0x3b:
			return frames > 1
		case 0x21:
			if pos >= len(body) {
				return true
			}
			pos++
			if !skipBlocks() {
				return true
			}
		case 0x2c:
			if len(body)-pos < 9 {
				return true
			}
			packed := body[pos+8]
			pos += 9
			if packed&0x80 != 0 {
				pos += 3 * (1 << ((packed & 7) + 1))
			}
			if pos >= len(body) {
				return true
			}
			pos++
			if !skipBlocks() {
				return true
			}
			frames++
			if frames > 1 {
				return true
			}
		default:
			return true
		}
	}
	return true
}

func fitSize(width, height, maxSide int) (int, int) {
	if width <= maxSide && height <= maxSide {
		return width, height
	}
	if width >= height {
		dstW := maxSide
		dstH := int(float64(height) * float64(maxSide) / float64(width))
		if dstH < 1 {
			dstH = 1
		}
		return dstW, dstH
	}
	dstH := maxSide
	dstW := int(float64(width) * float64(maxSide) / float64(height))
	if dstW < 1 {
		dstW = 1
	}
	return dstW, dstH
}
