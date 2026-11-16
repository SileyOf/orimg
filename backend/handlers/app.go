package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"zhuapic/config"
	"zhuapic/models"
	"zhuapic/storage"
	"zhuapic/utils"
)

type App struct {
	DB          *sql.DB
	Cfg         *config.Config
	Store       *storage.Manager
	Coordinator *storage.Coordinator
}

type loginRequest struct {
	Email         string `json:"email"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	CaptchaToken  string `json:"captcha_token"`
	CaptchaAnswer string `json:"captcha_answer"`
}

var publicSettingKeys = []string{
	"site_name",
	"site_tagline",
	"site_logo_url",
	"default_accent_color",
	"background_url",
	"background_overlay",
	"background_blur",
	"hero_title",
	"hero_subtitle",
	"announcement",
	"footer_text",
	"upload_mode",
	"user_path",
	"public_upload_enabled",
	"storage_mode",
	"max_upload_mb",
	"cache_ttl_days",
	"disk_stop_percent",
	"allowed_types",
	"app_version",
}

var editableSettingKeys = map[string]bool{
	"site_name":              true,
	"site_tagline":           true,
	"site_logo_url":          true,
	"default_accent_color":   true,
	"background_url":         true,
	"background_overlay":     true,
	"background_blur":        true,
	"admin_brand_name":       true,
	"admin_brand_subtitle":   true,
	"admin_badge_text":       true,
	"site_domains":           true,
	"hero_title":             true,
	"hero_subtitle":          true,
	"announcement":           true,
	"footer_text":            true,
	"upload_mode":            true,
	"admin_path":             true,
	"user_path":              true,
	"public_upload_enabled":  true,
	"storage_mode":           true,
	"max_upload_mb":          true,
	"cache_ttl_days":         true,
	"disk_stop_percent":      true,
	"recycle_bin_days":       true,
	"replica_worker_enabled": true,
	"db_backup_enabled":      true,
	"db_backup_target_id":    true,
	"db_backup_hour":         true,
	"db_backup_s3_prefix":    true,
	"mysqldump_path":         true,
	"allowed_types":          true,
	"local_upload_dir":       true,
	"s3_cache_dir":           true,
	"thumb_dir":              true,
	"s3_endpoint":            true,
	"s3_region":              true,
	"s3_bucket":              true,
	"s3_access_key":          true,
	"s3_secret_key":          true,
	"s3_force_path_style":    true,
	"s3_key_prefix":          true,
	"login_captcha_enabled":  true,
	"upload_rate_per_min":    true,
	"image_rate_per_min":     true,
	"login_rate_per_min":     true,
}

func (a *App) Login(w http.ResponseWriter, r *http.Request) {
	if !loginLimiter.Allow("login:"+a.clientIP(r), int(utils.SettingInt64(a.DB, "login_rate_per_min", 20, 1, 300))) {
		utils.WriteError(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid login payload")
		return
	}
	if utils.SettingBool(a.DB, "login_captcha_enabled", true) {
		if err := a.verifyCaptcha(req.CaptchaToken, req.CaptchaAnswer); err != nil {
			utils.WriteError(w, http.StatusBadRequest, "captcha verification failed")
			return
		}
	}
	email, ok := cleanEmail(firstNonEmpty(req.Email, req.Username))
	if !ok {
		utils.WriteError(w, http.StatusBadRequest, "invalid email")
		return
	}
	var id uint
	var username string
	var storedEmail string
	var hash string
	var role string
	var active bool
	var version uint64
	err := a.DB.QueryRow(`SELECT id, username, email, password, role, active, auth_version FROM users WHERE email = ?`, email).Scan(&id, &username, &storedEmail, &hash, &role, &active, &version)
	if !utils.VerifyPasswordOrDummy(hash, err == nil, req.Password) {
		utils.WriteError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if !active {
		utils.WriteError(w, http.StatusForbidden, "user disabled")
		return
	}
	token, err := utils.GenerateToken(a.Cfg, id, username, storedEmail, role, version)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	loginUser, _ := a.findUserByID(id)
	a.writeAudit(r, loginUser, "auth.login", "user", strconv.FormatUint(uint64(id), 10), nil)
	utils.WriteJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user": map[string]any{
			"id":       id,
			"username": username,
			"email":    storedEmail,
			"role":     role,
		},
	})
}

func (a *App) PublicSettings(w http.ResponseWriter, r *http.Request) {
	all := utils.GetSettings(a.DB)
	out := map[string]any{}
	for _, key := range publicSettingKeys {
		out[key] = all[key]
	}
	out["app_version"] = config.AppVersion
	out["s3_configured"] = a.Store.S3Configured(a.storageOptions())
	out["upload_mode"] = "classic"
	out["upload_policy"] = publicPolicy(a.uploadPolicyFor(nil, false))
	utils.WriteJSON(w, http.StatusOK, out)
}

func (a *App) AdminSettings(w http.ResponseWriter, r *http.Request) {
	all := utils.GetSettings(a.DB)
	opts := a.storageOptions()
	all["s3_configured"] = strconv.FormatBool(a.Store.S3Configured(opts))
	all["base_url"] = a.Cfg.BaseURL
	all["upload_dir"] = opts.UploadDir
	all["cache_dir"] = opts.CacheDir
	all["app_version"] = config.AppVersion
	if all["s3_secret_key"] != "" {
		all["s3_secret_key_set"] = "true"
		all["s3_secret_key"] = ""
	} else {
		all["s3_secret_key_set"] = "false"
	}
	utils.WriteJSON(w, http.StatusOK, map[string]any{"settings": all})
}

func (a *App) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var payload map[string]string
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid settings payload")
		return
	}

	updated := map[string]string{}
	if raw, ok := payload["db_backup_target_id"]; ok && raw != "0" && raw != "" {
		id, err := strconv.ParseUint(raw, 10, 31)
		if err != nil || id == 0 || a.Coordinator == nil {
			utils.WriteError(w, 400, "无效备份存储")
			return
		}
		unlock, err := a.Coordinator.LockTargetConfig(r.Context())
		if err != nil {
			utils.WriteError(w, 409, "存储配置正在更新")
			return
		}
		defer unlock()
		target, err := a.Coordinator.Target(r.Context(), uint(id))
		if err != nil || !target.Enabled {
			utils.WriteError(w, 400, "备份存储不存在或已停用")
			return
		}
		if target.Mode() == "s3" && !a.Store.S3Configured(a.Coordinator.TargetOptions(target)) {
			utils.WriteError(w, 400, "备份存储配置不完整")
			return
		}
	}
	if a.Coordinator != nil {
		if err := a.Coordinator.CheckLegacyLocationChanges(r.Context(), payload); err != nil {
			utils.WriteError(w, http.StatusConflict, err.Error())
			return
		}
	}
	for key, value := range payload {
		if !editableSettingKeys[key] {
			continue
		}
		if key == "s3_secret_key" && strings.TrimSpace(value) == "" {
			continue
		}
		value = sanitizeSetting(key, value, a.Cfg)
		if key == "s3_secret_key" {
			encrypted, err := utils.EncryptSecret(value, a.Cfg.JWTSecret)
			if err != nil {
				utils.WriteError(w, http.StatusInternalServerError, "failed to protect s3 secret")
				return
			}
			value = encrypted
		}
		if err := utils.UpsertSetting(a.DB, key, value); err != nil {
			utils.WriteError(w, http.StatusInternalServerError, "failed to save settings")
			return
		}
		updated[key] = value
	}
	actor, _ := a.currentUser(r)
	a.writeAudit(r, actor, "settings.updated", "site_settings", "bulk", map[string]any{"count": len(updated)})
	utils.WriteJSON(w, http.StatusOK, map[string]any{"settings": updated})
}

func (a *App) UploadLogo(w http.ResponseWriter, r *http.Request) {
	const maxLogoBytes = 2 * 1024 * 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxLogoBytes+1024)
	if err := r.ParseMultipartForm(maxLogoBytes + 1024); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid logo upload")
		return
	}
	file, header, err := r.FormFile("logo")
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "missing logo file")
		return
	}
	_ = file.Close()
	body, err := utils.ReadMultipart(header, maxLogoBytes)
	if err != nil || int64(len(body)) > maxLogoBytes {
		utils.WriteError(w, http.StatusBadRequest, "logo is too large")
		return
	}
	mimeType, _, _, err := utils.DetectImageBytes(body, header.Filename)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "failed to read logo")
		return
	}
	switch mimeType {
	case "image/svg+xml":
		body, err = utils.SanitizeSVG(body)
		if err != nil {
			utils.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
	case "image/png", "image/jpeg", "image/webp", "image/avif", "image/x-icon", "image/vnd.microsoft.icon":
	default:
		utils.WriteError(w, http.StatusBadRequest, "unsupported logo type")
		return
	}
	ext := utils.NormalizeImageExt(header.Filename, mimeType)
	random, err := utils.RandomHex(6)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to create logo name")
		return
	}
	name := "site-logo-" + random + ext
	if err := utils.SaveSiteAsset(a.Cfg.AssetDir, name, body); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to save logo")
		return
	}
	url := "/assets/" + name
	if err := utils.UpsertSetting(a.DB, "site_logo_url", url); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to save logo setting")
		return
	}
	actor, _ := a.currentUser(r)
	a.writeAudit(r, actor, "branding.logo_updated", "site_settings", "site_logo_url", nil)
	utils.WriteJSON(w, http.StatusOK, map[string]any{"url": url})
}

func (a *App) Upload(w http.ResponseWriter, r *http.Request) {
	if !uploadLimiter.Allow("upload:"+a.clientIP(r), int(utils.SettingInt64(a.DB, "upload_rate_per_min", 60, 1, 1000))) {
		utils.WriteError(w, http.StatusTooManyRequests, "too many uploads")
		return
	}
	publicUpload := utils.SettingBool(a.DB, "public_upload_enabled", true)
	user, loggedIn := a.currentUser(r)
	apiAuthenticated := false
	if !loggedIn {
		user, loggedIn = a.apiTokenUser(r)
		apiAuthenticated = loggedIn
	}
	if r.Header.Get("Authorization") != "" && !loggedIn {
		utils.WriteError(w, http.StatusUnauthorized, "invalid authorization token")
		return
	}
	isAdmin := loggedIn && user.Role == "admin"
	if !publicUpload && !loggedIn {
		utils.WriteError(w, http.StatusUnauthorized, "upload requires login")
		return
	}

	policy := a.uploadPolicyFor(user, loggedIn)
	if apiAuthenticated && !policy.AllowAPI {
		utils.WriteError(w, http.StatusForbidden, "api uploads are disabled for this user group")
		return
	}
	if !policy.Active {
		utils.WriteError(w, http.StatusForbidden, "uploads are disabled for this user group")
		return
	}
	maxMB := clampInt64(policy.MaxFileMB, 1, 200)
	maxBytes := maxMB * 1024 * 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+(1<<20))
	if err := r.ParseMultipartForm(min(maxBytes, 8<<20)); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "missing image file")
		return
	}
	_ = file.Close()
	if header.Size <= 0 || header.Size > maxBytes {
		utils.WriteError(w, http.StatusBadRequest, "file size exceeds limit")
		return
	}
	a.createImageFromUpload(w, r, header, user, isAdmin, maxBytes, policy)
}

func (a *App) createImageFromUpload(w http.ResponseWriter, r *http.Request, header *multipart.FileHeader, user *models.User, isAdmin bool, maxBytes int64, policy models.UploadPolicy) {
	uploadCtx, cancelUpload := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancelUpload()
	r = r.WithContext(uploadCtx)
	source, err := header.Open()
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "failed to read image")
		return
	}
	defer source.Close()
	mimeType, width, height, err := utils.DetectImageStream(source, header.Filename)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "failed to read image")
		return
	}
	if width > 0 && height > 0 && int64(width)*int64(height) > 120_000_000 {
		utils.WriteError(w, http.StatusBadRequest, "image dimensions are too large")
		return
	}
	allowedTypes := firstNonEmpty(policy.AllowedTypes, utils.GetSetting(a.DB, "allowed_types", "jpg,jpeg,png,gif,webp,avif,bmp,tif,tiff,ico,svg,heic,heif,jxl,psd"))
	if !utils.AllowedMime(mimeType, allowedTypes) {
		utils.WriteError(w, http.StatusBadRequest, "unsupported image type")
		return
	}
	var reader io.ReadSeeker = source
	imageSize := header.Size
	if mimeType == "image/svg+xml" {
		if !policy.AllowSVG {
			utils.WriteError(w, http.StatusBadRequest, "svg upload is disabled for this user group")
			return
		}
		const maxSVGBytes = 5 * 1024 * 1024
		if header.Size > maxSVGBytes {
			utils.WriteError(w, http.StatusBadRequest, "svg file is too large")
			return
		}
		body, readErr := utils.ReadMultipart(header, min(maxBytes, maxSVGBytes))
		if readErr != nil {
			utils.WriteError(w, http.StatusBadRequest, "failed to read svg")
			return
		}
		body, err = utils.SanitizeSVG(body)
		if err != nil {
			utils.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		reader = bytes.NewReader(body)
		imageSize = int64(len(body))
	}

	ext := utils.NormalizeImageExt(header.Filename, mimeType)
	random, err := utils.RandomHex(12)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to create image name")
		return
	}
	opts := a.storageOptions()
	uploadTarget, mode, targetErr := a.resolveUploadTarget(r, policy, user != nil, isAdmin)
	if targetErr != nil {
		utils.WriteError(w, http.StatusForbidden, targetErr.Error())
		return
	}
	if uploadTarget != nil {
		opts = a.Coordinator.TargetOptions(uploadTarget)
		if uploadTarget.LegacyMode != "" {
			uploadTarget = nil
		}
	}
	if (mode == "s3" || mode == "mirror") && !a.Store.S3Configured(opts) {
		utils.WriteError(w, http.StatusBadRequest, "s3 is not configured")
		return
	}
	if (mode == "local" || mode == "mirror") && a.Coordinator != nil {
		var capacityErr error
		if uploadTarget != nil {
			capacityErr = a.Coordinator.CheckTargetCapacity(uploadTarget)
		} else {
			capacityErr = a.Coordinator.CheckLocalCapacity()
		}
		if err := capacityErr; err != nil {
			utils.WriteError(w, http.StatusInsufficientStorage, err.Error())
			return
		}
	}
	releaseQuota, err := a.reserveUploadQuota(r.Context(), user, policy, imageSize)
	if err != nil {
		utils.WriteError(w, http.StatusForbidden, err.Error())
		return
	}
	defer releaseQuota()
	if err := a.consumePolicyUsage(policy, user, a.clientIP(r), imageSize); err != nil {
		utils.WriteError(w, http.StatusForbidden, err.Error())
		return
	}
	checksum, err := storage.ChecksumSHA256Reader(reader)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "failed to checksum image")
		return
	}
	// Read small originals once and reuse the bytes for animation detection
	// and thumbnails instead of reopening the multipart file for each step.
	const inspectLimit = 32 * 1024 * 1024
	var inspectBody []byte
	if mimeType != "image/svg+xml" && header.Size <= inspectLimit {
		inspectBody, _ = utils.ReadMultipart(header, header.Size)
	}
	isAnimated := false
	if inspectBody != nil {
		isAnimated = utils.IsAnimatedImage(inspectBody, mimeType)
	} else {
		isAnimated = utils.IsAnimatedMultipart(header, mimeType)
	}

	image := models.Image{
		PublicName:   random + ext,
		OriginalName: filepath.Base(header.Filename),
		MimeType:     mimeType,
		Ext:          ext,
		Size:         imageSize,
		Width:        width,
		Height:       height,
		StorageMode:  mode,
		UserID:       0,
		UploadIP:     a.clientIP(r),
		IsAnimated:   isAnimated,
	}
	if policy.RetentionDays > 0 {
		expiresAt := time.Now().AddDate(0, 0, policy.RetentionDays)
		image.ExpiresAt = &expiresAt
	}
	if user != nil {
		image.UserID = user.ID
		image.Username = user.Username
	}
	if err := a.Store.SaveReader(r.Context(), opts, mode, &image, reader, image.Size, checksum); err != nil {
		// Storage errors can include filesystem paths or S3 responses.
		log.Printf("upload: storing %s (%s) failed: %v", image.PublicName, mode, err)
		utils.WriteError(w, http.StatusInternalServerError, "failed to store image")
		return
	}
	if !image.IsAnimated && image.MimeType != "image/svg+xml" && int64(len(inspectBody)) == image.Size {
		a.createThumbnails(&image, inspectBody)
	}
	if err := a.insertImage(&image); err != nil {
		_ = a.Store.Delete(r.Context(), opts, &image)
		a.deleteThumbnails(&image)
		utils.WriteError(w, http.StatusInternalServerError, "failed to save image record")
		return
	}
	if a.Coordinator != nil {
		var registerErr error
		if uploadTarget != nil {
			registerErr = a.Coordinator.RegisterTargetImage(r.Context(), &image, uploadTarget, checksum)
		} else {
			registerErr = a.Coordinator.RegisterImage(r.Context(), &image, checksum)
		}
		if err := registerErr; err != nil {
			_ = a.Store.Delete(r.Context(), opts, &image)
			a.deleteThumbnails(&image)
			_, _ = a.DB.Exec(`DELETE FROM images WHERE id = ?`, image.ID)
			utils.WriteError(w, http.StatusInternalServerError, "failed to register storage replica")
			return
		}
	}
	baseURL := a.requestBaseURL(r)
	url := imageURL(baseURL, image.PublicName)
	utils.WriteJSON(w, http.StatusOK, map[string]any{
		"image":    a.imageResponse(&image, baseURL),
		"url":      url,
		"markdown": "![](" + url + ")",
		"html":     `<img src="` + url + `" alt="">`,
	})
}

func (a *App) ServeImage(w http.ResponseWriter, r *http.Request) {
	if !imageLimiter.Allow("image:"+a.clientIP(r), int(utils.SettingInt64(a.DB, "image_rate_per_min", 600, 30, 5000))) {
		utils.WriteError(w, http.StatusTooManyRequests, "too many image requests")
		return
	}
	name := filepath.Base(strings.TrimPrefix(r.URL.Path, "/i/"))
	if name == "." || name == "/" || strings.Contains(name, "..") {
		utils.WriteError(w, http.StatusBadRequest, "invalid image name")
		return
	}
	image, err := a.findImageByPublicName(name)
	if err != nil {
		utils.WriteError(w, http.StatusNotFound, "image not found")
		return
	}

	var filePath string
	opts := a.storageOptions()
	var named bool
	if a.Coordinator != nil {
		var loadErr error
		filePath, named, loadErr = a.Coordinator.NamedImagePath(r.Context(), image)
		if loadErr != nil {
			utils.WriteError(w, http.StatusBadGateway, "failed to load verified storage replica")
			return
		}
	}
	if named {
		w.Header().Set("X-Orimg-Storage", "named")
	} else if image.StorageMode == "mirror" && a.Coordinator != nil {
		localPath, hit, err := a.Coordinator.EnsureMirrorLocal(r.Context(), image)
		if err != nil {
			utils.WriteError(w, http.StatusBadGateway, "failed to load healthy image replica")
			return
		}
		filePath = localPath
		if hit {
			w.Header().Set("X-ZhuaPic-Replica", "LOCAL")
		} else {
			w.Header().Set("X-ZhuaPic-Replica", "REPAIRED")
		}
	} else if image.StorageMode == "s3" {
		if a.Coordinator != nil {
			if replicaKey, keyErr := a.Coordinator.ReadyReplicaKey(r.Context(), image.ID, "s3"); keyErr == nil {
				image.StorageKey = replicaKey
			}
		}
		ttl := utils.SettingCacheTTL(a.DB, a.Cfg.CacheTTL)
		cachePath, hit, err := a.Store.EnsureS3Cache(r.Context(), opts, image, ttl)
		if err != nil {
			utils.WriteError(w, http.StatusBadGateway, "failed to load image from storage")
			return
		}
		filePath = cachePath
		if hit {
			w.Header().Set("X-ZhuaPic-Cache", "HIT")
		} else {
			w.Header().Set("X-ZhuaPic-Cache", "MISS")
		}
	} else {
		localKey := image.StorageKey
		if a.Coordinator != nil {
			if replicaKey, keyErr := a.Coordinator.ReadyReplicaKey(r.Context(), image.ID, "local"); keyErr == nil {
				localKey = replicaKey
			}
		}
		filePath = a.Store.LocalPath(opts, localKey)
	}

	w.Header().Set("Content-Type", image.MimeType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if image.MimeType == "image/svg+xml" {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src data:; style-src 'unsafe-inline'")
	}
	http.ServeFile(w, r, filePath)
}

func (a *App) ServeThumbnail(w http.ResponseWriter, r *http.Request) {
	if !imageLimiter.Allow("thumb:"+a.clientIP(r), int(utils.SettingInt64(a.DB, "image_rate_per_min", 600, 30, 5000))) {
		utils.WriteError(w, http.StatusTooManyRequests, "too many image requests")
		return
	}
	size := r.PathValue("size")
	name := filepath.Base(r.PathValue("name"))
	if size != "small" && size != "medium" && size != "large" {
		utils.WriteError(w, http.StatusBadRequest, "invalid thumbnail size")
		return
	}
	image, err := a.findImageByPublicName(name)
	if err != nil {
		utils.WriteError(w, http.StatusNotFound, "image not found")
		return
	}
	key := image.ThumbMedium
	switch size {
	case "small":
		key = image.ThumbSmall
	case "large":
		key = image.ThumbLarge
	}
	if key == "" {
		http.Redirect(w, r, "/i/"+image.PublicName, http.StatusFound)
		return
	}
	path := filepath.Join(a.storageOptions().ThumbDir, filepath.Clean(key))
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, path)
}

func (a *App) ListImages(w http.ResponseWriter, r *http.Request) {
	limit := 60
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	user, ok := a.currentUser(r)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	scope := r.URL.Query().Get("scope")
	all := user.Role == "admin" && scope != "mine"
	cursor, err := decodeImageCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid image cursor")
		return
	}
	filter := imageListFilter{
		Query:   strings.TrimSpace(r.URL.Query().Get("q")),
		Storage: strings.ToLower(strings.TrimSpace(r.URL.Query().Get("storage"))),
	}
	if len(filter.Query) > 100 {
		utils.WriteError(w, http.StatusBadRequest, "image search is too long")
		return
	}
	if filter.Storage != "" && filter.Storage != "local" && filter.Storage != "s3" && filter.Storage != "mirror" {
		utils.WriteError(w, http.StatusBadRequest, "invalid storage filter")
		return
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("from")); raw != "" {
		filter.From, err = time.Parse("2006-01-02", raw)
		if err != nil {
			utils.WriteError(w, http.StatusBadRequest, "invalid from date")
			return
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("to")); raw != "" {
		filter.To, err = time.Parse("2006-01-02", raw)
		if err != nil {
			utils.WriteError(w, http.StatusBadRequest, "invalid to date")
			return
		}
		filter.To = filter.To.AddDate(0, 0, 1)
	}
	images, hasMore, err := a.listImages(limit, user.ID, all, cursor, filter)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to load images")
		return
	}
	out := make([]map[string]any, 0, len(images))
	baseURL := a.requestBaseURL(r)
	for i := range images {
		out = append(out, a.imageResponse(&images[i], baseURL))
	}
	nextCursor := ""
	if hasMore && len(images) > 0 {
		last := images[len(images)-1]
		nextCursor = encodeImageCursor(last.CreatedAt, last.ID)
	}
	utils.WriteJSON(w, http.StatusOK, map[string]any{"images": out, "next_cursor": nextCursor, "has_more": hasMore})
}

func (a *App) DeleteImage(w http.ResponseWriter, r *http.Request) {
	id, err := utils.UserIDFromString(r.PathValue("id"))
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid image id")
		return
	}
	image, err := a.findImageByID(id)
	if err != nil {
		utils.WriteError(w, http.StatusNotFound, "image not found")
		return
	}
	user, ok := a.currentUser(r)
	if !ok {
		utils.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if user.Role != "admin" && image.UserID != user.ID {
		utils.WriteError(w, http.StatusForbidden, "forbidden")
		return
	}
	days := utils.SettingInt64(a.DB, "recycle_bin_days", 7, 1, 30)
	if _, err := a.DB.Exec(`UPDATE images SET deleted_at = NOW(), delete_after = DATE_ADD(NOW(), INTERVAL ? DAY) WHERE id = ? AND deleted_at IS NULL`, days, image.ID); err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to move image to recycle bin")
		return
	}
	a.writeAudit(r, user, "image.deleted", "image", strconv.FormatUint(uint64(image.ID), 10), nil)
	utils.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true, "purge_after_days": days})
}

func (a *App) deleteThumbnails(image *models.Image) {
	opts := a.storageOptions()
	for _, key := range []string{image.ThumbSmall, image.ThumbMedium, image.ThumbLarge} {
		if key == "" || filepath.IsAbs(key) {
			continue
		}
		_ = os.Remove(filepath.Join(opts.ThumbDir, filepath.Clean(key)))
	}
}

func (a *App) createThumbnails(image *models.Image, body []byte) {
	if image.IsAnimated || image.MimeType == "image/svg+xml" || image.Width <= 0 || image.Height <= 0 {
		return
	}
	opts := a.storageOptions()
	day := time.Now().Format("20060102")
	base := strings.TrimSuffix(filepath.Base(image.PublicName), filepath.Ext(image.PublicName))
	targets := []struct {
		name string
		size int
		set  func(string)
	}{
		{"small", 320, func(path string) { image.ThumbSmall = path }},
		{"medium", 720, func(path string) { image.ThumbMedium = path }},
		{"large", 1440, func(path string) { image.ThumbLarge = path }},
	}
	sizes := make([]int, len(targets))
	for i, target := range targets {
		sizes[i] = target.size
	}
	thumbs, err := utils.Thumbnails(body, sizes)
	if err != nil {
		return
	}
	for i, target := range targets {
		thumb := thumbs[i]
		rel := filepath.Join(day, target.name, base+".jpg")
		dest := filepath.Join(opts.ThumbDir, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return
		}
		if err := os.WriteFile(dest, thumb, 0644); err != nil {
			return
		}
		target.set(rel)
	}
}

func (a *App) insertImage(image *models.Image) error {
	res, err := a.DB.Exec(
		`INSERT INTO images
		(public_name, original_name, mime_type, ext, size, width, height, storage_mode, storage_key, user_id, upload_ip, thumb_small, thumb_medium, thumb_large, is_animated, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, 0), ?, ?, ?, ?, ?, ?)`,
		image.PublicName,
		image.OriginalName,
		image.MimeType,
		image.Ext,
		image.Size,
		image.Width,
		image.Height,
		image.StorageMode,
		image.StorageKey,
		image.UserID,
		image.UploadIP,
		image.ThumbSmall,
		image.ThumbMedium,
		image.ThumbLarge,
		image.IsAnimated,
		image.ExpiresAt,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	image.ID = uint(id)
	image.CreatedAt = time.Now()
	return nil
}

func (a *App) findImageByPublicName(name string) (*models.Image, error) {
	return scanImage(a.DB.QueryRow(imageSelectSQL()+` WHERE images.public_name = ? AND images.deleted_at IS NULL`, name))
}

func (a *App) findImageByID(id uint) (*models.Image, error) {
	return scanImage(a.DB.QueryRow(imageSelectSQL()+` WHERE images.id = ? AND images.deleted_at IS NULL`, id))
}

type imageListFilter struct {
	Query   string
	Storage string
	From    time.Time
	To      time.Time
}

func (a *App) listImages(limit int, userID uint, all bool, cursor *imageCursor, filter imageListFilter) ([]models.Image, bool, error) {
	query := imageSelectSQL()
	args := []any{}
	if !all {
		query += ` WHERE images.user_id = ? AND images.deleted_at IS NULL`
		args = append(args, userID)
	} else {
		query += ` WHERE images.deleted_at IS NULL`
	}
	if cursor != nil {
		query += ` AND (images.created_at < ? OR (images.created_at = ? AND images.id < ?))`
		args = append(args, cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
	}
	if filter.Query != "" {
		like := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(filter.Query) + "%"
		query += ` AND (images.original_name LIKE ? OR images.public_name LIKE ?`
		args = append(args, like, like)
		if all {
			query += ` OR users.username LIKE ? OR users.email LIKE ?`
			args = append(args, like, like)
		}
		query += `)`
	}
	if !filter.From.IsZero() {
		query += ` AND images.created_at >= ?`
		args = append(args, filter.From)
	}
	if !filter.To.IsZero() {
		query += ` AND images.created_at < ?`
		args = append(args, filter.To)
	}
	if filter.Storage != "" {
		switch filter.Storage {
		case "mirror", "local", "s3":
			query += " AND (" + effectiveStorageModeSQL() + ") = ?"
			args = append(args, filter.Storage)
		}
	}
	query += ` ORDER BY images.created_at DESC, images.id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := a.DB.Query(query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	images := []models.Image{}
	for rows.Next() {
		image, err := scanImage(rows)
		if err != nil {
			return nil, false, err
		}
		images = append(images, *image)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(images) > limit
	if hasMore {
		images = images[:limit]
	}
	return images, hasMore, nil
}

type imageScanner interface {
	Scan(dest ...any) error
}

func scanImage(scanner imageScanner) (*models.Image, error) {
	var image models.Image
	err := scanner.Scan(
		&image.ID,
		&image.PublicName,
		&image.OriginalName,
		&image.MimeType,
		&image.Ext,
		&image.Size,
		&image.Width,
		&image.Height,
		&image.StorageMode,
		&image.StorageKey,
		&image.UserID,
		&image.Username,
		&image.UploadIP,
		&image.ThumbSmall,
		&image.ThumbMedium,
		&image.ThumbLarge,
		&image.IsAnimated,
		&image.ExpiresAt,
		&image.CreatedAt,
		&image.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &image, nil
}

func imageSelectSQL() string {
	return `SELECT images.id, images.public_name, images.original_name, images.mime_type, images.ext, images.size, images.width, images.height,
		` + effectiveStorageModeSQL() + `,
		images.storage_key, COALESCE(images.user_id, 0), COALESCE(users.username, '游客'), images.upload_ip,
		images.thumb_small, images.thumb_medium, images.thumb_large, images.is_animated, images.expires_at, images.created_at, images.updated_at
		FROM images LEFT JOIN users ON users.id = images.user_id`
}

func effectiveStorageModeSQL() string {
	return `CASE
	 WHEN (SELECT COUNT(*) FROM image_replicas r WHERE r.image_id=images.id AND r.status='ready') +
	      (SELECT COUNT(*) FROM storage_target_replicas r WHERE r.image_id=images.id AND r.status='ready') > 1 THEN 'mirror'
	 WHEN EXISTS (SELECT 1 FROM image_replicas r WHERE r.image_id=images.id AND r.storage_mode='local' AND r.status='ready')
	   OR EXISTS (SELECT 1 FROM storage_target_replicas r JOIN storage_targets t ON t.id=r.target_id WHERE r.image_id=images.id AND r.status='ready' AND t.provider='local') THEN 'local'
	 WHEN EXISTS (SELECT 1 FROM image_replicas r WHERE r.image_id=images.id AND r.storage_mode='s3' AND r.status='ready')
	   OR EXISTS (SELECT 1 FROM storage_target_replicas r JOIN storage_targets t ON t.id=r.target_id WHERE r.image_id=images.id AND r.status='ready' AND t.provider<>'local') THEN 's3'
	 ELSE images.storage_mode END`
}

func (a *App) imageResponse(image *models.Image, baseURL string) map[string]any {
	result := map[string]any{
		"id":            image.ID,
		"public_name":   image.PublicName,
		"original_name": image.OriginalName,
		"mime_type":     image.MimeType,
		"size":          image.Size,
		"width":         image.Width,
		"height":        image.Height,
		"storage_mode":  image.StorageMode,
		"user_id":       image.UserID,
		"username":      image.Username,
		"is_animated":   image.IsAnimated,
		"url":           imageURL(baseURL, image.PublicName),
		"thumb_small":   thumbnailURL(baseURL, "small", image),
		"thumb_medium":  thumbnailURL(baseURL, "medium", image),
		"thumb_large":   thumbnailURL(baseURL, "large", image),
		"created_at":    image.CreatedAt.Format(time.RFC3339),
		"date":          image.CreatedAt.Format("2006-01-02"),
	}
	if image.ExpiresAt != nil {
		result["expires_at"] = image.ExpiresAt.Format(time.RFC3339)
	}
	return result
}

func (a *App) imageURL(publicName string) string {
	return imageURL(a.Cfg.BaseURL, publicName)
}

func imageURL(baseURL, publicName string) string {
	return strings.TrimRight(baseURL, "/") + "/i/" + publicName
}

func thumbnailURL(baseURL, size string, image *models.Image) string {
	hasThumb := false
	switch size {
	case "small":
		hasThumb = image.ThumbSmall != ""
	case "medium":
		hasThumb = image.ThumbMedium != ""
	case "large":
		hasThumb = image.ThumbLarge != ""
	}
	if !hasThumb {
		return imageURL(baseURL, image.PublicName)
	}
	return strings.TrimRight(baseURL, "/") + "/t/" + size + "/" + image.PublicName
}

func (a *App) requestBaseURL(r *http.Request) string {
	scheme := utils.RequestScheme(r, a.Cfg.TrustedProxies)
	return selectRequestBaseURL(a.Cfg.BaseURL, utils.GetSetting(a.DB, "site_domains", ""), r.Host, scheme)
}

func selectRequestBaseURL(baseURL, siteDomains, requestHost, scheme string) string {
	requestHost = normalizeDomain(requestHost)
	allowed := map[string]bool{}
	if configured := baseURLDomain(baseURL); configured != "" {
		allowed[configured] = true
	}
	for _, domain := range strings.Split(siteDomains, ",") {
		if domain = normalizeDomain(domain); domain != "" {
			allowed[domain] = true
		}
	}
	if requestHost == "" || !allowed[requestHost] {
		return strings.TrimRight(baseURL, "/")
	}
	if scheme != "http" && scheme != "https" {
		scheme = "https"
	}
	return scheme + "://" + requestHost
}

func (a *App) storageOptions() storage.Options {
	settings := utils.GetSettings(a.DB)
	opts := a.Store.DefaultOptions()
	opts.UploadDir = firstNonEmpty(settings["local_upload_dir"], a.Cfg.UploadDir)
	opts.CacheDir = firstNonEmpty(settings["s3_cache_dir"], a.Cfg.CacheDir)
	opts.ThumbDir = firstNonEmpty(settings["thumb_dir"], "data/thumbs")
	opts.S3Prefix = firstNonEmpty(settings["s3_key_prefix"], "images")
	opts.S3.Endpoint = strings.TrimRight(firstNonEmpty(settings["s3_endpoint"], a.Cfg.S3.Endpoint), "/")
	opts.S3.Region = firstNonEmpty(settings["s3_region"], a.Cfg.S3.Region)
	opts.S3.Bucket = firstNonEmpty(settings["s3_bucket"], a.Cfg.S3.Bucket)
	opts.S3.AccessKey = firstNonEmpty(settings["s3_access_key"], a.Cfg.S3.AccessKey)
	opts.S3.SecretKey = firstNonEmpty(settings["s3_secret_key"], a.Cfg.S3.SecretKey)
	if decrypted, err := utils.DecryptSecret(opts.S3.SecretKey, a.Cfg.JWTSecret); err == nil {
		opts.S3.SecretKey = decrypted
	} else {
		opts.S3.SecretKey = ""
	}
	opts.S3.ForcePathStyle = settingBoolString(settings["s3_force_path_style"], a.Cfg.S3.ForcePathStyle)
	return opts
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func settingBoolString(raw string, fallback bool) bool {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return fallback
	}
	return raw == "true" || raw == "1" || raw == "yes" || raw == "on"
}

func sanitizeSetting(key, value string, cfg *config.Config) string {
	value = strings.TrimSpace(value)
	switch key {
	case "background_url":
		return sanitizeBackgroundURL(value)
	case "background_overlay":
		return boundedBackgroundNumber(value, 75, 95, 82)
	case "background_blur":
		return boundedBackgroundNumber(value, 0, 20, 0)
	case "default_accent_color":
		switch value {
		case "lime", "blue", "purple", "pink", "red", "orange", "yellow", "green", "graphite":
			return value
		default:
			return "lime"
		}
	case "site_logo_url":
		if value == "" {
			return "/assets/logo.svg"
		}
		if strings.HasPrefix(value, "/assets/") || strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") {
			return value
		}
		return "/assets/logo.svg"
	case "upload_mode":
		return "classic"
	case "admin_path", "user_path":
		return sanitizeSitePath(value, map[string]string{
			"admin_path": "/admin",
			"user_path":  "/dashboard",
		}[key])
	case "storage_mode":
		if value != "s3" && value != "mirror" {
			return "local"
		}
	case "site_domains":
		return sanitizeDomains(value, cfg.BaseURL)
	case "public_upload_enabled":
		lower := strings.ToLower(value)
		if lower == "true" || lower == "1" || lower == "yes" || lower == "on" {
			return "true"
		}
		return "false"
	case "max_upload_mb":
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return strconv.FormatInt(cfg.MaxUploadMB, 10)
		}
		if parsed < 1 {
			parsed = 1
		}
		if parsed > 200 {
			parsed = 200
		}
		return strconv.FormatInt(parsed, 10)
	case "cache_ttl_days":
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return "30"
		}
		if parsed < 1 {
			parsed = 1
		}
		if parsed > 365 {
			parsed = 365
		}
		return strconv.FormatInt(parsed, 10)
	case "disk_stop_percent":
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return "85"
		}
		if parsed < 50 {
			parsed = 50
		}
		if parsed > 99 {
			parsed = 99
		}
		return strconv.FormatInt(parsed, 10)
	case "recycle_bin_days":
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return "7"
		}
		if parsed < 1 {
			parsed = 1
		}
		if parsed > 30 {
			parsed = 30
		}
		return strconv.FormatInt(parsed, 10)
	case "db_backup_hour":
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 0 || parsed > 23 {
			return "4"
		}
		return strconv.FormatInt(parsed, 10)
	case "db_backup_target_id":
		parsed, err := strconv.ParseUint(value, 10, 31)
		if err != nil {
			return "0"
		}
		return strconv.FormatUint(parsed, 10)
	case "upload_rate_per_min", "image_rate_per_min", "login_rate_per_min":
		fallback := int64(60)
		min := int64(1)
		max := int64(5000)
		if key == "image_rate_per_min" {
			fallback = 600
			min = 30
		}
		if key == "login_rate_per_min" {
			fallback = 20
			max = 300
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return strconv.FormatInt(fallback, 10)
		}
		if parsed < min {
			parsed = min
		}
		if parsed > max {
			parsed = max
		}
		return strconv.FormatInt(parsed, 10)
	case "allowed_types":
		parts := []string{}
		seen := map[string]bool{}
		for _, item := range strings.Split(strings.ToLower(value), ",") {
			item = strings.Trim(strings.TrimSpace(item), ".")
			switch item {
			case "jpg", "jpeg", "png", "gif", "webp", "avif", "bmp", "tif", "tiff", "ico", "svg", "heic", "heif", "jxl", "psd":
				if !seen[item] {
					parts = append(parts, item)
					seen[item] = true
				}
			}
		}
		if len(parts) == 0 {
			return "jpg,jpeg,png,gif,webp,avif,bmp,tif,tiff,ico,svg,heic,heif,jxl,psd"
		}
		return strings.Join(parts, ",")
	case "local_upload_dir", "s3_cache_dir", "thumb_dir":
		if value == "" {
			if key == "local_upload_dir" {
				return cfg.UploadDir
			}
			if key == "s3_cache_dir" {
				return cfg.CacheDir
			}
			return "data/thumbs"
		}
		return filepath.Clean(value)
	case "s3_endpoint":
		return strings.TrimRight(value, "/")
	case "s3_region":
		if value == "" {
			return "auto"
		}
	case "s3_force_path_style":
		lower := strings.ToLower(value)
		if lower == "true" || lower == "1" || lower == "yes" || lower == "on" {
			return "true"
		}
		return "false"
	case "s3_key_prefix", "db_backup_s3_prefix":
		return sanitizeObjectPrefix(value)
	case "mysqldump_path":
		if value == "" {
			return "mysqldump"
		}
		clean := filepath.Clean(value)
		if filepath.Base(clean) != "mysqldump" {
			return "mysqldump"
		}
		return clean
	case "login_captcha_enabled", "replica_worker_enabled", "db_backup_enabled":
		lower := strings.ToLower(value)
		if lower == "true" || lower == "1" || lower == "yes" || lower == "on" {
			return "true"
		}
		return "false"
	}
	return value
}

