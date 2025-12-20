package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"podmanview/internal/auth"
	"podmanview/internal/config"
	"podmanview/internal/events"
	"podmanview/internal/podman"
	"podmanview/internal/plugins"
	"podmanview/internal/storage"
	"podmanview/internal/updater"
)

// Server represents the API server
type Server struct {
	router         *chi.Mux
	podmanClient   *podman.Client
	pamAuth        *auth.PAMAuth
	jwtManager     *auth.JWTManager
	authMw         *auth.Middleware
	wsTokenStore   *auth.WSTokenStore
	eventStore     *events.Store
	config         *config.Config
	updater        *updater.Updater
	historyHandler *HistoryHandler
	plugins        []plugins.Plugin
	pluginRegistry *plugins.Registry
	storage        storage.Storage
	version        string
	staticVersion  string
}

// NewServer creates new API server without plugins
func NewServer(podmanClient *podman.Client, cfg *config.Config, version, staticVersion string) *Server {
	return NewServerWithPlugins(podmanClient, cfg, version, staticVersion, nil, nil, nil)
}

// NewServerWithPlugins creates new API server with plugins
func NewServerWithPlugins(podmanClient *podman.Client, cfg *config.Config, version, staticVersion string, pluginList []plugins.Plugin, registry *plugins.Registry, pluginStorage storage.Storage) *Server {
	pamAuth := auth.NewPAMAuth()
	jwtManager := auth.NewJWTManager(cfg.JWTSecret(), cfg.JWTExpiration())
	authMw := auth.NewMiddleware(jwtManager)
	wsTokenStore := auth.NewWSTokenStore()
	eventStore := events.NewStore(100) // Keep last 100 events in memory

	// Get working directory for updater
	workDir, err := os.Getwd()
	if err != nil {
		log.Printf("Warning: failed to get working directory: %v", err)
		workDir = "."
	}

	// Create updater
	upd, err := updater.New(version, workDir)
	if err != nil {
		log.Printf("Warning: failed to create updater: %v", err)
	}

	// Create history handler (store history in database)
	historyHandler := NewHistoryHandler(pluginStorage)

	s := &Server{
		router:         chi.NewRouter(),
		podmanClient:   podmanClient,
		pamAuth:        pamAuth,
		jwtManager:     jwtManager,
		authMw:         authMw,
		wsTokenStore:   wsTokenStore,
		eventStore:     eventStore,
		config:         cfg,
		updater:        upd,
		historyHandler: historyHandler,
		plugins:        pluginList,
		pluginRegistry: registry,
		storage:        pluginStorage,
		version:        version,
		staticVersion:  staticVersion,
	}

	s.setupRoutes()
	return s
}

