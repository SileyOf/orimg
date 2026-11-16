package handlers

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"net/http"
	"regexp"
	"zhuapic/utils"
)

var avatarName = regexp.MustCompile(`^[a-f0-9]{32}\.png$`)

func normalizeAvatar(body []byte) ([]byte, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || (format != "png" && format != "jpeg") || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 512 || cfg.Height > 512 {
		return nil, fmt.Errorf("头像需为不超过 512×512 的 PNG 或 JPEG")
	}
	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (a *App) UploadAvatar(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(r)
	if !ok {
		utils.WriteError(w, 401, "unauthorized")
		return
	}
	if !uploadLimiter.Allow(fmt.Sprintf("avatar:%d", user.ID), 10) {
		utils.WriteError(w, 429, "头像更换过于频繁")
		return
	}
	const limit = 2 << 20
	r.Body = http.MaxBytesReader(w, r.Body, limit+65536)
	if err := r.ParseMultipartForm(limit + 65536); err != nil {
		utils.WriteError(w, 400, "头像超过 2 MB")
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("avatar")
	if err != nil {
		utils.WriteError(w, 400, "请选择头像")
		return
	}
	file.Close()
	body, err := utils.ReadMultipart(header, limit)
	if err != nil {
		utils.WriteError(w, 400, "头像读取失败")
		return
	}
	body, err = normalizeAvatar(body)
	if err != nil {
		utils.WriteError(w, 400, err.Error())
		return
	}
	id, err := utils.RandomHex(16)
	if err != nil {
		utils.WriteError(w, 500, "头像保存失败")
		return
	}
	name := id + ".png"
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		utils.WriteError(w, 500, "头像保存失败")
		return
	}
	defer tx.Rollback()
	// One row per account makes replacement bounded and keeps avatars in normal database backups.
	if _, err = tx.Exec(`UPDATE users SET avatar_url=? WHERE id=?`, "/avatars/"+name, user.ID); err == nil {
		_, err = tx.Exec(`INSERT INTO user_avatars(name,user_id,content) VALUES(?,?,?) ON DUPLICATE KEY UPDATE name=VALUES(name),content=VALUES(content),created_at=CURRENT_TIMESTAMP`, name, user.ID, body)
	}
	if err != nil {
		utils.WriteError(w, 500, "头像保存失败")
		return
	}
	if err = tx.Commit(); err != nil {
		utils.WriteError(w, 500, "头像保存失败")
		return
	}
	a.writeAudit(r, user, "profile.avatar_updated", "user", fmt.Sprint(user.ID), nil)
	utils.WriteJSON(w, 200, map[string]string{"avatar_url": "/avatars/" + name})
}

func (a *App) DeleteAvatar(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(r)
	if !ok {
		utils.WriteError(w, 401, "unauthorized")
		return
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		utils.WriteError(w, 500, "头像移除失败")
		return
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE users SET avatar_url='' WHERE id=?`, user.ID); err == nil {
		_, err = tx.Exec(`DELETE FROM user_avatars WHERE user_id=?`, user.ID)
	}
	if err != nil {
		utils.WriteError(w, 500, "头像移除失败")
		return
	}
	if err = tx.Commit(); err != nil {
		utils.WriteError(w, 500, "头像移除失败")
		return
	}
	a.writeAudit(r, user, "profile.avatar_removed", "user", fmt.Sprint(user.ID), nil)
	utils.WriteJSON(w, 200, map[string]string{"avatar_url": ""})
}

func (a *App) ServeAvatar(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !avatarName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	var body []byte
	if err := a.DB.QueryRowContext(r.Context(), `SELECT content FROM user_avatars JOIN users ON users.id=user_avatars.user_id WHERE user_avatars.name=? AND users.active=1`, name).Scan(&body); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	w.Write(body)
}
