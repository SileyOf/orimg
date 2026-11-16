package handlers

import (
	"bytes"
	"fmt"
	"image"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"zhuapic/utils"
)

func sanitizeBackgroundURL(value string) string {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.User != nil || u.Fragment != "" || strings.ContainsAny(value, "\r\n\\") {
		return ""
	}
	if u.Scheme == "https" && u.Hostname() != "" {
		return u.String()
	}
	if u.Scheme == "" && u.Host == "" && strings.HasPrefix(u.Path, "/assets/") && !strings.Contains(u.Path, "..") {
		return u.String()
	}
	return ""
}

func boundedBackgroundNumber(value string, low, high, fallback int) string {
	n, err := strconv.Atoi(value)
	if err != nil {
		n = fallback
	}
	return strconv.Itoa(max(low, min(high, n)))
}

func backgroundExtension(body []byte) (string, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
		return "", fmt.Errorf("invalid background dimensions")
	}
	switch format {
	case "jpeg":
		return ".jpg", nil
	case "png":
		return ".png", nil
	default:
		return "", fmt.Errorf("background must be PNG or JPEG")
	}
}

func (a *App) UploadBackground(w http.ResponseWriter, r *http.Request) {
	const limit = 8 * 1024 * 1024
	r.Body = http.MaxBytesReader(w, r.Body, limit+65536)
	if err := r.ParseMultipartForm(limit + 65536); err != nil {
		utils.WriteError(w, 400, "background exceeds 8MB or invalid upload")
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("background")
	if err != nil {
		utils.WriteError(w, 400, "missing background")
		return
	}
	_ = file.Close()
	body, err := utils.ReadMultipart(header, limit)
	if err != nil {
		utils.WriteError(w, 400, "background exceeds 8MB")
		return
	}
	ext, err := backgroundExtension(body)
	if err != nil {
		utils.WriteError(w, 400, err.Error())
		return
	}
	id, err := utils.RandomHex(12)
	if err != nil {
		utils.WriteError(w, 500, "failed to create background name")
		return
	}
	name := "site-background-" + id + ext
	if err := utils.SaveSiteAsset(a.Cfg.AssetDir, name, body); err != nil {
		utils.WriteError(w, 500, "failed to save background")
		return
	}
	// Uploading stages an asset; the existing settings form commits the selected URL.
	utils.WriteJSON(w, 200, map[string]string{"url": "/assets/" + name})
}
