package installer

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"zhuapic/config"
	"zhuapic/database"
	"zhuapic/utils"
)

type Request struct {
	Host          string `json:"host"`
	Port          string `json:"port"`
	User          string `json:"user"`
	Password      string `json:"password"`
	Name          string `json:"name"`
	TLS           string `json:"tls"`
	CA            string `json:"ca"`
	BaseURL       string `json:"base_url"`
	Existing      bool   `json:"existing"`
	Secret        string `json:"original_secret"`
	AdminUsername string `json:"admin_username"`
	AdminEmail    string `json:"admin_email"`
	AdminPassword string `json:"admin_password"`
	Confirmed     bool   `json:"confirmed"`
	EnableWorkers bool   `json:"enable_workers"`
}

type Server struct {
	Base      *config.Config
	Path      string
	Pending   *config.Installation
	Token     string
	Activate  func(*config.Config, *sql.DB)
	mu        sync.Mutex
	completed bool
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/install/check", s.protect(s.check))
	mux.HandleFunc("POST /api/install/complete", s.protect(s.complete))
	mux.HandleFunc("GET /api/install/status", s.protect(func(w http.ResponseWriter, r *http.Request) {
		utils.WriteJSON(w, 200, map[string]any{"pending": s.Pending != nil, "version": config.AppVersion})
	}))
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { utils.WriteJSON(w, 200, map[string]bool{"ok": true}) })
	mux.HandleFunc("GET /api/ready", func(w http.ResponseWriter, r *http.Request) { utils.WriteError(w, 503, "installation required") })
	mux.HandleFunc("GET /assets/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/assets/")
		allowed := map[string]bool{"install.js": true, "install.css": true, "styles.css": true, "design.css": true, "appearance.css": true, "appearance.js": true, "logo.svg": true, "logo-mark.svg": true, "favicon.svg": true}
		if !allowed[name] {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join("static", "assets", name))
	})
	mux.HandleFunc("GET /install", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, "static/install.html") })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			utils.WriteError(w, 503, "installation required")
			return
		}
		http.Redirect(w, r, "/install", http.StatusSeeOther)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			controller := http.NewResponseController(w)
			_ = controller.SetReadDeadline(time.Now().Add(30 * time.Second))
			defer controller.SetReadDeadline(time.Time{})
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'; object-src 'none'")
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) protect(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !utils.SecureSetupRequest(r, s.Base.TrustedProxies) {
			utils.WriteError(w, 403, "安装需要 HTTPS 或本机回环地址连接")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host || u.Scheme != utils.RequestScheme(r, s.Base.TrustedProxies) {
				utils.WriteError(w, 403, "不允许跨站安装请求")
				return
			}
		}
		expected := sha256.Sum256([]byte(s.Token))
		supplied := sha256.Sum256([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")))
		if s.Token == "" || subtle.ConstantTimeCompare(expected[:], supplied[:]) != 1 {
			utils.WriteError(w, 401, "安装码不正确，请查看服务器启动日志")
			return
		}
		if !s.mu.TryLock() {
			utils.WriteError(w, 409, "另一个安装操作正在进行，请稍候")
			return
		}
		defer s.mu.Unlock()
		if s.completed {
			utils.WriteError(w, 410, "安装已完成")
			return
		}
		next(w, r)
	}
}

func (s *Server) request(w http.ResponseWriter, r *http.Request) (*config.Config, Request, error) {
	var req Request
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return nil, req, errors.New("安装表单无效")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, req, errors.New("安装表单必须为单个 JSON 对象")
	}
	cfg := *s.Base
	cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName = strings.TrimSpace(req.Host), req.Port, req.User, req.Password, req.Name
	cfg.DBTLS, cfg.DBTLSCA = req.TLS, req.CA
	cfg.BaseURL = strings.TrimRight(req.BaseURL, "/")
	cfg.AdminUsername, cfg.AdminEmail, cfg.AdminPassword = req.AdminUsername, strings.ToLower(strings.TrimSpace(req.AdminEmail)), req.AdminPassword
	// Probe does not use or create a signing key. Complete replaces this placeholder.
	cfg.JWTSecret = "0123456789abcdef0123456789abcdef"
	if err := cfg.Validate(); err != nil {
		return nil, req, err
	}
	if s.Pending != nil {
		old := s.Pending.Config
		if old.DBHost != cfg.DBHost || old.DBPort != cfg.DBPort || old.DBName != cfg.DBName || old.DBUser != cfg.DBUser {
			return nil, req, errors.New("未完成的安装已绑定原数据库，请使用相同的主机、库名和用户继续")
		}
		cfg.JWTSecret = old.JWTSecret
	}
	return &cfg, req, nil
}