func sanitizeDomains(value, baseURL string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == ' '
	})
	seen := map[string]bool{}
	out := make([]string, 0, len(parts)+1)
	if domain := baseURLDomain(baseURL); domain != "" {
		seen[domain] = true
		out = append(out, domain)
	}
	for _, part := range parts {
		domain := normalizeDomain(part)
		if domain != "" && !seen[domain] {
			seen[domain] = true
			out = append(out, domain)
		}
	}
	return strings.Join(out, ",")
}

func baseURLDomain(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return normalizeDomain(parsed.Host)
}

func normalizeDomain(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.User != nil || parsed.Host == "" {
			return ""
		}
		raw = parsed.Host
	}
	parsed, err := url.Parse("http://" + raw)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	hostname := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if hostname == "" {
		return ""
	}
	if hostname != "localhost" && net.ParseIP(hostname) == nil {
		for _, label := range strings.Split(hostname, ".") {
			if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
				return ""
			}
			for _, char := range label {
				if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
					return ""
				}
			}
		}
	}
	if port := parsed.Port(); port != "" {
		return net.JoinHostPort(hostname, port)
	}
	return hostname
}

func sanitizeSitePath(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	value = "/" + strings.Trim(value, "/")
	if value == "/" || strings.Contains(value, "..") || strings.HasPrefix(value, "/api") || strings.HasPrefix(value, "/i/") || strings.HasPrefix(value, "/t/") || strings.HasPrefix(value, "/assets") {
		return fallback
	}
	return value
}

func sanitizeObjectPrefix(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.Trim(value, "/")
	parts := []string{}
	for _, part := range strings.Split(value, "/") {
		part = strings.TrimSpace(part)
		if part == "" || part == "." || part == ".." {
			continue
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "images"
	}
	return strings.Join(parts, "/")
}

func (a *App) clientIP(r *http.Request) string {
	return utils.ClientIP(r, a.Cfg.TrustedProxies)
}

func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
