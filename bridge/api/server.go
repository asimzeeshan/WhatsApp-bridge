package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/asimzeeshan/WhatsApp-bridge/bridge/config"
	"github.com/asimzeeshan/WhatsApp-bridge/bridge/connection"
	"github.com/asimzeeshan/WhatsApp-bridge/bridge/store"
)

// Server is the HTTP API server for the WhatsApp bridge.
type Server struct {
	router    chi.Router
	db        *store.DB
	connMgr   *connection.Manager
	cfg       *config.Config
	startedAt time.Time
	logger    *slog.Logger
	httpSrv   *http.Server
}

func NewServer(db *store.DB, connMgr *connection.Manager, cfg *config.Config, logger *slog.Logger) *Server {
	s := &Server{
		db:        db,
		connMgr:   connMgr,
		cfg:       cfg,
		startedAt: time.Now(),
		logger:    logger,
	}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(s.logMiddleware)

	rl := NewRateLimiter(
		cfg.Bridge.RateLimit.MessagesPerSecond,
		cfg.Bridge.RateLimit.Burst,
		cfg.Bridge.RateLimit.JitterMs,
	)

	r.Route("/api", func(r chi.Router) {
		r.Get("/status", s.handleStatus)
		r.Get("/health", s.handleHealth)

		// Messages
		r.Get("/messages", s.handleListMessages)
		r.Get("/messages/{id}/context", s.handleMessageContext)
		r.Get("/check", s.handleCheckNewMessages)

		// Chats
		r.Get("/chats", s.handleListChats)
		r.Get("/chats/{jid}", s.handleGetChat)
		r.Get("/unread", s.handleUnread)

		// Contacts
		r.Get("/contacts", s.handleListContacts)
		r.Get("/contacts/{jid}", s.handleGetContact)

		// Groups
		r.Get("/groups", s.handleListGroups)
		r.Get("/groups/{jid}", s.handleGetGroup)

		// Send (rate-limited)
		r.With(rl.Middleware).Post("/send", s.handleSendMessage)
		r.With(rl.Middleware).Post("/send/media", s.handleSendMedia)
		r.With(rl.Middleware).Post("/send/reaction", s.handleSendReaction)
		r.With(rl.Middleware).Post("/send/edit", s.handleEditMessage)
		r.With(rl.Middleware).Post("/send/revoke", s.handleRevokeMessage)

		// Download
		r.Post("/download", s.handleDownload)

		// Links
		r.Get("/links", s.handleListLinks)

		// Telemetry
		r.Get("/telemetry/daily", s.handleTelemetryDaily)
		r.Get("/telemetry/tools", s.handleTelemetryTools)
		r.Post("/telemetry/tool", s.handleRecordToolCall)

		// Daily summary (stub)
		r.Get("/daily-summary", s.handleDailySummary)
	})

	s.router = r
	return s
}

func (s *Server) logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		s.logger.Debug("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"duration", time.Since(start).String(),
		)
	})
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	s.httpSrv = &http.Server{
		Addr:    s.cfg.Bridge.Addr,
		Handler: s.router,
	}
	s.logger.Info("API server starting", "addr", s.cfg.Bridge.Addr)
	return s.httpSrv.ListenAndServe()
}

// Shutdown gracefully shuts down the HTTP server with a 15-second timeout.
func (s *Server) Shutdown() error {
	if s.httpSrv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return s.httpSrv.Shutdown(ctx)
}

// requireConnected returns false and writes an error if WhatsApp is not connected.
func (s *Server) requireConnected(w http.ResponseWriter) bool {
	if !s.connMgr.State.IsConnected() || s.connMgr.Client == nil {
		writeError(w, http.StatusServiceUnavailable, "NOT_CONNECTED", "WhatsApp is not connected")
		return false
	}
	return true
}
