package handler

import (
	"database/sql"
	"sync"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"fuwapachi/internal/config"
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
	r.HandleFunc("/messages", h.CreateMessage).Methods("POST")
	r.HandleFunc("/messages/{id}", h.DeleteMessage).Methods("DELETE")

	// WebSocket
	r.HandleFunc("/ws", h.HandleWebSocket).Methods("GET")

	return r
}
