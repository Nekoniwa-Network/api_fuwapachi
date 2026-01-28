package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	"github.com/rs/cors"
)

// Config holds application configuration
type Config struct {
	// MariaDB接続設定
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string

	// サーバー設定
	ServerPort string
	Env        string

	// CORS設定
	AllowedOrigins []string
}

// LoadConfig loads configuration from environment variables
func LoadConfig() Config {
	// MariaDB接続設定
	dbHost := os.Getenv("DB_HOST")
	if dbHost == "" {
		dbHost = "localhost"
	}

	dbPort := os.Getenv("DB_PORT")
	if dbPort == "" {
		dbPort = "3306"
	}

	dbUser := os.Getenv("DB_USER")
	dbPassword := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")

	// サーバー設定
	serverPort := os.Getenv("SERVER_PORT")
	if serverPort == "" {
		serverPort = "8080"
	}

	env := os.Getenv("ENV")
	if env == "" {
		env = "development"
	}

	// CORS設定
	allowedOrigins := os.Getenv("ALLOWED_ORIGINS")
	if allowedOrigins == "" {
		allowedOrigins = "http://localhost:3000,http://127.0.0.1:3000"
	}

	config := Config{
		DBHost:         dbHost,
		DBPort:         dbPort,
		DBUser:         dbUser,
		DBPassword:     dbPassword,
		DBName:         dbName,
		ServerPort:     serverPort,
		Env:            env,
		AllowedOrigins: strings.Split(allowedOrigins, ","),
	}

	// Trim spaces from allowed origins
	for i := range config.AllowedOrigins {
		config.AllowedOrigins[i] = strings.TrimSpace(config.AllowedOrigins[i])
	}

	return config
}

// InitDB initializes database connection
func InitDB(config Config) (*sql.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
		config.DBUser,
		config.DBPassword,
		config.DBHost,
		config.DBPort,
		config.DBName,
	)

	print(dsn)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// 接続テスト
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	log.Println("✅ Database connection established")
	return db, nil
}

