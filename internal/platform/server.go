package platform

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"adaptive-vocabulary/internal/bank"
	"adaptive-vocabulary/internal/content"
	"adaptive-vocabulary/internal/exam"
	"adaptive-vocabulary/internal/generation"
	"adaptive-vocabulary/internal/importer"
	"adaptive-vocabulary/internal/learning"
	"adaptive-vocabulary/internal/maintenance"
	"adaptive-vocabulary/internal/practice"
	"adaptive-vocabulary/internal/revision"
)

const (
	DefaultMaxBodyBytes    = 2 * 1024 * 1024
	DefaultReadTimeout     = 15 * time.Second
	DefaultWriteTimeout    = 15 * time.Second
	DefaultIdleTimeout     = 60 * time.Second
	DefaultShutdownTimeout = 5 * time.Second
)

type Server struct {
	Config        *Config
	DB            *DB
	Lock          *ProcessLock
	Security      *SecurityConfig
	ExamHandler   *exam.Handler
	FSRSPolicy    *learning.FSRSPolicyConfig
	SchemaVersion int
	BaseURL       string
	Listener      net.Listener
	HTTPServer    *http.Server
	closeOnce     sync.Once
}

func NewServer(cfg *Config) (*Server, error) {
	// 1. Acquire process lock
	lock, err := AcquireProcessLock(cfg.DataDir)
	if err != nil {
		return nil, err
	}

	// 2. Open SQLite database
	db, err := OpenDB(cfg.DataDir)
	if err != nil {
		_ = lock.Release()
		return nil, fmt.Errorf("open db: %w", err)
	}

	// 3. Run migrations
	schemaVersion, err := RunMigrations(db.DB)
	if err != nil {
		_ = db.Close()
		_ = lock.Release()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	// 4. Apply seed if configured
	if cfg.Seed != "" {
		if err := content.ApplySeed(context.Background(), db.DB, cfg.Seed); err != nil {
			_ = db.Close()
			_ = lock.Release()
			return nil, fmt.Errorf("apply seed: %w", err)
		}
	}

	security, err := LoadOrGenerateSecurityConfig(cfg.DataDir, cfg.WorkerToken)
	if err != nil {
		_ = db.Close()
		_ = lock.Release()
		return nil, fmt.Errorf("init security config: %w", err)
	}

	fsrsPolicy, _ := learning.LoadFSRSPolicyFromPath(cfg.PolicyPath)
	if fsrsPolicy == nil {
		candidates := []string{
			cfg.PolicyPath,
			filepath.Join("..", cfg.PolicyPath),
			filepath.Join("..", "..", cfg.PolicyPath),
		}
		for _, c := range candidates {
			if p, err2 := learning.LoadFSRSPolicyFromPath(c); err2 == nil {
				fsrsPolicy = p
				break
			}
		}
	}

	examHandler, err := exam.NewHandler(db.DB, cfg.PolicyPath)
	if err != nil {
		_ = db.Close()
		_ = lock.Release()
		return nil, fmt.Errorf("init exam handler: %w", err)
	}

	// Settle expired exams before reporting ready
	if _, err := examHandler.Service().SettleExpiredExams(context.Background()); err != nil {
		_ = db.Close()
		_ = lock.Release()
		return nil, fmt.Errorf("settle expired exams: %w", err)
	}

	return &Server{
		Config:        cfg,
		DB:            db,
		Lock:          lock,
		Security:      security,
		ExamHandler:   examHandler,
		FSRSPolicy:    fsrsPolicy,
		SchemaVersion: schemaVersion,
	}, nil
}

func (s *Server) Start() error {
	// Listen on configured address
	listener, err := net.Listen("tcp", s.Config.ListenAddr)
	if err != nil {
		_ = s.Close()
		return fmt.Errorf("listen on %s: %w", s.Config.ListenAddr, err)
	}
	s.Listener = listener

	addr := listener.Addr().(*net.TCPAddr)
	s.BaseURL = fmt.Sprintf("http://127.0.0.1:%d", addr.Port)

	// Set up mux
	mux := http.NewServeMux()
	home := &HomeHandler{
		DB:            s.DB,
		Config:        s.Config,
		Security:      s.Security,
		SchemaVersion: s.SchemaVersion,
	}

	webFrontend := NewWebFrontendHandler(home)

	mux.HandleFunc("/", webFrontend.ServeSPA)
	mux.HandleFunc("/client-context", home.ServeClientContext)
	mux.HandleFunc("/api/v1/health", home.ServeHealth)
	mux.HandleFunc("/api/v1/test/action", home.ServeTestPost)
	mux.HandleFunc("/api/v1/web-manifest", webFrontend.ServeManifest)

	assetsDir := filepath.Join(webFrontend.DistDir, "assets")
	if info, err := os.Stat(assetsDir); err == nil && info.IsDir() {
		mux.Handle("/assets/", webFrontend.ServeAssets())
	}

	shellStaticDir := findWebDir("web/static/shell")
	if info, err := os.Stat(shellStaticDir); err == nil && info.IsDir() {
		mux.Handle("/static/shell/", http.StripPrefix("/static/shell/", http.FileServer(http.Dir(shellStaticDir))))
	}

	// Bank and content routes
	bankHandler := bank.NewHandler(s.DB.DB)
	bankHandler.RegisterRoutes(mux)

	// Importer routes
	importerHandler := importer.NewHandler(s.DB.DB)
	importerHandler.RegisterRoutes(mux)

	// Practice routes
	practiceHandler, err := practice.NewHandler(s.DB.DB, s.Config.PolicyPath)
	if err != nil {
		_ = s.Close()
		return fmt.Errorf("init practice handler: %w", err)
	}
	practiceHandler.RegisterRoutes(mux)

	// Exam routes
	s.ExamHandler.RegisterRoutes(mux)

	// Generation routes
	genConfig, err := generation.LoadGenerationConfigFromPath(s.Config.PolicyPath)
	if err != nil {
		def := generation.DefaultGenerationConfig()
		genConfig = &def
	}
	generationHandler := generation.NewHandler(s.DB.DB, *genConfig, s.Config.WorkerToken)
	generationHandler.RegisterRoutes(mux)

	// Maintenance routes
	var maintService *maintenance.Service
	if s.FSRSPolicy != nil {
		maintService = maintenance.NewServiceWithPolicy(s.DB.DB, *s.FSRSPolicy)
	} else {
		maintService = maintenance.NewService(s.DB.DB)
	}
	maintHandler := maintenance.NewHandler(maintService)
	maintHandler.RegisterRoutes(mux)

	// Revision routes
	revService := revision.NewService(s.DB.DB, maintService)
	revHandler := revision.NewHandler(revService)
	revHandler.RegisterRoutes(mux)

	// Apply middleware stack in order
	var handler http.Handler = mux
	handler = MaintenanceGateMiddleware(s.DB.DB)(handler)
	handler = CSRFMiddleware(s.Security)(handler)
	handler = OriginCheckMiddleware(handler)
	handler = HostCheckMiddleware(handler)
	handler = MaxBytesMiddleware(DefaultMaxBodyBytes)(handler)
	handler = CacheControlMiddleware(handler)
	handler = RequestIDMiddleware(handler)

	s.HTTPServer = &http.Server{
		Handler:      handler,
		ReadTimeout:  DefaultReadTimeout,
		WriteTimeout: DefaultWriteTimeout,
		IdleTimeout:  DefaultIdleTimeout,
	}

	// Print single-line ready JSON to stdout
	readySig := ReadySignal{
		Ready:           true,
		BaseURL:         s.BaseURL,
		PID:             os.Getpid(),
		SchemaVersion:   s.SchemaVersion,
		ContractVersion: s.Config.ContractVersion,
		PolicyVersions: map[string]any{
			"policy":         s.Config.PolicyPath,
			"design_version": s.Config.Policy.DesignVersion,
		},
		DataDir: s.Config.DataDir,
	}
	if err := PrintReadySignal(readySig); err != nil {
		_ = s.Close()
		return err
	}

	// Handle graceful shutdown on OS signals
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, os.Interrupt, syscall.SIGTERM)

	serverErrCh := make(chan error, 1)
	go func() {
		if err := s.HTTPServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- err
		}
	}()

	select {
	case sig := <-stopCh:
		_ = sig
		return s.Close()
	case err := <-serverErrCh:
		_ = s.Close()
		return err
	}
}

func (s *Server) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		var errs []error
		if s.Listener != nil {
			if err := s.Listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				errs = append(errs, err)
			}
		}
		if s.HTTPServer != nil {
			ctx, cancel := context.WithTimeout(context.Background(), DefaultShutdownTimeout)
			defer cancel()
			if err := s.HTTPServer.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errs = append(errs, err)
			}
		}
		if s.DB != nil {
			if err := s.DB.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if s.Lock != nil {
			if err := s.Lock.Release(); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) > 0 {
			closeErr = fmt.Errorf("close server errors: %v", errs)
		}
	})
	return closeErr
}
