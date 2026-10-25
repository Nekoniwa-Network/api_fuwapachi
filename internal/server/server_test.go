package server

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"fuwapachi/internal/config"
	"fuwapachi/internal/handler"
	"fuwapachi/internal/model"
)

func TestNew_SetsTimeouts(t *testing.T) {
	srv := New(":0", http.NotFoundHandler())
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.WriteTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Fatalf("all timeouts must be set: %+v", srv)
	}
	if srv.ReadHeaderTimeout > srv.ReadTimeout {
		t.Errorf("ReadHeaderTimeout (%v) should not exceed ReadTimeout (%v)", srv.ReadHeaderTimeout, srv.ReadTimeout)
	}
}

// ReadTimeout と WriteTimeout を過ぎても、WebSocket の接続は切れずに送受信できること
func TestTimeouts_DoNotCloseWebSocket(t *testing.T) {
	const origin = "http://localhost:8080"
	h := handler.New(nil, config.Config{AllowedOrigins: []string{origin}})
	go h.HandleBroadcast()
	defer close(h.Broadcast)

	short := Timeouts{
		ReadHeader: 100 * time.Millisecond,
		Read:       200 * time.Millisecond,
		Write:      200 * time.Millisecond,
		Idle:       200 * time.Millisecond,
	}
	ts := httptest.NewUnstartedServer(nil)
	ts.Config = NewWithTimeouts("", h.SetupRouter(), short)
	ts.Start()
	defer ts.Close()

	header := http.Header{}
	header.Set("Origin", origin)
	ws, _, err := websocket.DefaultDialer.Dial(strings.Replace(ts.URL, "http://", "ws://", 1)+"/ws", header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close()

	// 期限の数倍待つ
	time.Sleep(4 * short.Write)

	// クライアントからの送信（サーバーの読み取り）
	if err := ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"ping"}`)); err != nil {
		t.Fatalf("client write after timeouts: %v", err)
	}

	// サーバーからの送信
	h.Broadcast <- model.DeleteEventMessage{Type: "message_deleted", ID: "7", DeletedAt: time.Now()}
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("client read after timeouts: %v", err)
	}
	if !strings.Contains(string(msg), `"id":"7"`) {
		t.Errorf("unexpected message: %s", msg)
	}
	if h.ClientCount() != 1 {
		t.Errorf("client count = %d, want 1", h.ClientCount())
	}
}

// ヘッダーを送り切らない接続は ReadHeaderTimeout で切られること
func TestTimeouts_ReadHeaderTimeoutClosesSlowClient(t *testing.T) {
	short := Timeouts{
		ReadHeader: 100 * time.Millisecond,
		Read:       time.Second,
		Write:      time.Second,
		Idle:       time.Second,
	}
	ts := httptest.NewUnstartedServer(nil)
	ts.Config = NewWithTimeouts("", http.NotFoundHandler(), short)
	ts.Start()
	defer ts.Close()

	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// リクエスト行だけ送り、ヘッダーを終わらせない
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	start := time.Now()
	_, err = bufio.NewReader(conn).ReadByte()
	elapsed := time.Since(start)
	if err == nil {
		// net/http は 408 などを書いてから閉じることがある。その場合も閉じられたことになる
		return
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("server did not close the connection within %v", elapsed)
	}
}
