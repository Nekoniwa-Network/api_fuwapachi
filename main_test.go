package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/websocket"
)

// setupTestDB テスト用データベース接続をセットアップ
func setupTestDB(t *testing.T) *sql.DB {
	// テスト用のインメモリSQLiteまたは専用のテストDB接続
	// ここではテスト用MariaDB接続を想定

	testDB, err := sql.Open("mysql", "<>:<>@tcp(<>:3306)/<>?parseTime=true")
	if err != nil {
		t.Skipf("Skipping test: could not connect to test database: %v", err)
		return nil
	}

	if err := testDB.Ping(); err != nil {
		t.Skipf("Skipping test: could not ping test database: %v", err)
		return nil
	}

	// テーブル作成
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS messages (
		id VARCHAR(255) PRIMARY KEY,
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

	return testDB
}

// cleanupTestDB テスト後のクリーンアップ
func cleanupTestDB(testDB *sql.DB) {
	if testDB != nil {
		testDB.Exec("DELETE FROM messages")
		testDB.Close()
	}
}

// TestCreateMessage_Success メッセージ作成成功テスト
func TestCreateMessage_Success(t *testing.T) {
	testDB := setupTestDB(t)
	if testDB == nil {
		return
	}
	defer cleanupTestDB(testDB)

	// グローバルdbを一時的に差し替え
	originalDB := db
	db = testDB
	defer func() { db = originalDB }()

	router := SetupRouter()

	msgPayload := map[string]string{
		"id":      "msg-001",
		"content": "Hello, World!",
	}
	body, _ := json.Marshal(msgPayload)

	req := httptest.NewRequest("POST", "/messages", bytes.NewReader(body))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status %d, got %d", http.StatusCreated, w.Code)
	}

	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Expected Content-Type: application/json, got %s", w.Header().Get("Content-Type"))
	}

	var responseMsg Message
	json.Unmarshal(w.Body.Bytes(), &responseMsg)

	if responseMsg.ID != "msg-001" || responseMsg.Content != "Hello, World!" {
		t.Errorf("Response message mismatch: %+v", responseMsg)
	}

	if responseMsg.DeletedAt != nil {
		t.Error("DeletedAt should be nil for new message")
	}
}

// TestCreateMessage_MissingID ID 必須チェック
func TestCreateMessage_MissingID(t *testing.T) {
	testDB := setupTestDB(t)
	if testDB == nil {
		return
	}
	defer cleanupTestDB(testDB)

	originalDB := db
	db = testDB
	defer func() { db = originalDB }()

	router := SetupRouter()

	msgPayload := map[string]string{
		"id":      "",
		"content": "No ID message",
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
	if errResp["error"] != "id is required" {
		t.Errorf("Expected error 'id is required', got %s", errResp["error"])
	}
}

// TestCreateMessage_Duplicate 重複 ID チェック
func TestCreateMessage_Duplicate(t *testing.T) {
	testDB := setupTestDB(t)
	if testDB == nil {
		return
	}
	defer cleanupTestDB(testDB)

	originalDB := db
	db = testDB
	defer func() { db = originalDB }()

	router := SetupRouter()

	msgPayload := map[string]string{
		"id":      "msg-dup",
		"content": "First message",
	}
	body, _ := json.Marshal(msgPayload)

	// 1 回目：成功
	req := httptest.NewRequest("POST", "/messages", bytes.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("First POST should succeed, got status %d", w.Code)
	}

	// 2 回目：失敗
	req = httptest.NewRequest("POST", "/messages", bytes.NewReader(body))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("Expected status %d for duplicate, got %d", http.StatusConflict, w.Code)
	}

	var errResp map[string]string
	json.Unmarshal(w.Body.Bytes(), &errResp)
	if !strings.Contains(errResp["error"], "already exists") {
		t.Errorf("Expected 'already exists' error, got %s", errResp["error"])
	}
}

// TestCreateMessage_InvalidJSON JSON パース失敗
func TestCreateMessage_InvalidJSON(t *testing.T) {
	testDB := setupTestDB(t)
	if testDB == nil {
		return
	}
	defer cleanupTestDB(testDB)

	originalDB := db
	db = testDB
	defer func() { db = originalDB }()

	router := SetupRouter()

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

// TestGetMessages メッセージ取得テスト
func TestGetMessages(t *testing.T) {
	testDB := setupTestDB(t)
	if testDB == nil {
		return
	}
	defer cleanupTestDB(testDB)

	originalDB := db
	db = testDB
	defer func() { db = originalDB }()

	// テストデータ挿入
	testDB.Exec("INSERT INTO messages (id, content, created_at, deleted_at) VALUES (?, ?, ?, ?)",
		"msg-1", "Message 1", time.Now(), nil)
	testDB.Exec("INSERT INTO messages (id, content, created_at, deleted_at) VALUES (?, ?, ?, ?)",
		"msg-2", "Message 2", time.Now(), nil)

	router := SetupRouter()

	req := httptest.NewRequest("GET", "/messages", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var msgList []Message
	json.Unmarshal(w.Body.Bytes(), &msgList)

	if len(msgList) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(msgList))
	}
}

