package handler

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gorilla/websocket"

	"fuwapachi/internal/config"
	"fuwapachi/internal/model"
)

const testOrigin = "http://localhost:8080"

func newWSTestHandler(t *testing.T, maxConns int) *Handler {
	t.Helper()
	return New(nil, config.Config{
		AllowedOrigins:   []string{testOrigin},
		WSMaxConnections: maxConns,
	})
}

func dialWS(t *testing.T, serverURL string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	u := strings.Replace(serverURL, "http://", "ws://", 1) + "/ws"
	header := http.Header{}
	header.Set("Origin", testOrigin)
	return websocket.DefaultDialer.Dial(u, header)
}

// waitFor は cond が真になるまで最大 timeout 待つ
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// 上限を超える大きさのメッセージを送ると、1009 で接続が閉じられること
func TestWebSocket_ReadLimitClosesConnection(t *testing.T) {
	h := newWSTestHandler(t, 0)
	server := httptest.NewServer(h.SetupRouter())
	defer server.Close()

	ws, _, err := dialWS(t, server.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close()

	if !waitFor(t, time.Second, func() bool { return h.ClientCount() == 1 }) {
		t.Fatalf("client not registered")
	}

	// 上限ちょうどは受け付ける
	if err := ws.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("a", wsMaxMessageSize))); err != nil {
		t.Fatalf("write max-size message: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if h.ClientCount() != 1 {
		t.Fatalf("max-size message should not close the connection")
	}

	// 上限 + 1 バイトで閉じられる
	if err := ws.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("a", wsMaxMessageSize+1))); err != nil {
		t.Fatalf("write oversized message: %v", err)
	}

	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = ws.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != websocket.CloseMessageTooBig {
		t.Fatalf("read after oversized message: err = %v, want close 1009", err)
	}

	if !waitFor(t, time.Second, func() bool { return h.ClientCount() == 0 }) {
		t.Errorf("client should be unregistered, count = %d", h.ClientCount())
	}
}

// 同時接続数の上限を超えた接続は 503 で断り、切断すると枠が戻ること
func TestWebSocket_ConnectionLimit(t *testing.T) {
	h := newWSTestHandler(t, 1)
	server := httptest.NewServer(h.SetupRouter())
	defer server.Close()

	ws1, _, err := dialWS(t, server.URL)
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}

	_, resp, err := dialWS(t, server.URL)
	if err == nil {
		t.Fatal("second dial should fail while at the limit")
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second dial response = %v, want 503", resp)
	}

	ws1.Close()
	if !waitFor(t, 2*time.Second, func() bool { return h.ClientCount() == 0 }) {
		t.Fatalf("first client not unregistered")
	}

	ws3, _, err := dialWS(t, server.URL)
	if err != nil {
		t.Fatalf("dial after release: %v", err)
	}
	ws3.Close()
}

// pong を返さない相手は pongWait の後に切断され、返す相手は残ること
func TestWebSocket_PingPongDeadline(t *testing.T) {
	h := newWSTestHandler(t, 0)
	h.ws = wsSettings{
		writeWait:  time.Second,
		pongWait:   300 * time.Millisecond,
		pingPeriod: 100 * time.Millisecond,
	}
	server := httptest.NewServer(h.SetupRouter())
	defer server.Close()

	// 読む相手: gorilla の既定の ping ハンドラーが読み取り中に pong を返す
	alive, _, err := dialWS(t, server.URL)
	if err != nil {
		t.Fatalf("dial alive: %v", err)
	}
	defer alive.Close()
	go func() {
		for {
			if _, _, err := alive.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// 読まない相手: pong を返さない
	silent, _, err := dialWS(t, server.URL)
	if err != nil {
		t.Fatalf("dial silent: %v", err)
	}
	defer silent.Close()

	if !waitFor(t, time.Second, func() bool { return h.ClientCount() == 2 }) {
		t.Fatalf("clients not registered, count = %d", h.ClientCount())
	}

	if !waitFor(t, 2*time.Second, func() bool { return h.ClientCount() == 1 }) {
		t.Fatalf("silent client should be disconnected, count = %d", h.ClientCount())
	}

	// pongWait の数倍の時間が過ぎても、pong を返す相手は残っている
	time.Sleep(time.Second)
	if h.ClientCount() != 1 {
		t.Errorf("responsive client should stay connected, count = %d", h.ClientCount())
	}
}

// 送信待ちが溢れた遅い相手は切断され、他の相手への配信は止まらないこと
func TestBroadcast_SlowClientIsDisconnectedOthersReceive(t *testing.T) {
	h := newWSTestHandler(t, 0)

	// 遅い相手: サーバー側の接続を登録するが writePump を動かさない（送信待ちが減らない）
	slowConnCh := make(chan *websocket.Conn, 1)
	upgradeOnly := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		slowConnCh <- conn
	}))
	defer upgradeOnly.Close()

	slowPeer, _, err := dialWS(t, upgradeOnly.URL)
	if err != nil {
		t.Fatalf("dial slow: %v", err)
	}
	defer slowPeer.Close()
	h.wsSlots <- struct{}{} // HandleWebSocket と同じく枠を取る
	slow := &wsClient{conn: <-slowConnCh, send: make(chan []byte, wsSendBuffer), done: make(chan struct{})}
	h.ClientMu.Lock()
	h.Clients[slow] = struct{}{}
	h.ClientMu.Unlock()

	// 普通の相手
	server := httptest.NewServer(h.SetupRouter())
	defer server.Close()
	fast, _, err := dialWS(t, server.URL)
	if err != nil {
		t.Fatalf("dial fast: %v", err)
	}
	defer fast.Close()
	if !waitFor(t, time.Second, func() bool { return h.ClientCount() == 2 }) {
		t.Fatalf("clients not registered, count = %d", h.ClientCount())
	}

	go h.HandleBroadcast()
	defer close(h.Broadcast)

	// 1 件ずつ配信し、普通の相手が受け取ってから次を送る。
	// 遅い相手の送信待ちだけが溜まり、wsSendBuffer 件を超えたところで溢れる
	const events = wsSendBuffer + 4
	for i := 0; i < events; i++ {
		h.Broadcast <- model.DeleteEventMessage{Type: "message_deleted", ID: "1", DeletedAt: time.Now()}

		fast.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, msg, err := fast.ReadMessage()
		if err != nil {
			t.Fatalf("fast client read %d: %v", i, err)
		}
		if !strings.Contains(string(msg), `"message_deleted"`) {
			t.Fatalf("unexpected message: %s", msg)
		}
	}

	if !waitFor(t, time.Second, func() bool { return h.ClientCount() == 1 }) {
		t.Fatalf("slow client should be disconnected, count = %d", h.ClientCount())
	}
	h.ClientMu.RLock()
	_, slowStillThere := h.Clients[slow]
	h.ClientMu.RUnlock()
	if slowStillThere {
		t.Error("slow client is still registered")
	}
}

