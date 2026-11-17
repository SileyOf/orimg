package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"zhuapic/config"
	"zhuapic/database"
	"zhuapic/installer"
	"zhuapic/mailer"
	"zhuapic/routes"
	"zhuapic/storage"
	"zhuapic/utils"
)

type liveHandler struct {
	mu      sync.RWMutex
	handler http.Handler
}

func (h *liveHandler) set(next http.Handler) { h.mu.Lock(); defer h.mu.Unlock(); h.handler = next }
func (h *liveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	next := h.handler
	h.mu.RUnlock()
	next.ServeHTTP(w, r)
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "-version" {
		fmt.Println(config.AppVersion)
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "-healthcheck" {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		if err := healthcheck(port); err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		log.Printf("Startup failed: %v", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()
	path := config.InstallationPath()
	state, err := config.ReadInstallation(path)
	if err != nil {
		return err
	}
	if state != nil {
		cfg = &state.Config
		if port := os.Getenv("PORT"); port != "" {
			cfg.Port = port
		}
		if proxies, ok := os.LookupEnv("TRUSTED_PROXIES"); ok {
			cfg.TrustedProxies = proxies
		}
		if disabled, ok := os.LookupEnv("DISABLE_BACKGROUND_WORK"); ok {
			cfg.DisableBackgroundWork, err = strconv.ParseBool(disabled)
			if err != nil {
				return err
			}
		}
	}
	needsInstall := state != nil && !state.Installed || state == nil && !config.HasLegacyConfiguration()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()
	var workers sync.WaitGroup
	var activeDB *sql.DB
	var activation sync.Mutex
	live := &liveHandler{}
	activate := func(c *config.Config, db *sql.DB) {
		activation.Lock()
		defer activation.Unlock()
		if workerCtx.Err() != nil {
			db.Close()
			return
		}
		activeDB = db
		service := storage.NewService(db, c, storage.NewManager(c))
		live.set(routes.Setup(db, c, service))
		if !c.DisableBackgroundWork {
			start := func(fn func()) { workers.Add(1); go func() { defer workers.Done(); fn() }() }
			start(func() {
				storage.StartCacheJanitor(workerCtx, func() string { return utils.GetSetting(db, "s3_cache_dir", c.CacheDir) }, func() time.Duration { return utils.SettingCacheTTL(db, c.CacheTTL) }, c.CleanupInterval)
			})
			start(func() { service.Start(workerCtx) })
			start(func() { mailer.New(db, c.JWTSecret).Start(workerCtx) })
		}
	}
	if needsInstall {
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		defer lock.Close()
		if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			return errors.New("another setup process owns this configuration directory")
		}
		defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		latest, err := config.ReadInstallation(path)
		if err != nil {
			return err
		}
		if latest != nil && latest.Installed {
			return errors.New("installation was completed by another process; restart this process")
		}
		state = latest
		token, err := utils.RandomHex(24)
		if err != nil {
			return err
		}
		setup := &installer.Server{Base: cfg, Path: path, Pending: state, Token: token, Activate: activate}
		live.set(setup.Handler())
		log.Printf("Installation required at /install. One-time setup code: %s", token)
	} else {
		if err = cfg.ValidateConnection(); err != nil {
			return err
		}
		if err = utils.PreserveSiteAssets(cfg.AssetDir); err != nil {
			return err
		}
		db, err := database.Open(ctx, cfg)
		if err != nil {
			return errors.New("database connection failed; check database credentials, TLS and network")
		}
		info, err := database.Inspect(ctx, db)
		if err != nil {
			db.Close()
			return err
		}
		if err = database.PrepareRuntimeSecurity(ctx, db, cfg, info.Empty, path); err != nil {
			db.Close()
			return err
		}
		if config.ValidateSecret(cfg.JWTSecret) != nil {
			log.Printf("Legacy encryption key preserved. Independent session key loaded from %s; back up this private file. Old sessions must sign in again.", config.SessionKeyPath(path))
		}
		if err = database.Initialize(ctx, db, cfg, info.Empty); err != nil {
			db.Close()
			return err
		}
		activate(cfg, db)
	}
	server := &http.Server{Addr: ":" + cfg.Port, Handler: live, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	log.Printf("Origin %s listening on :%s", config.AppVersion, cfg.Port)
	select {
	case <-ctx.Done():
	case err = <-serveErr:
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	activation.Lock()
	cancelWorkers()
	activation.Unlock()
	if shutdownErr := server.Shutdown(shutdown); shutdownErr != nil {
		_ = server.Close()
	}
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-shutdown.Done():
	}
	activation.Lock()
	if activeDB != nil {
		activeDB.Close()
	}
	activation.Unlock()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