// TestGetMessages_Empty 空の状態で取得
func TestGetMessages_Empty(t *testing.T) {
	testDB := setupTestDB(t)
	if testDB == nil {
		return
	}
	defer cleanupTestDB(testDB)

	originalDB := db
	db = testDB
	defer func() { db = originalDB }()

	router := SetupRouter()

	req := httptest.NewRequest("GET", "/messages", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var msgList []Message
	json.Unmarshal(w.Body.Bytes(), &msgList)

	if len(msgList) != 0 {
		t.Errorf("Expected 0 messages for empty store, got %d", len(msgList))
	}
}

// TestDeleteMessage メッセージ削除テスト
func TestDeleteMessage(t *testing.T) {
	testDB := setupTestDB(t)
	if testDB == nil {
		return
	}
	defer cleanupTestDB(testDB)

	originalDB := db
	db = testDB
	defer func() { db = originalDB }()

	// テストデータ挿入
	testDB.Exec("INSERT INTO messages (id, content, created_at, deleted_at) VALUES (?, ?, ?, ?)",
		"msg-del", "To be deleted", time.Now(), nil)

	router := SetupRouter()

	req := httptest.NewRequest("DELETE", "/messages/msg-del", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("Expected status %d, got %d", http.StatusNoContent, w.Code)
	}

	// 削除済みのメッセージが DeletedAt を持つことを確認
	var deletedAt sql.NullTime
	err := testDB.QueryRow("SELECT deleted_at FROM messages WHERE id = ?", "msg-del").Scan(&deletedAt)
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
	if testDB == nil {
		return
	}
	defer cleanupTestDB(testDB)

	originalDB := db
	db = testDB
	defer func() { db = originalDB }()

	router := SetupRouter()

	req := httptest.NewRequest("DELETE", "/messages/nonexistent", nil)
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
	// configを初期化（テスト用）
	config = Config{
		AllowedOrigins: []string{"http://localhost:8080", "http://127.0.0.1:8080"},
	}

	clients = make(map[*websocket.Conn]bool)

	// テストサーバー起動
	server := httptest.NewServer(SetupRouter())
	defer server.Close()

	// ws:// に置き換え
	url := strings.Replace(server.URL, "http://", "ws://", 1)

	// WebSocket ダイアル（Origin ヘッダを設定）
	header := http.Header{}
	header.Set("Origin", "http://localhost:8080")

	ws, _, err := websocket.DefaultDialer.Dial(url+"/ws", header)
	if err != nil {
		t.Errorf("Failed to connect to WebSocket: %v", err)
		return
	}
	defer ws.Close()

	// 接続確認
	clientMu.RLock()
	clientCount := len(clients)
	clientMu.RUnlock()

	if clientCount == 0 {
		t.Error("WebSocket client should be registered")
	}

	// キープアライブメッセージ送信
	msg := map[string]string{"type": "ping"}
	ws.WriteJSON(msg)

	// タイムアウトなく待機
	time.Sleep(100 * time.Millisecond)
}

// TestWebSocketOriginCheck Origin チェックテスト
func TestWebSocketOriginCheck(t *testing.T) {
	// configを初期化（テスト用）
	config = Config{
		AllowedOrigins: []string{"http://localhost:8080", "http://127.0.0.1:8080"},
	}

	server := httptest.NewServer(SetupRouter())
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
	if testDB == nil {
		return
	}
	defer cleanupTestDB(testDB)

	originalDB := db
	db = testDB
	defer func() { db = originalDB }()

	router := SetupRouter()

	now := time.Now()
	msgPayload := map[string]interface{}{
		"id":         "msg-deleted",
		"content":    "Should not be deleted",
		"deleted_at": now,
	}
	body, _ := json.Marshal(msgPayload)

	req := httptest.NewRequest("POST", "/messages", bytes.NewReader(body))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status %d, got %d", http.StatusCreated, w.Code)
	}

	var responseMsg Message
	json.Unmarshal(w.Body.Bytes(), &responseMsg)

	if responseMsg.DeletedAt != nil {
		t.Error("Server should override deleted_at to nil for new messages")
	}
}

// TestConcurrentMessageCreation 並行メッセージ作成テスト
func TestConcurrentMessageCreation(t *testing.T) {
	testDB := setupTestDB(t)
	if testDB == nil {
		return
	}
	defer cleanupTestDB(testDB)

	originalDB := db
	db = testDB
	defer func() { db = originalDB }()

	router := SetupRouter()

	// 10 個の並行リクエスト
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(index int) {
			msgPayload := map[string]string{
				"id":      fmt.Sprintf("concurrent-%d", index),
				"content": "Concurrent message",
			}
			body, _ := json.Marshal(msgPayload)

			req := httptest.NewRequest("POST", "/messages", bytes.NewReader(body))
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != http.StatusCreated {
				t.Errorf("Concurrent request failed with status %d", w.Code)
			}

			done <- true
		}(i)
	}

	// すべてのゴルーチン完了を待機
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

// TestMessageFieldValidation created_at がクライアントから送られても サーバーが上書きすることを確認
func TestMessageFieldValidation(t *testing.T) {
	testDB := setupTestDB(t)
	if testDB == nil {
		return
	}
	defer cleanupTestDB(testDB)

	originalDB := db
	db = testDB
	defer func() { db = originalDB }()

	router := SetupRouter()

	oldTime := time.Now().Add(-24 * time.Hour)
	msgPayload := map[string]interface{}{
		"id":         "msg-override",
		"content":    "Test override",
		"created_at": oldTime,
	}
	body, _ := json.Marshal(msgPayload)

	req := httptest.NewRequest("POST", "/messages", bytes.NewReader(body))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	var responseMsg Message
	json.Unmarshal(w.Body.Bytes(), &responseMsg)

	// created_at は現在時刻に上書きされているはず
	if responseMsg.CreatedAt.Before(time.Now().Add(-1 * time.Second)) {
		t.Error("Server should override created_at with current time")
	}
}