// Message struct
type Message struct {
	ID        string     `json:"id"`
	UID       string     `json:"uid,omitempty"`
	Content   string     `json:"content"`
	CreatedAt time.Time  `json:"created_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// DeleteEventMessage WebSocket通知用
type DeleteEventMessage struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	DeletedAt time.Time `json:"deleted_at"`
}

// データベース接続
var (
	db *sql.DB
)

// WebSocket接続管理
var (
	clients   = make(map[*websocket.Conn]bool)
	clientMu  sync.RWMutex
	broadcast = make(chan DeleteEventMessage, 100) // バッファ化してDELETE リクエストのブロッキングを回避
	config    Config
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

// CreateMessage: POST /messages
func CreateMessage(w http.ResponseWriter, r *http.Request) {
	log.Printf("[POST /messages] Request received from %s", r.RemoteAddr)

	var msg Message
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
	result, err := db.Exec("INSERT INTO messages (content, created_at, deleted_at) VALUES (?, ?, ?)",
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

// GetMessages: GET /messages
func GetMessages(w http.ResponseWriter, r *http.Request) {
	log.Printf("[GET /messages] Request received from %s", r.RemoteAddr)

	rows, err := db.Query("SELECT id, content, created_at, deleted_at FROM messages")
	if err != nil {
		log.Printf("[GET /messages] ❌ Database error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Database error"})
		return
	}
	defer rows.Close()

	var msgList []Message
	for rows.Next() {
		var msg Message
		var deletedAt sql.NullTime
		if err := rows.Scan(&msg.ID, &msg.Content, &msg.CreatedAt, &deletedAt); err != nil {
			log.Printf("[GET /messages] ❌ Scan error: %v", err)
			continue
		}
		if deletedAt.Valid {
			msg.DeletedAt = &deletedAt.Time
		}
		msgList = append(msgList, msg)
	}

	if msgList == nil {
		msgList = []Message{}
	}

	log.Printf("[GET /messages] ✅ Returned %d messages", len(msgList))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(msgList)
}

// DeleteMessage: DELETE /messages/{id}
func DeleteMessage(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	log.Printf("[DELETE /messages/%s] Request received from %s", id, r.RemoteAddr)

	// Check if message exists
	var exists bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM messages WHERE id = ?)", id).Scan(&exists)
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
	_, err = db.Exec("UPDATE messages SET deleted_at = ? WHERE id = ?", now, id)
	if err != nil {
		log.Printf("[DELETE /messages/%s] ❌ Database error: %v", id, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to delete message"})
		return
	}

	log.Printf("[DELETE /messages/%s] ✅ Deleted successfully", id)

	// WebSocket経由で他のクライアントに削除を通知
	broadcast <- DeleteEventMessage{
		Type:      "message_deleted",
		ID:        id,
		DeletedAt: now,
	}
	log.Printf("[WebSocket] 📢 Broadcasting delete event for message: %s", id)

	w.WriteHeader(http.StatusNoContent)
}

// WebSocket: GET /ws
func HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	upgrader := createUpgrader(config.AllowedOrigins)
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	clientMu.Lock()
	clients[conn] = true
	totalClients := len(clients)
	clientMu.Unlock()

	log.Printf("New WebSocket connection. Total clients: %d", totalClients)

	// クライアントからのメッセージを受信（キープアライブ用）
	for {
		var msg interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			clientMu.Lock()
			delete(clients, conn)
			remainingClients := len(clients)
			clientMu.Unlock()
			log.Printf("[WebSocket] Client disconnected. Total clients: %d", remainingClients)
			break
		}
	}
}

// WebSocket ブロードキャスター
func handleBroadcast() {
	for event := range broadcast {
		// clients マップをスナップショットしてからロックを外すことで、
		// range 中に delete して "concurrent map iteration and map write"
		// が発生するのを防ぐ
		clientMu.RLock()
		clientsSnapshot := make([]*websocket.Conn, 0, len(clients))
		for client := range clients {
			clientsSnapshot = append(clientsSnapshot, client)
		}
		clientMu.RUnlock()

		for _, client := range clientsSnapshot {
			if err := client.WriteJSON(event); err != nil {
				client.Close()
				// WriteJSON 失敗時のみ、クライアントを削除するために
				// 書き込みロックを取得する
				clientMu.Lock()
				delete(clients, client)
				clientMu.Unlock()
			}
		}
	}
}

// SetupRouter: ルーター設定
func SetupRouter() *mux.Router {
	r := mux.NewRouter()

	// REST API
	r.HandleFunc("/messages", GetMessages).Methods("GET")
	r.HandleFunc("/messages", CreateMessage).Methods("POST")
	r.HandleFunc("/messages/{id}", DeleteMessage).Methods("DELETE")

	// WebSocket
	r.HandleFunc("/ws", HandleWebSocket).Methods("GET")

	return r
}

func main() {
	// .envファイルを読み込み
	if err := godotenv.Load(); err != nil {
		log.Printf("⚠️  .env file not found, using default values: %v", err)
	}

	// 環境変数を読み込み
	config = LoadConfig()

	// データベース接続を初期化
	var err error
	db, err = InitDB(config)
	if err != nil {
		log.Fatalf("❌ Failed to initialize database: %v", err)
	}
	defer db.Close()

	// WebSocket ブロードキャスターを開始
	go handleBroadcast()

	r := SetupRouter()

	// CORS対応
	c := cors.New(cors.Options{
		AllowedOrigins:   config.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "DELETE", "OPTIONS", "PUT"},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		ExposedHeaders:   []string{"Content-Length"},
		MaxAge:           300,
		AllowCredentials: true,
	})

	handler := c.Handler(r)

	fmt.Println("========================================")
	fmt.Println("  Fuwapachi API Server")
	fmt.Println("========================================")
	fmt.Printf("  Environment: %s\n", config.Env)
	fmt.Printf("  Server: http://localhost:%s\n", config.ServerPort)
	fmt.Printf("  WebSocket: ws://localhost:%s/ws\n", config.ServerPort)
	if config.DBName != "" {
		fmt.Printf("  Database: %s@%s:%s/%s\n", config.DBUser, config.DBHost, config.DBPort, config.DBName)
	}
	fmt.Printf("  Allowed Origins: %v\n", config.AllowedOrigins)
	fmt.Println("========================================")
	log.Println("🚀 Server started successfully")
	log.Fatal(http.ListenAndServe(":"+config.ServerPort, handler))
}
