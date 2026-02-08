package handler

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"

	"fuwapachi/internal/config"
	"fuwapachi/internal/model"
)

func TestMain(m *testing.M) {
	// プロジェクトルートの.envを読み込み
	_ = godotenv.Load("../../.env")
	os.Exit(m.Run())
}

// setupTestDB テスト用データベース接続をセットアップ
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()

	host := os.Getenv("DB_HOST")
	if host == "" {
		t.Skip("Skipping: DB_HOST not set")
	}

	port := os.Getenv("DB_PORT")
	if port == "" {
		port = "3306"
	}

	user := os.Getenv("DB_USER")
	password := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true", user, password, host, port, dbName)

	testDB, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Skipf("Skipping: could not connect to test database: %v", err)
		return nil
	}

	if err := testDB.Ping(); err != nil {
		t.Skipf("Skipping: could not ping test database: %v", err)
		return nil
	}

	// AUTO_INCREMENT対応のテーブル作成
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS messages (
		id INT AUTO_INCREMENT PRIMARY KEY,
		content TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		deleted_at DATETIME NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
	`
	if _, err := testDB.Exec(createTableSQL); err != nil {
		t.Fatalf("Failed to create test table: %v", err)
	}

	// テストデータをクリア
	testDB.Exec("DELETE FROM messages")
	// AUTO_INCREMENTをリセット
	testDB.Exec("ALTER TABLE messages AUTO_INCREMENT = 1")

	return testDB
}

// cleanupTestDB テスト後のクリーンアップ
func cleanupTestDB(testDB *sql.DB) {
	if testDB != nil {
		testDB.Exec("DELETE FROM messages")
		testDB.Close()
	}
}

// newTestHandler テスト用のHandlerを生成
func newTestHandler(testDB *sql.DB) *Handler {
	return &Handler{
		DB: testDB,
		Config: config.Config{
			AllowedOrigins: []string{"http://localhost:8080", "http://127.0.0.1:8080"},
		},
		Clients:   make(map[*websocket.Conn]bool),
		Broadcast: make(chan model.DeleteEventMessage, 100),
	}
}

// TestCreateMessage_Success メッセージ作成成功テスト
func TestCreateMessage_Success(t *testing.T) {
	testDB := setupTestDB(t)
	defer cleanupTestDB(testDB)

	h := newTestHandler(testDB)
	router := h.SetupRouter()

	msgPayload := map[string]string{
		"content": "Hello, World!",
	}
	body, _ := json.Marshal(msgPayload)

	req := httptest.NewRequest("POST", "/messages", bytes.NewReader(body))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status %d, got %d. Body: %s", http.StatusCreated, w.Code, w.Body.String())
	}

	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Expected Content-Type: application/json, got %s", w.Header().Get("Content-Type"))
	}

	var responseMsg model.Message
	json.Unmarshal(w.Body.Bytes(), &responseMsg)

	if responseMsg.ID == "" {
		t.Error("Expected auto-generated ID, got empty string")
	}

	if responseMsg.Content != "Hello, World!" {
		t.Errorf("Expected content 'Hello, World!', got %q", responseMsg.Content)
	}

	if responseMsg.DeletedAt != nil {
		t.Error("DeletedAt should be nil for new message")
	}
}

// TestCreateMessage_MissingContent Content 必須チェック
func TestCreateMessage_MissingContent(t *testing.T) {
	testDB := setupTestDB(t)
	defer cleanupTestDB(testDB)

	h := newTestHandler(testDB)
	router := h.SetupRouter()

	msgPayload := map[string]string{
		"content": "",
	}
	body, _ := json.Marshal(msgPayload)

	req := httptest.NewRequest("POST", "/messages", bytes.NewReader(body))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var errResp map[string]string
	json.Unmarshal(w.Body.Bytes(), &errResp)
	if errResp["error"] != "content is required" {
		t.Errorf("Expected error 'content is required', got %s", errResp["error"])
	}
}

// TestCreateMessage_InvalidJSON JSON パース失敗
func TestCreateMessage_InvalidJSON(t *testing.T) {
	testDB := setupTestDB(t)
	defer cleanupTestDB(testDB)

	h := newTestHandler(testDB)
	router := h.SetupRouter()

	req := httptest.NewRequest("POST", "/messages", strings.NewReader("invalid json"))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var errResp map[string]string
	json.Unmarshal(w.Body.Bytes(), &errResp)
	if errResp["error"] != "Invalid request body" {
		t.Errorf("Expected 'Invalid request body' error, got %s", errResp["error"])
	}
}

// TestGetMessages メッセージ取得テスト（10件以下はすべて返る）
func TestGetMessages(t *testing.T) {
	testDB := setupTestDB(t)
	defer cleanupTestDB(testDB)

	// テストデータ挿入（AUTO_INCREMENT）
	testDB.Exec("INSERT INTO messages (content, created_at) VALUES (?, ?)", "Message 1", time.Now())
	testDB.Exec("INSERT INTO messages (content, created_at) VALUES (?, ?)", "Message 2", time.Now())

	h := newTestHandler(testDB)
	router := h.SetupRouter()

	req := httptest.NewRequest("GET", "/messages", nil)
	req.Header.Set("Origin", "http://localhost:8080")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var msgList []model.Message
	json.Unmarshal(w.Body.Bytes(), &msgList)

	if len(msgList) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(msgList))
	}
}

// TestGetMessages_MaxLimit 10件を超えるレコードがあっても最大10件しか返らない
func TestGetMessages_MaxLimit(t *testing.T) {
	testDB := setupTestDB(t)
	defer cleanupTestDB(testDB)

	// 15件挿入
	for i := 0; i < 15; i++ {
		testDB.Exec("INSERT INTO messages (content, created_at) VALUES (?, ?)",
			fmt.Sprintf("Message %d", i+1), time.Now())
	}

	h := newTestHandler(testDB)
	router := h.SetupRouter()

	req := httptest.NewRequest("GET", "/messages", nil)
	req.Header.Set("Origin", "http://localhost:8080")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var msgList []model.Message
	json.Unmarshal(w.Body.Bytes(), &msgList)

	if len(msgList) > 10 {
		t.Errorf("Expected at most 10 messages, got %d", len(msgList))
	}

	if len(msgList) != 10 {
		t.Errorf("Expected exactly 10 messages (limit), got %d", len(msgList))
	}
}

// TestGetMessages_Empty 空の状態で取得
func TestGetMessages_Empty(t *testing.T) {
	testDB := setupTestDB(t)
	defer cleanupTestDB(testDB)

	h := newTestHandler(testDB)
	router := h.SetupRouter()

	req := httptest.NewRequest("GET", "/messages", nil)
	req.Header.Set("Origin", "http://localhost:8080")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var msgList []model.Message
	json.Unmarshal(w.Body.Bytes(), &msgList)

	if len(msgList) != 0 {
		t.Errorf("Expected 0 messages for empty store, got %d", len(msgList))
	}
}

// TestGetMessages_ExcludesSoftDeleted ソフトデリート済みレコードがGETに含まれないことを確認
func TestGetMessages_ExcludesSoftDeleted(t *testing.T) {
	testDB := setupTestDB(t)
	defer cleanupTestDB(testDB)

	// 未削除メッセージ2件
	testDB.Exec("INSERT INTO messages (content, created_at) VALUES (?, ?)", "Active 1", time.Now())
	testDB.Exec("INSERT INTO messages (content, created_at) VALUES (?, ?)", "Active 2", time.Now())
	// 削除済みメッセージ2件
	testDB.Exec("INSERT INTO messages (content, created_at, deleted_at) VALUES (?, ?, ?)", "Deleted 1", time.Now(), time.Now())
	testDB.Exec("INSERT INTO messages (content, created_at, deleted_at) VALUES (?, ?, ?)", "Deleted 2", time.Now(), time.Now())

	h := newTestHandler(testDB)
	router := h.SetupRouter()

	req := httptest.NewRequest("GET", "/messages", nil)
	req.Header.Set("Origin", "http://localhost:8080")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var msgList []model.Message
	json.Unmarshal(w.Body.Bytes(), &msgList)

	if len(msgList) != 2 {
		t.Errorf("Expected 2 active messages, got %d", len(msgList))
	}

	// 削除済みメッセージが含まれていないことを確認
	for _, msg := range msgList {
		if msg.Content == "Deleted 1" || msg.Content == "Deleted 2" {
			t.Errorf("Soft-deleted message should not appear in GET results: %q", msg.Content)
		}
	}
}

// TestDeleteMessage_AlreadyDeleted 既に削除済みのメッセージの再削除は404を返す
func TestDeleteMessage_AlreadyDeleted(t *testing.T) {
	testDB := setupTestDB(t)
	defer cleanupTestDB(testDB)

	// 削除済みメッセージを挿入
	result, err := testDB.Exec("INSERT INTO messages (content, created_at, deleted_at) VALUES (?, ?, ?)",
		"Already deleted", time.Now(), time.Now())
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}
	insertedID, _ := result.LastInsertId()
	idStr := fmt.Sprintf("%d", insertedID)

	h := newTestHandler(testDB)
	router := h.SetupRouter()

	req := httptest.NewRequest("DELETE", "/messages/"+idStr, nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status %d for already-deleted message, got %d", http.StatusNotFound, w.Code)
	}

	var errResp map[string]string
	json.Unmarshal(w.Body.Bytes(), &errResp)
	if errResp["error"] != "Message not found" {
		t.Errorf("Expected 'Message not found' error, got %s", errResp["error"])
	}
}

// TestCreateMessage_OversizedBody 巨大リクエストボディが拒否されることを確認
func TestCreateMessage_OversizedBody(t *testing.T) {
	testDB := setupTestDB(t)
	defer cleanupTestDB(testDB)

	h := newTestHandler(testDB)
	router := h.SetupRouter()

	// 2MBのボディを生成
	largeContent := strings.Repeat("x", 2*1024*1024)
	msgPayload := map[string]string{"content": largeContent}
	body, _ := json.Marshal(msgPayload)

	req := httptest.NewRequest("POST", "/messages", bytes.NewReader(body))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d for oversized body, got %d", http.StatusBadRequest, w.Code)
	}
}

// TestDeleteMessage メッセージ削除テスト（ソフトデリート）
func TestDeleteMessage(t *testing.T) {
	testDB := setupTestDB(t)
	defer cleanupTestDB(testDB)

	// テストデータ挿入（AUTO_INCREMENT）
	result, err := testDB.Exec("INSERT INTO messages (content, created_at) VALUES (?, ?)", "To be deleted", time.Now())
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}
	insertedID, _ := result.LastInsertId()
	idStr := fmt.Sprintf("%d", insertedID)

	h := newTestHandler(testDB)
	// broadcast goroutineを起動（チャネルブロッキング防止）
	go h.HandleBroadcast()
	router := h.SetupRouter()

	req := httptest.NewRequest("DELETE", "/messages/"+idStr, nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status %d, got %d", http.StatusNoContent, w.Code)
	}

	// 削除済みのメッセージが DeletedAt を持つことを確認
	var deletedAt sql.NullTime
	err = testDB.QueryRow("SELECT deleted_at FROM messages WHERE id = ?", insertedID).Scan(&deletedAt)
	if err != nil {
		t.Errorf("Message should still exist in database after soft delete: %v", err)
	}

	if !deletedAt.Valid {
		t.Error("Message should have DeletedAt set")
	}
}

// TestDeleteMessage_NotFound 存在しないメッセージ削除
func TestDeleteMessage_NotFound(t *testing.T) {
	testDB := setupTestDB(t)
	defer cleanupTestDB(testDB)

	h := newTestHandler(testDB)
	router := h.SetupRouter()

	req := httptest.NewRequest("DELETE", "/messages/999999", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status %d, got %d", http.StatusNotFound, w.Code)
	}

	var errResp map[string]string
	json.Unmarshal(w.Body.Bytes(), &errResp)
	if errResp["error"] != "Message not found" {
		t.Errorf("Expected 'Message not found' error, got %s", errResp["error"])
	}
}

// TestWebSocketConnection WebSocket 接続テスト
func TestWebSocketConnection(t *testing.T) {
	h := &Handler{
		Config: config.Config{
			AllowedOrigins: []string{"http://localhost:8080", "http://127.0.0.1:8080"},
		},
		Clients:   make(map[*websocket.Conn]bool),
		Broadcast: make(chan model.DeleteEventMessage, 100),
	}

	server := httptest.NewServer(h.SetupRouter())
	defer server.Close()

	url := strings.Replace(server.URL, "http://", "ws://", 1)

	header := http.Header{}
	header.Set("Origin", "http://localhost:8080")

	ws, _, err := websocket.DefaultDialer.Dial(url+"/ws", header)
	if err != nil {
		t.Errorf("Failed to connect to WebSocket: %v", err)
		return
	}
	defer ws.Close()

	// 接続確認
	h.ClientMu.RLock()
	clientCount := len(h.Clients)
	h.ClientMu.RUnlock()

	if clientCount == 0 {
		t.Error("WebSocket client should be registered")
	}

	// キープアライブメッセージ送信
	msg := map[string]string{"type": "ping"}
	ws.WriteJSON(msg)

	time.Sleep(100 * time.Millisecond)
}

// TestWebSocketOriginCheck Origin チェックテスト
func TestWebSocketOriginCheck(t *testing.T) {
	h := &Handler{
		Config: config.Config{
			AllowedOrigins: []string{"http://localhost:8080", "http://127.0.0.1:8080"},
		},
		Clients:   make(map[*websocket.Conn]bool),
		Broadcast: make(chan model.DeleteEventMessage, 100),
	}

	server := httptest.NewServer(h.SetupRouter())
	defer server.Close()

	url := strings.Replace(server.URL, "http://", "ws://", 1)

	// 許可されていない Origin で接続試行
	header := http.Header{}
	header.Set("Origin", "http://forbidden.example.com")

	_, _, err := websocket.DefaultDialer.Dial(url+"/ws", header)
	if err == nil {
		t.Error("WebSocket connection from forbidden origin should fail")
	}
}

// TestCreateMessageWithDeletedAt deleted_at を含むリクエストでもサーバー側で nil に上書きされることを確認
func TestCreateMessageWithDeletedAt(t *testing.T) {
	testDB := setupTestDB(t)
	defer cleanupTestDB(testDB)

	h := newTestHandler(testDB)
	router := h.SetupRouter()

	now := time.Now()
	msgPayload := map[string]interface{}{
		"content":    "Should not be deleted",
		"deleted_at": now,
	}
	body, _ := json.Marshal(msgPayload)

	req := httptest.NewRequest("POST", "/messages", bytes.NewReader(body))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status %d, got %d. Body: %s", http.StatusCreated, w.Code, w.Body.String())
	}

	var responseMsg model.Message
	json.Unmarshal(w.Body.Bytes(), &responseMsg)

	if responseMsg.DeletedAt != nil {
		t.Error("Server should override deleted_at to nil for new messages")
	}
}

// TestConcurrentMessageCreation 並行メッセージ作成テスト
func TestConcurrentMessageCreation(t *testing.T) {
	testDB := setupTestDB(t)
	defer cleanupTestDB(testDB)

	h := newTestHandler(testDB)
	router := h.SetupRouter()

	// 10 個の並行リクエスト
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(index int) {
			msgPayload := map[string]string{
				"content": fmt.Sprintf("Concurrent message %d", index),
			}
			body, _ := json.Marshal(msgPayload)

			req := httptest.NewRequest("POST", "/messages", bytes.NewReader(body))
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != http.StatusCreated {
				t.Errorf("Concurrent request failed with status %d: %s", w.Code, w.Body.String())
			}

			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// DBからカウントを確認
	var count int
	err := testDB.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count)
	if err != nil {
		t.Errorf("Failed to count messages: %v", err)
	}

	if count != 10 {
		t.Errorf("Expected 10 messages from concurrent requests, got %d", count)
	}
}

// TestMessageFieldValidation created_at がクライアントから送られてもサーバーが上書きすることを確認
func TestMessageFieldValidation(t *testing.T) {
	testDB := setupTestDB(t)
	defer cleanupTestDB(testDB)

	h := newTestHandler(testDB)
	router := h.SetupRouter()

	oldTime := time.Now().Add(-24 * time.Hour)
	msgPayload := map[string]interface{}{
		"content":    "Test override",
		"created_at": oldTime,
	}
	body, _ := json.Marshal(msgPayload)

	req := httptest.NewRequest("POST", "/messages", bytes.NewReader(body))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	var responseMsg model.Message
	json.Unmarshal(w.Body.Bytes(), &responseMsg)

	// created_at は現在時刻に上書きされているはず
	if responseMsg.CreatedAt.Before(time.Now().Add(-1 * time.Second)) {
		t.Error("Server should override created_at with current time")
	}
}
