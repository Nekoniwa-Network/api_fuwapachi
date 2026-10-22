package handler

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"fuwapachi/internal/model"
)

const (
	// wsMaxMessageSize はクライアントから受け取るメッセージの上限（バイト）
	// サーバーはクライアントからのメッセージを使わない（読み捨てる）ため、
	// キープアライブ用の小さな JSON が収まる大きさにとどめる
	wsMaxMessageSize = 4096

	// wsSendBuffer はクライアントごとの送信待ちの数。溢れたら遅い相手として切断する
	// 削除がまとめて起きても健全な相手が切られないよう、少し余裕を持たせる
	// （イベント 1 件は 100 バイト前後なので、1 接続あたり最大で 10KB 未満）
	wsSendBuffer = 64
)

// wsSettings は WebSocket の期限など（テストで短くできるようにフィールドにする）
type wsSettings struct {
	writeWait  time.Duration // 1 回の送信にかけてよい時間
	pongWait   time.Duration // この時間 pong もメッセージも来なければ切断する
	pingPeriod time.Duration // ping を送る間隔。pongWait より短くする
}

func defaultWSSettings() wsSettings {
	return wsSettings{
		writeWait:  10 * time.Second,
		pongWait:   60 * time.Second,
		pingPeriod: 54 * time.Second,
	}
}

// wsClient は接続 1 本ぶんの状態
type wsClient struct {
	conn *websocket.Conn
	send chan []byte
	done chan struct{}
	once sync.Once
}

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

// ClientCount は接続中の WebSocket クライアント数を返す
func (h *Handler) ClientCount() int {
	h.ClientMu.RLock()
	defer h.ClientMu.RUnlock()
	return len(h.Clients)
}

// HandleWebSocket handles GET /ws
func (h *Handler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// 同時接続数の上限。Upgrade の前に枠を取り、取れなければ 503 を返す
	select {
	case h.wsSlots <- struct{}{}:
	default:
		log.Printf("[WebSocket] ❌ Connection limit reached (%d)", cap(h.wsSlots))
		http.Error(w, "Too many WebSocket connections", http.StatusServiceUnavailable)
		return
	}

	upgrader := createUpgrader(h.Config.AllowedOrigins)
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		<-h.wsSlots
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	c := &wsClient{
		conn: conn,
		send: make(chan []byte, wsSendBuffer),
		done: make(chan struct{}),
	}

	h.ClientMu.Lock()
	h.Clients[c] = struct{}{}
	totalClients := len(h.Clients)
	h.ClientMu.Unlock()

	log.Printf("New WebSocket connection. Total clients: %d", totalClients)

	go h.writePump(c)
	h.readPump(c)
}

// closeClient は接続を閉じて一覧から外し、接続数の枠を返す。何度呼んでも 1 回だけ働く
func (h *Handler) closeClient(c *wsClient) {
	c.once.Do(func() {
		close(c.done)
		c.conn.Close()

		h.ClientMu.Lock()
		delete(h.Clients, c)
		remainingClients := len(h.Clients)
		h.ClientMu.Unlock()

		<-h.wsSlots
		log.Printf("[WebSocket] Client disconnected. Total clients: %d", remainingClients)
	})
}

// readPump はクライアントからのメッセージを読み捨て、pong と期限を管理する
func (h *Handler) readPump(c *wsClient) {
	defer h.closeClient(c)

	c.conn.SetReadLimit(wsMaxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(h.ws.pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(h.ws.pongWait))
	})

	for {
		// 上限を超えるメッセージでは ErrReadLimit になり、
		// gorilla/websocket が 1009 (message too big) の close を送る
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
		// クライアントからのメッセージもキープアライブとして扱う
		c.conn.SetReadDeadline(time.Now().Add(h.ws.pongWait))
	}
}

// writePump はクライアントへの送信を 1 本のゴルーチンで行う。各送信に期限を付ける
func (h *Handler) writePump(c *wsClient) {
	ticker := time.NewTicker(h.ws.pingPeriod)
	defer func() {
		ticker.Stop()
		h.closeClient(c)
	}()

	for {
		select {
		case <-c.done:
			return
		case msg := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(h.ws.writeWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(h.ws.writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// broadcastEvent は 1 件のイベントを全クライアントの送信待ちに入れる
//
// 送信待ちが溢れているクライアントは遅い相手として切断し、他のクライアントへの配信を止めない。
func (h *Handler) broadcastEvent(event model.DeleteEventMessage) {
	payload, err := json.Marshal(event)
	if err != nil {
		log.Printf("[WebSocket] ❌ Failed to encode event: %v", err)
		return
	}

	// clients マップをスナップショットしてからロックを外すことで、
	// range 中に delete して "concurrent map iteration and map write"
	// が発生するのを防ぐ
	h.ClientMu.RLock()
	clientsSnapshot := make([]*wsClient, 0, len(h.Clients))
	for client := range h.Clients {
		clientsSnapshot = append(clientsSnapshot, client)
	}
	h.ClientMu.RUnlock()

	for _, client := range clientsSnapshot {
		select {
		case client.send <- payload:
		default:
			log.Printf("[WebSocket] ⚠️  Send buffer full, disconnecting slow client")
			h.closeClient(client)
		}
	}
}

// HandleBroadcast broadcasts delete events to all connected WebSocket clients
func (h *Handler) HandleBroadcast() {
	for event := range h.Broadcast {
		h.broadcastEvent(event)
	}
}
