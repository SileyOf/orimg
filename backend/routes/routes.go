package routes

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"time"

	"zhuapic/config"
	"zhuapic/handlers"
	"zhuapic/storage"
	"zhuapic/utils"
)

func Setup(db *sql.DB, cfg *config.Config, store *storage.Manager, coordinators ...*storage.Coordinator) http.Handler {
	coordinator := storage.NewCoordinator(db, cfg, store)
	if len(coordinators) > 0 && coordinators[0] != nil {
		coordinator = coordinators[0]
	}
	app := &handlers.App{DB: db, Cfg: cfg, Store: store, Coordinator: coordinator}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		utils.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			utils.WriteError(w, 503, "database unavailable")
			return
		}
		utils.WriteJSON(w, 200, map[string]bool{"ready": true})
	})
	mux.HandleFunc("/install", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/api/install/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("GET /api/settings/public", app.PublicSettings)
	mux.HandleFunc("GET /api/storage/options", app.UploadTargets)
	mux.HandleFunc("GET /api/admin/storage/cache", app.RequireAdmin(app.StorageCache))
	mux.HandleFunc("GET /api/auth/captcha", app.Captcha)
	mux.HandleFunc("POST /api/auth/login", app.Login)
	mux.HandleFunc("GET /api/mail/status", app.MailStatus)
	mux.HandleFunc("POST /api/auth/register", app.RequestRegistration)
	mux.HandleFunc("POST /api/auth/register/confirm", app.ConfirmRegistration)
	mux.HandleFunc("GET /api/admin/mail/registration", app.RequireAdmin(app.RegistrationSettings))
	mux.HandleFunc("PUT /api/admin/mail/registration", app.RequireAdmin(app.RegistrationSettings))
	mux.HandleFunc("GET /api/admin/mail/templates/{kind}", app.RequireAdmin(app.MailTemplates))
	mux.HandleFunc("PUT /api/admin/mail/templates/{kind}", app.RequireAdmin(app.MailTemplates))
	mux.HandleFunc("POST /api/admin/mail/templates/{kind}", app.RequireAdmin(app.MailTemplates))
	for _, page := range []string{"forgot-password", "register"} {
		mux.HandleFunc("GET /"+page, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			http.ServeFile(w, r, "static/"+page+".html")
		})
	}
	mux.HandleFunc("POST /api/auth/mail/confirm", app.ConfirmAccountMail)
	mux.HandleFunc("POST /api/auth/mail/{purpose}", app.RequestAccountMail)
	mux.HandleFunc("GET /api/admin/mail/config", app.RequireAdmin(app.MailConfig))
	mux.HandleFunc("PUT /api/admin/mail/config", app.RequireAdmin(app.MailConfig))
	mux.HandleFunc("POST /api/admin/mail/test", app.RequireAdmin(app.MailTest))
	mux.HandleFunc("GET /api/admin/mail/jobs", app.RequireAdmin(app.MailJobs))
	mux.HandleFunc("POST /api/admin/mail/jobs/{id}", app.RequireAdmin(app.MailJobs))
	mux.HandleFunc("GET /account-action", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, "static/account-action.html")
	})
	mux.HandleFunc("GET /api/me", app.RequireLogin(app.Me))
	mux.HandleFunc("PUT /api/me", app.RequireLogin(app.UpdateMe))
	mux.HandleFunc("POST /api/me/avatar", app.RequireLogin(app.UploadAvatar))
	mux.HandleFunc("DELETE /api/me/avatar", app.RequireLogin(app.DeleteAvatar))
	mux.HandleFunc("GET /avatars/{name}", app.ServeAvatar)
	mux.HandleFunc("GET /api/user/dashboard", app.RequireLogin(app.UserDashboard))
	mux.HandleFunc("GET /api/user/tokens", app.RequireLogin(app.UserTokens))
	mux.HandleFunc("POST /api/user/tokens", app.RequireLogin(app.UserTokens))
	mux.HandleFunc("DELETE /api/user/tokens/{id}", app.RequireLogin(app.DeleteUserToken))
	mux.HandleFunc("POST /api/upload", app.Upload)
	mux.HandleFunc("GET /api/admin/settings", app.RequireAdmin(app.AdminSettings))
	mux.HandleFunc("PUT /api/admin/settings", app.RequireAdmin(app.UpdateSettings))
	mux.HandleFunc("POST /api/admin/logo", app.RequireAdmin(app.UploadLogo))
	mux.HandleFunc("POST /api/admin/background", app.RequireAdmin(app.UploadBackground))
	mux.HandleFunc("GET /api/admin/dashboard", app.RequireAdmin(app.AdminDashboard))
	mux.HandleFunc("GET /api/admin/audit", app.RequireAdmin(app.AdminAuditLogs))
	mux.HandleFunc("GET /api/admin/groups", app.RequireAdmin(app.AdminGroups))
	mux.HandleFunc("PUT /api/admin/guest-group", app.RequireAdmin(app.UpdateGuestGroup))
	mux.HandleFunc("POST /api/admin/groups", app.RequireAdmin(app.AdminGroups))
	mux.HandleFunc("PUT /api/admin/groups/{id}", app.RequireAdmin(app.AdminGroup))
	mux.HandleFunc("DELETE /api/admin/groups/{id}", app.RequireAdmin(app.AdminGroup))
	mux.HandleFunc("PUT /api/admin/policies/{id}", app.RequireAdmin(app.UpdateUploadPolicy))
	mux.HandleFunc("GET /api/admin/storage", app.RequireAdmin(app.StorageOverview))
	mux.HandleFunc("GET /api/admin/storage/targets", app.RequireAdmin(app.StorageTargets))
	mux.HandleFunc("POST /api/admin/storage/targets", app.RequireAdmin(app.StorageTargets))
	mux.HandleFunc("PUT /api/admin/storage/targets/{id}", app.RequireAdmin(app.StorageTarget))
	mux.HandleFunc("DELETE /api/admin/storage/targets/{id}", app.RequireAdmin(app.StorageTarget))
	mux.HandleFunc("POST /api/admin/storage/targets/{id}/actions", app.RequireAdmin(app.StorageTargetAction))
	mux.HandleFunc("POST /api/admin/storage/jobs", app.RequireAdmin(app.CreateStorageJob))
	mux.HandleFunc("PUT /api/admin/storage/jobs/{id}", app.RequireAdmin(app.ControlStorageJob))
	mux.HandleFunc("POST /api/admin/storage/backup", app.RequireAdmin(app.CreateDatabaseBackup))
	mux.HandleFunc("GET /api/admin/trash", app.RequireAdmin(app.ListTrash))
	mux.HandleFunc("POST /api/admin/trash/{id}/restore", app.RequireAdmin(app.RestoreImage))
	mux.HandleFunc("DELETE /api/admin/trash/{id}", app.RequireAdmin(app.PurgeImage))
	mux.HandleFunc("GET /api/admin/images", app.RequireAdmin(app.ListImages))
	mux.HandleFunc("DELETE /api/admin/images/{id}", app.RequireAdmin(app.DeleteImage))
	mux.HandleFunc("POST /api/admin/images/bulk", app.RequireAdmin(app.BulkImages))
	mux.HandleFunc("GET /api/admin/users", app.RequireAdmin(app.AdminUsers))
	mux.HandleFunc("POST /api/admin/users", app.RequireAdmin(app.AdminUsers))
	mux.HandleFunc("PUT /api/admin/users/{id}", app.RequireAdmin(app.AdminUser))
	mux.HandleFunc("DELETE /api/admin/users/{id}", app.RequireAdmin(app.AdminUser))
	mux.HandleFunc("GET /api/user/images", app.RequireLogin(app.ListImages))
	mux.HandleFunc("DELETE /api/user/images/{id}", app.RequireLogin(app.DeleteImage))
	mux.HandleFunc("POST /api/user/images/bulk", app.RequireLogin(app.BulkImages))

	mux.HandleFunc("GET /i/", app.ServeImage)
	mux.HandleFunc("GET /t/{size}/{name}", app.ServeThumbnail)
	assets := http.StripPrefix("/assets/", http.FileServer(http.Dir("static/assets")))
	mux.Handle("/assets/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !utils.ServeSiteAsset(cfg.AssetDir, w, r) {
			assets.ServeHTTP(w, r)
		}
	}))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/i/") {
			utils.WriteError(w, http.StatusNotFound, "not found")
			return
		}
		path := "/" + strings.Trim(r.URL.Path, "/")
		if path == "/" {
			http.ServeFile(w, r, "static/index.html")
			return
		}
		adminPath := utils.GetSetting(db, "admin_path", "/admin")
		userPath := utils.GetSetting(db, "user_path", "/dashboard")
		if path == adminPath || path == adminPath+"/" {
			http.ServeFile(w, r, "static/admin.html")
			return
		}
		if path == userPath || path == userPath+"/" || path == "/dashboard" || path == "/dashboard/" {
			http.ServeFile(w, r, "static/dashboard.html")
			return
		}
		http.ServeFile(w, r, "static/index.html")
	})

	return securityHeaders(requestLimits(cors(db, cfg, mux)), cfg.TrustedProxies)
}

