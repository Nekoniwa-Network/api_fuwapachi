package handler

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

// createUpgrader creates a WebSocket upgrader with the given allowed origins
func createUpgrader(allowedOrigins []string) websocket.Upgrader {
	allowedMap := make(map[string]bool)
	for _, origin := range allowedOrigins {
		allowedMap[origin] = true
	}

	return websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			return allowedMap[origin]
		},
	}
}

// HandleWebSocket handles GET /ws
func (h *Handler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	upgrader := createUpgrader(h.Config.AllowedOrigins)
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	h.ClientMu.Lock()
	h.Clients[conn] = true
	totalClients := len(h.Clients)
	h.ClientMu.Unlock()

	log.Printf("New WebSocket connection. Total clients: %d", totalClients)

	// クライアントからのメッセージを受信（キープアライブ用）
	for {
		var msg interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			h.ClientMu.Lock()
			delete(h.Clients, conn)
			remainingClients := len(h.Clients)
			h.ClientMu.Unlock()
			log.Printf("[WebSocket] Client disconnected. Total clients: %d", remainingClients)
			break
		}
	}
}

// HandleBroadcast broadcasts delete events to all connected WebSocket clients
func (h *Handler) HandleBroadcast() {
	for event := range h.Broadcast {
		// clients マップをスナップショットしてからロックを外すことで、
		// range 中に delete して "concurrent map iteration and map write"
		// が発生するのを防ぐ
		h.ClientMu.RLock()
		clientsSnapshot := make([]*websocket.Conn, 0, len(h.Clients))
		for client := range h.Clients {
			clientsSnapshot = append(clientsSnapshot, client)
		}
		h.ClientMu.RUnlock()

		for _, client := range clientsSnapshot {
			if err := client.WriteJSON(event); err != nil {
				client.Close()
				h.ClientMu.Lock()
				delete(h.Clients, client)
				h.ClientMu.Unlock()
			}
		}
	}
}
