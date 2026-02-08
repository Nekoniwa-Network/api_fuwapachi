package model

import "time"

// Message represents a chat message
type Message struct {
	ID        string     `json:"id"`
	Content   string     `json:"content"`
	CreatedAt time.Time  `json:"created_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// DeleteEventMessage is used for WebSocket delete notifications
type DeleteEventMessage struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	DeletedAt time.Time `json:"deleted_at"`
}