func cors(db *sql.DB, cfg *config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if !originAllowed(cfg, origin, utils.GetSetting(db, "site_domains", "")) {
				utils.WriteError(w, http.StatusForbidden, "origin not allowed")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func originAllowed(cfg *config.Config, origin, siteDomains string) bool {
	parsed, err := url.Parse(strings.TrimSpace(origin))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return false
	}
	allowed := map[string]bool{}
	if base, err := url.Parse(strings.TrimSpace(cfg.BaseURL)); err == nil && base.Host != "" {
		allowed[strings.ToLower(base.Host)] = true
	}
	for _, domain := range strings.Split(siteDomains, ",") {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain != "" {
			allowed[domain] = true
		}
	}
	return allowed[strings.ToLower(parsed.Host)]
}

func securityHeaders(next http.Handler, proxies ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob: https:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		trusted := ""
		if len(proxies) > 0 {
			trusted = proxies[0]
		}
		if utils.RequestScheme(r, trusted) == "https" {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func requestLimits(next http.Handler) http.Handler {
	uploads := make(chan struct{}, 4)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" || r.Method == "PUT" || r.Method == "DELETE" {
			timeout := 30 * time.Second
			if r.URL.Path == "/api/upload" {
				select {
				case uploads <- struct{}{}:
					defer func() { <-uploads }()
				default:
					utils.WriteError(w, 503, "upload capacity reached; retry later")
					return
				}
				timeout = 15 * time.Minute
			} else if !strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
				r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			}
			controller := http.NewResponseController(w)
			_ = controller.SetReadDeadline(time.Now().Add(timeout))
			defer controller.SetReadDeadline(time.Time{})
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}