func (s *Server) check(w http.ResponseWriter, r *http.Request) {
	cfg, _, err := s.request(w, r)
	if err != nil {
		utils.WriteError(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	db, err := database.Open(ctx, cfg)
	if err != nil {
		utils.WriteError(w, 400, "无法连接数据库，请检查地址、凭据、权限和 TLS 配置")
		return
	}
	defer db.Close()
	info, err := database.Inspect(ctx, db, s.Pending != nil && s.Pending.Fresh)
	if err != nil {
		utils.WriteError(w, 400, err.Error())
		return
	}
	utils.WriteJSON(w, 200, map[string]any{"database": info, "resume": s.Pending != nil, "bootstrap": info.Empty || s.Pending != nil && s.Pending.Fresh && info.Users == 0})
}

func (s *Server) complete(w http.ResponseWriter, r *http.Request) {
	cfg, req, err := s.request(w, r)
	if err != nil {
		utils.WriteError(w, 400, err.Error())
		return
	}
	if !req.Confirmed {
		utils.WriteError(w, 400, "请确认数据库备份及持久化目录已准备好")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	db, err := database.Open(ctx, cfg)
	if err != nil {
		utils.WriteError(w, 400, "无法连接数据库")
		return
	}
	keep := false
	defer func() {
		if !keep {
			db.Close()
		}
	}()
	info, err := database.Inspect(ctx, db, s.Pending != nil && s.Pending.Fresh)
	if err != nil {
		utils.WriteError(w, 400, err.Error())
		return
	}
	fresh := info.Empty
	if s.Pending != nil {
		fresh = s.Pending.Fresh
	}
	if s.Pending == nil && req.Existing == info.Empty {
		utils.WriteError(w, 409, "数据库状态已变化，请重新检查")
		return
	}
	if !info.Empty && info.Users > 0 {
		if err = database.VerifyAdministrator(ctx, db, req.AdminEmail, req.AdminPassword); err != nil {
			utils.WriteError(w, 403, "原管理员邮箱或密码不正确")
			return
		}
	}
	if !fresh {
		if info.ActiveAdmins == 0 {
			utils.WriteError(w, 400, "旧库没有可用管理员，请先恢复原管理员账号；不会自动创建或提升账号权限")
			return
		}
		if s.Pending == nil {
			cfg.JWTSecret = req.Secret
		}
		cfg.DisableBackgroundWork = !req.EnableWorkers
	} else {
		if info.Users == 0 {
			if err = config.ValidateAdmin(cfg.AdminUsername, cfg.AdminEmail, cfg.AdminPassword); err != nil {
				utils.WriteError(w, 400, err.Error())
				return
			}
		}
		if s.Pending == nil {
			cfg.JWTSecret, err = utils.RandomHex(32)
			if err != nil {
				utils.WriteError(w, 500, "无法生成安装密钥")
				return
			}
		}
	}
	if err = database.PrepareRuntimeSecurity(ctx, db, cfg, fresh, s.Path); err != nil {
		utils.WriteError(w, 400, err.Error())
		return
	}
	for _, dir := range []string{cfg.AssetDir, cfg.UploadDir, cfg.CacheDir} {
		if err = os.MkdirAll(dir, 0750); err != nil {
			utils.WriteError(w, 500, "数据目录不可写，请检查挂载和权限")
			return
		}
	}
	checkpoint := &config.Installation{Fresh: fresh, Config: *cfg}
	if err = utils.PreserveSiteAssets(cfg.AssetDir); err != nil {
		utils.WriteError(w, 500, "无法保留已有背景或 Logo，请检查资源目录权限")
		return
	}
	if err = config.WriteInstallation(s.Path, checkpoint); err != nil {
		utils.WriteError(w, 500, "安装配置不可写，数据库尚未升级")
		return
	}
	s.Pending = checkpoint
	if err = database.Initialize(ctx, db, cfg, fresh); err != nil {
		log.Printf("Installation initialization failed: %v", err)
		utils.WriteError(w, 500, "初始化未完成，配置已安全保存。请查看服务日志后使用同一数据库重试；不要删除数据库表。")
		return
	}
	checkpoint.Installed = true
	if err = config.WriteInstallation(s.Path, checkpoint); err != nil {
		utils.WriteError(w, 500, "数据库已就绪，但安装状态保存失败；修复配置目录权限后重试")
		return
	}
	cfg.AdminPassword = ""
	s.completed = true
	s.Activate(cfg, db)
	keep = true
	utils.WriteJSON(w, 200, map[string]any{"installed": true, "login_url": "/dashboard", "workers_paused": cfg.DisableBackgroundWork})
}
