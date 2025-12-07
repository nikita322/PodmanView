package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"podmanview/internal/auth"
	"podmanview/internal/config"
	"podmanview/internal/events"
	"podmanview/internal/podman"
)

// Server represents the API server
type Server struct {
	router       *chi.Mux
	podmanClient *podman.Client
	pamAuth      *auth.PAMAuth
	jwtManager   *auth.JWTManager
	authMw       *auth.Middleware
	wsTokenStore *auth.WSTokenStore
	eventStore   *events.Store
	config       *config.Config
}

// NewServer creates new API server
func NewServer(podmanClient *podman.Client, cfg *config.Config) *Server {
	pamAuth := auth.NewPAMAuth()
	jwtManager := auth.NewJWTManager(cfg.JWTSecret(), cfg.JWTExpiration())
	authMw := auth.NewMiddleware(jwtManager)
	wsTokenStore := auth.NewWSTokenStore()
	eventStore := events.NewStore(100) // Keep last 100 events in memory

	s := &Server{
		router:       chi.NewRouter(),
		podmanClient: podmanClient,
		pamAuth:      pamAuth,
		jwtManager:   jwtManager,
		authMw:       authMw,
		wsTokenStore: wsTokenStore,
		eventStore:   eventStore,
		config:       cfg,
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
	terminalHandler := NewTerminalHandler(s.podmanClient, s.wsTokenStore, s.eventStore)
	eventsHandler := NewEventsHandler(s.eventStore)

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

		// Terminal (WebSocket)
		r.Get("/api/containers/{id}/terminal", terminalHandler.Connect)
		r.Post("/api/containers/{id}/exec", terminalHandler.SimpleTerminal)
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
		r.Post("/api/system/prune", systemHandler.Prune)
		r.Post("/api/system/reboot", systemHandler.Reboot)
		r.Post("/api/system/shutdown", systemHandler.Shutdown)
	})

	// Static files and SPA
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	// Serve index.html for all other routes (SPA)
	r.Get("/*", s.serveIndex)
}

// serveIndex serves the main HTML page
func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "web/templates/index.html")
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
