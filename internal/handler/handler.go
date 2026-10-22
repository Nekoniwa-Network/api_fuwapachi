package handler

import (
	"database/sql"
	"sync"

	"github.com/gorilla/mux"

	"fuwapachi/internal/config"
	"fuwapachi/internal/middleware"
	"fuwapachi/internal/model"
)

// Handler holds application dependencies
type Handler struct {
	DB        *sql.DB
	Config    config.Config
	Clients   map[*wsClient]struct{}
	ClientMu  sync.RWMutex
	Broadcast chan model.DeleteEventMessage

	ws      wsSettings
	wsSlots chan struct{} // 同時接続数の上限を数えるセマフォ

	wsPerIP    map[string]int // 接続元ごとの接続数（ClientMu で守る）
	wsPerIPMax int
}

// New creates a new Handler with the given dependencies
func New(db *sql.DB, cfg config.Config) *Handler {
	maxConns := cfg.WSMaxConnections
	if maxConns <= 0 {
		maxConns = config.DefaultWSMaxConnections
	}
	perIPMax := cfg.WSMaxConnectionsPerIP
	if perIPMax <= 0 {
		perIPMax = config.DefaultWSMaxConnectionsPerIP
	}
	return &Handler{
		DB:         db,
		Config:     cfg,
		Clients:    make(map[*wsClient]struct{}),
		Broadcast:  make(chan model.DeleteEventMessage, 100),
		ws:         defaultWSSettings(),
		wsSlots:    make(chan struct{}, maxConns),
		wsPerIP:    make(map[string]int),
		wsPerIPMax: perIPMax,
	}
}

// SetupRouter configures and returns the HTTP router
func (h *Handler) SetupRouter() *mux.Router {
	r := mux.NewRouter()

	// REST API
	r.HandleFunc("/messages", h.GetMessages).Methods("GET")

	// Create a subrouter for POST and DELETE to apply rate limiting (e.g. 1 req/sec, burst 5)
	postRouter := r.Methods("POST").Subrouter()
	postRouter.HandleFunc("/messages", h.CreateMessage)

	deleteRouter := r.Methods("DELETE").Subrouter()
	deleteRouter.HandleFunc("/messages/{id}", h.DeleteMessage)

	rl := middleware.NewRateLimiterWithConfig(h.Config.TrustedProxies, middleware.DefaultMaxClients)
	// limit to 1 request per second with a burst of 5 per IP
	postRouter.Use(rl.Limit(1, 5))
	deleteRouter.Use(rl.Limit(1, 5))

	// WebSocket
	r.HandleFunc("/ws", h.HandleWebSocket).Methods("GET")

	return r
}