// setupRoutes configures all routes
func (s *Server) setupRoutes() {
	r := s.router

	// Middleware
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))

	// Create handlers
	authHandler := NewAuthHandler(s.pamAuth, s.jwtManager, s.wsTokenStore, s.eventStore)
	containerHandler := NewContainerHandler(s.podmanClient, s.eventStore)
	imageHandler := NewImageHandler(s.podmanClient, s.eventStore)
	systemHandler := NewSystemHandler(s.podmanClient, s.eventStore)
	terminalHandler := NewTerminalHandler(s.podmanClient, s.wsTokenStore, s.eventStore, s.historyHandler)
	eventsHandler := NewEventsHandler(s.eventStore)
	updateHandler := NewUpdateHandler(s.updater, s.eventStore)
	fileManagerHandler := NewFileManagerHandler(s.eventStore, "")  // Empty baseDir means use home dir
	pluginHandler := NewPluginHandler(s)

	// Public routes
	r.Post("/api/auth/login", authHandler.Login)

	// Protected API routes
	r.Group(func(r chi.Router) {
		// Apply auth middleware only if NoAuth is false
		if !s.config.NoAuth() {
			r.Use(s.authMw.RequireAuth)
		} else {
			// In no-auth mode, inject a fake admin user
			r.Use(s.fakeAuthMiddleware)
		}

		// Auth
		r.Post("/api/auth/logout", authHandler.Logout)
		r.Get("/api/auth/me", authHandler.Me)
		r.Get("/api/auth/ws-token", authHandler.WSToken)

		// Events
		r.Get("/api/events", eventsHandler.List)

		// Containers
		r.Get("/api/containers", containerHandler.List)
		r.Post("/api/containers", containerHandler.Create)
		r.Get("/api/containers/{id}", containerHandler.Inspect)
		r.Get("/api/containers/{id}/logs", containerHandler.Logs)
		r.Post("/api/containers/{id}/start", containerHandler.Start)
		r.Post("/api/containers/{id}/stop", containerHandler.Stop)
		r.Post("/api/containers/{id}/restart", containerHandler.Restart)
		r.Delete("/api/containers/{id}", containerHandler.Remove)

		// Terminal (WebSocket) - history is sent via WebSocket
		r.Get("/api/containers/{id}/terminal", terminalHandler.Connect)
		r.Get("/api/terminal", terminalHandler.HostTerminal)

		// Images
		r.Get("/api/images", imageHandler.List)
		r.Get("/api/images/{id}", imageHandler.Inspect)
		r.Post("/api/images/pull", imageHandler.Pull)
		r.Delete("/api/images/{id}", imageHandler.Remove)

		// System
		r.Get("/api/system/dashboard", systemHandler.Dashboard)
		r.Get("/api/system/info", systemHandler.Info)
		r.Get("/api/system/df", systemHandler.DiskUsage)
		r.Post("/api/system/reboot", systemHandler.Reboot)
		r.Post("/api/system/shutdown", systemHandler.Shutdown)

		// Updates
		r.Get("/api/system/version", updateHandler.Version)
		r.Get("/api/system/update/check", updateHandler.Check)
		r.Get("/api/system/update/status", updateHandler.Status)
		r.Post("/api/system/update", updateHandler.Perform)

		// File Manager
		r.Get("/api/files/browse", fileManagerHandler.Browse)
		r.Get("/api/files/download", fileManagerHandler.Download)
		r.Get("/api/files/stream", fileManagerHandler.StreamFile) // New: streaming endpoint for large files
		r.Post("/api/files/upload", fileManagerHandler.Upload)
		r.Delete("/api/files", fileManagerHandler.Delete)
		r.Post("/api/files/mkdir", fileManagerHandler.MkDir)
		r.Post("/api/files/create", fileManagerHandler.CreateFile)
		r.Post("/api/files/rename", fileManagerHandler.Rename)
		r.Get("/api/files/read", fileManagerHandler.ReadFile)
		r.Post("/api/files/write", fileManagerHandler.WriteFile)

		// Plugins Management
		r.Get("/api/plugins", pluginHandler.List)
		r.Get("/api/plugins/{name}", pluginHandler.Get)
		r.Get("/api/plugins/{name}/html", pluginHandler.GetHTML)
		r.Post("/api/plugins/{name}/toggle", pluginHandler.Toggle)
	})

	// Register plugin routes
	s.registerPluginRoutes(r)

	// Static files and SPA
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	// Serve index.html for all other routes (SPA)
	r.Get("/*", s.serveIndex)
}

// pluginEnabledMiddleware checks if a plugin is enabled
func (s *Server) pluginEnabledMiddleware(pluginName string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for _, p := range s.plugins {
			if p.Name() == pluginName && p.IsEnabled() {
				next(w, r)
				return
			}
		}
		http.Error(w, "Plugin not enabled", http.StatusServiceUnavailable)
	}
}

// registerPluginRoutes registers routes for all plugins with middleware
func (s *Server) registerPluginRoutes(r chi.Router) {
	if s.plugins == nil || len(s.plugins) == 0 {
		return
	}

	for _, plugin := range s.plugins {
		routes := plugin.Routes()
		if routes == nil {
			continue
		}

		for _, route := range routes {
			handler := s.pluginEnabledMiddleware(plugin.Name(), route.Handler)

			if route.RequireAuth && !s.config.NoAuth() {
				authHandler := handler
				handler = func(w http.ResponseWriter, req *http.Request) {
					s.authMw.RequireAuth(http.HandlerFunc(authHandler)).ServeHTTP(w, req)
				}
			}

			switch route.Method {
			case "GET":
				r.Get(route.Path, handler)
			case "POST":
				r.Post(route.Path, handler)
			case "PUT":
				r.Put(route.Path, handler)
			case "PATCH":
				r.Patch(route.Path, handler)
			case "DELETE":
				r.Delete(route.Path, handler)
			}
		}
	}
}

// serveIndex serves the main HTML page with version placeholders replaced
func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	// Read the template file
	content, err := os.ReadFile("web/templates/index.html")
	if err != nil {
		http.Error(w, "Failed to load page", http.StatusInternalServerError)
		log.Printf("Error reading index.html: %v", err)
		return
	}

	// Replace placeholders
	html := string(content)
	html = strings.ReplaceAll(html, "{{VERSION}}", s.version)
	html = strings.ReplaceAll(html, "{{STATIC_VERSION}}", s.staticVersion)

	// Set content type and write response
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(html))
}

// Router returns the chi router
func (s *Server) Router() *chi.Mux {
	return s.router
}

// writeJSON writes JSON response
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// fakeAuthMiddleware injects a fake admin user for no-auth mode
func (s *Server) fakeAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fakeUser := &auth.User{
			Username: "dev",
			UID:      "0",
			Role:     auth.RoleAdmin,
		}
		ctx := r.Context()
		ctx = auth.SetUserContext(ctx, fakeUser)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
