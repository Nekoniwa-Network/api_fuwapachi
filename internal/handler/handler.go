package handler

import (
	"database/sql"
	"sync"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"fuwapachi/internal/config"
	"fuwapachi/internal/middleware"
	"fuwapachi/internal/model"
)

// Handler holds application dependencies
type Handler struct {
	DB        *sql.DB
	Config    config.Config
	Clients   map[*websocket.Conn]bool
	ClientMu  sync.RWMutex
	Broadcast chan model.DeleteEventMessage
}

// New creates a new Handler with the given dependencies
func New(db *sql.DB, cfg config.Config) *Handler {
	return &Handler{
		DB:        db,
		Config:    cfg,
		Clients:   make(map[*websocket.Conn]bool),
		Broadcast: make(chan model.DeleteEventMessage, 100),
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
	
	rl := middleware.NewRateLimiter()
	// limit to 1 request per second with a burst of 5 per IP
	postRouter.Use(rl.Limit(1, 5))
	deleteRouter.Use(rl.Limit(1, 5))

	// WebSocket
	r.HandleFunc("/ws", h.HandleWebSocket).Methods("GET")

	return r
}