// 配信のチャネルが詰まっていても、DELETE の応答が返ること
func TestDeleteMessage_ReturnsWhenBroadcastQueueFull(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	h := New(db, config.Config{})
	// HandleBroadcast を動かさず、チャネルを満杯にする（遅い相手で配信が止まった状態）
	for i := 0; i < cap(h.Broadcast); i++ {
		h.Broadcast <- model.DeleteEventMessage{}
	}

	mock.ExpectQuery("SELECT EXISTS").WithArgs("42").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec("UPDATE messages SET deleted_at").WithArgs(sqlmock.AnyArg(), "42").
		WillReturnResult(sqlmock.NewResult(0, 1))

	router := h.SetupRouter()
	req := httptest.NewRequest(http.MethodDelete, "/messages/42", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		router.ServeHTTP(rr, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("DELETE did not return while the broadcast queue was full")
	}

	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rr.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// ALLOWED_ORIGINS の末尾のカンマで空のオリジンが許可され、Origin の無い接続が通ってしまわないこと
func TestWebSocket_TrailingCommaDoesNotAllowMissingOrigin(t *testing.T) {
	origins, _ := config.ParseAllowedOrigins(testOrigin + ",")
	h := New(nil, config.Config{AllowedOrigins: origins})
	server := httptest.NewServer(h.SetupRouter())
	defer server.Close()

	u := strings.Replace(server.URL, "http://", "ws://", 1) + "/ws"
	if _, _, err := websocket.DefaultDialer.Dial(u, nil); err == nil {
		t.Error("connection without Origin header should be refused")
	}
}

func dialWSAs(t *testing.T, serverURL, cfIP string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	u := strings.Replace(serverURL, "http://", "ws://", 1) + "/ws"
	header := http.Header{}
	header.Set("Origin", testOrigin)
	header.Set("CF-Connecting-IP", cfIP)
	return websocket.DefaultDialer.Dial(u, header)
}

// 信頼するプロキシ経由では、接続元ごとの上限を超えた接続を 429 で断り、
// 他の接続元は接続でき、切断すると枠が戻ること
func TestWebSocket_PerClientLimit(t *testing.T) {
	_, loopback, _ := net.ParseCIDR("127.0.0.1/32")
	h := New(nil, config.Config{
		AllowedOrigins:        []string{testOrigin},
		TrustedProxies:        []*net.IPNet{loopback},
		WSMaxConnectionsPerIP: 2,
	})
	server := httptest.NewServer(h.SetupRouter())
	defer server.Close()

	a1, _, err := dialWSAs(t, server.URL, "198.51.100.1")
	if err != nil {
		t.Fatalf("a1: %v", err)
	}
	defer a1.Close()
	a2, _, err := dialWSAs(t, server.URL, "198.51.100.1")
	if err != nil {
		t.Fatalf("a2: %v", err)
	}
	defer a2.Close()

	_, resp, err := dialWSAs(t, server.URL, "198.51.100.1")
	if err == nil || resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("a3: err=%v resp=%v, want 429", err, resp)
	}

	b1, _, err := dialWSAs(t, server.URL, "198.51.100.2")
	if err != nil {
		t.Fatalf("b1 (別の接続元) should connect: %v", err)
	}
	defer b1.Close()

	a1.Close()
	if !waitFor(t, 2*time.Second, func() bool { return h.ClientCount() == 2 }) {
		t.Fatalf("a1 not unregistered, count = %d", h.ClientCount())
	}
	a4, _, err := dialWSAs(t, server.URL, "198.51.100.1")
	if err != nil {
		t.Fatalf("a4 after release: %v", err)
	}
	a4.Close()
}

// 信頼するプロキシが未設定なら、接続元ごとの上限は使わない（全員が cloudflared に見えるため）
func TestWebSocket_PerClientLimitDisabledWithoutTrustedProxies(t *testing.T) {
	h := New(nil, config.Config{
		AllowedOrigins:        []string{testOrigin},
		WSMaxConnectionsPerIP: 1,
	})
	server := httptest.NewServer(h.SetupRouter())
	defer server.Close()

	for i := 0; i < 3; i++ {
		ws, _, err := dialWSAs(t, server.URL, "198.51.100.1")
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		defer ws.Close()
	}
}
