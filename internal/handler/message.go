package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/mux"

	"fuwapachi/internal/model"
)

// CreateMessage handles POST /messages
func (h *Handler) CreateMessage(w http.ResponseWriter, r *http.Request) {
	log.Printf("[POST /messages] Request received from %s", r.RemoteAddr)

	// リクエストボディサイズを1MBに制限
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var msg model.Message
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		log.Printf("[POST /messages] ❌ Bad Request: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request body"})
		return
	}

	// Content is required for message creation
	if msg.Content == "" {
		log.Printf("[POST /messages] ❌ Bad Request: missing or empty content")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "content is required"})
		return
	}

	// Set server-side controlled fields
	msg.CreatedAt = time.Now()
	msg.DeletedAt = nil

	// Insert message into database with AUTO_INCREMENT id
	result, err := h.DB.Exec("INSERT INTO messages (content, created_at, deleted_at) VALUES (?, ?, ?)",
		msg.Content, msg.CreatedAt, msg.DeletedAt)
	if err != nil {
		log.Printf("[POST /messages] ❌ Database error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create message"})
		return
	}

	// Get the auto-generated id
	lastInsertID, err := result.LastInsertId()
	if err != nil {
		log.Printf("[POST /messages] ❌ Database error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to retrieve message id"})
		return
	}

	msg.ID = fmt.Sprintf("%d", lastInsertID)

	log.Printf("[POST /messages] ✅ Created message: ID=%s, Content=%q", msg.ID, msg.Content)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(msg)
}

// maxMessagesPerRequest は1回のGETで返す最大レコード数
const maxMessagesPerRequest = 10

func (h *Handler) isOriginAllowed(origin string) bool {
	for _, allowed := range h.Config.AllowedOrigins {
		if origin == allowed {
			return true
		}
	}

	return false
}

// GetMessages handles GET /messages
// 削除されていないレコードからランダムに最大10件を返す
func (h *Handler) GetMessages(w http.ResponseWriter, r *http.Request) {
	log.Printf("[GET /messages] Request received from %s", r.RemoteAddr)

	origin := r.Header.Get("Origin")
	if origin != "" {
		if !h.isOriginAllowed(origin) {
			log.Printf("[GET /messages] ❌ Forbidden origin: %s", origin)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "Forbidden"})
			return
		}
	} else {
		referer := r.Referer()
		if referer == "" {
			log.Printf("[GET /messages] ❌ Missing Origin and Referer")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "Forbidden"})
			return
		}

		parsed, err := url.Parse(referer)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			log.Printf("[GET /messages] ❌ Invalid Referer: %s", referer)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "Forbidden"})
			return
		}

		refererOrigin := fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
		if !h.isOriginAllowed(refererOrigin) {
			log.Printf("[GET /messages] ❌ Forbidden referer origin: %s", refererOrigin)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "Forbidden"})
			return
		}
	}

	// deleted_at IS NULL で未削除のみ対象、ORDER BY RAND() でランダム10件
	rows, err := h.DB.Query(
		"SELECT id, content, created_at FROM messages WHERE deleted_at IS NULL ORDER BY RAND() LIMIT ?",
		maxMessagesPerRequest,
	)
	if err != nil {
		log.Printf("[GET /messages] ❌ Database error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Database error"})
		return
	}
	defer rows.Close()

	var msgList []model.Message
	for rows.Next() {
		var msg model.Message
		if err := rows.Scan(&msg.ID, &msg.Content, &msg.CreatedAt); err != nil {
			log.Printf("[GET /messages] ❌ Scan error: %v", err)
			continue
		}
		msgList = append(msgList, msg)
	}

	if err := rows.Err(); err != nil {
		log.Printf("[GET /messages] ❌ Rows iteration error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Database error"})
		return
	}

	if msgList == nil {
		msgList = []model.Message{}
	}

	log.Printf("[GET /messages] ✅ Returned %d messages (random selection)", len(msgList))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(msgList)
}

// DeleteMessage handles DELETE /messages/{id}
func (h *Handler) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	log.Printf("[DELETE /messages/%s] Request received from %s", id, r.RemoteAddr)

	// Check if message exists and is not already deleted
	var exists bool
	err := h.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM messages WHERE id = ? AND deleted_at IS NULL)", id).Scan(&exists)
	if err != nil {
		log.Printf("[DELETE /messages/%s] ❌ Database error: %v", id, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Database error"})
		return
	}

	if !exists {
		log.Printf("[DELETE /messages/%s] ❌ Not Found", id)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Message not found"})
		return
	}

	// Update deleted_at timestamp
	now := time.Now()
	_, err = h.DB.Exec("UPDATE messages SET deleted_at = ? WHERE id = ?", now, id)
	if err != nil {
		log.Printf("[DELETE /messages/%s] ❌ Database error: %v", id, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to delete message"})
		return
	}

	log.Printf("[DELETE /messages/%s] ✅ Deleted successfully", id)

	// WebSocket経由で他のクライアントに削除を通知
	h.Broadcast <- model.DeleteEventMessage{
		Type:      "message_deleted",
		ID:        id,
		DeletedAt: now,
	}
	log.Printf("[WebSocket] 📢 Broadcasting delete event for message: %s", id)

	w.WriteHeader(http.StatusNoContent)
}
