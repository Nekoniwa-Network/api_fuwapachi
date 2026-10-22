package middleware

import (
	"container/list"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// DefaultMaxClients は limiter を保持する接続元の上限（既定値）
const DefaultMaxClients = 10000

// clientIdleTTL を超えて見えなかった接続元の limiter は掃除で捨てる
const clientIdleTTL = 3 * time.Minute

// cfConnectingIPHeader は Cloudflare が付ける、利用者の IP を示すヘッダー
const cfConnectingIPHeader = "CF-Connecting-IP"

// RateLimiter は接続元ごとにトークンバケットを持つレート制限
//
// 接続元の数が maxClients に達したときは、最も長く見えていない接続元の
// limiter を捨てて新しい接続元を受け入れる（LRU）。
type RateLimiter struct {
	mu             sync.Mutex
	clients        map[string]*list.Element
	lru            *list.List // 先頭が最近、末尾が最古
	maxClients     int
	trustedProxies []*net.IPNet
	now            func() time.Time
}

type client struct {
	key      string
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRateLimiter は信頼するプロキシ無し・既定の上限で RateLimiter を作る
func NewRateLimiter() *RateLimiter {
	return NewRateLimiterWithConfig(nil, DefaultMaxClients)
}

// NewRateLimiterWithConfig は信頼するプロキシの範囲と接続元の上限を指定して RateLimiter を作る
//
// trustedProxies に入る接続元から来た要求に限り、CF-Connecting-IP の値を接続元とみなす。
// maxClients が 0 以下のときは DefaultMaxClients を使う。
func NewRateLimiterWithConfig(trustedProxies []*net.IPNet, maxClients int) *RateLimiter {
	rl := newRateLimiter(trustedProxies, maxClients)
	go rl.cleanup()
	return rl
}

func newRateLimiter(trustedProxies []*net.IPNet, maxClients int) *RateLimiter {
	if maxClients <= 0 {
		maxClients = DefaultMaxClients
	}
	return &RateLimiter{
		clients:        make(map[string]*list.Element),
		lru:            list.New(),
		maxClients:     maxClients,
		trustedProxies: trustedProxies,
		now:            time.Now,
	}
}

func (rl *RateLimiter) cleanup() {
	for {
		time.Sleep(time.Minute)
		rl.removeIdle()
	}
}

// removeIdle は clientIdleTTL を超えて見えていない接続元を捨てる
func (rl *RateLimiter) removeIdle() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	for e := rl.lru.Back(); e != nil; {
		c := e.Value.(*client)
		if now.Sub(c.lastSeen) <= clientIdleTTL {
			// 末尾から古い順に並んでいるので、ここより前はすべて新しい
			break
		}
		prev := e.Prev()
		rl.lru.Remove(e)
		delete(rl.clients, c.key)
		e = prev
	}
}

// Len は保持している接続元の数を返す
func (rl *RateLimiter) Len() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.clients)
}

// limiterFor は key の limiter を返す。無ければ作り、上限を超える分は最古を捨てる
func (rl *RateLimiter) limiterFor(key string, r rate.Limit, b int) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	if e, ok := rl.clients[key]; ok {
		c := e.Value.(*client)
		c.lastSeen = now
		rl.lru.MoveToFront(e)
		return c.limiter
	}

	for len(rl.clients) >= rl.maxClients {
		oldest := rl.lru.Back()
		if oldest == nil {
			break
		}
		rl.lru.Remove(oldest)
		delete(rl.clients, oldest.Value.(*client).key)
	}

	c := &client{key: key, limiter: rate.NewLimiter(r, b), lastSeen: now}
	rl.clients[key] = rl.lru.PushFront(c)
	return c.limiter
}

// Limit applies rate limiting based on the client IP address
func (rl *RateLimiter) Limit(r rate.Limit, b int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			limiter := rl.limiterFor(RateLimitKey(req, rl.trustedProxies), r, b)

			if !limiter.Allow() {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(map[string]string{"error": "Too many requests"})
				return
			}

			next.ServeHTTP(w, req)
		})
	}
}

// RateLimitKey はレート制限の鍵を返す
//
// trustedProxies が空のときは、変更前と同じく RemoteAddr（IP:ポート）をそのまま鍵にする。
// トンネルの裏で信頼する範囲を設定し忘れたとき、ホスト部分だけを鍵にすると
// 利用者全員が cloudflared の 1 つの枠を分け合ってしまうため、変更前より悪くしない。
// trustedProxies があるときは ClientIP を鍵にする。
func RateLimitKey(req *http.Request, trustedProxies []*net.IPNet) string {
	if len(trustedProxies) == 0 {
		return req.RemoteAddr
	}
	return ClientIP(req, trustedProxies)
}

// ipv6KeyPrefix は IPv6 の鍵を丸める長さ。利用者は通常 /64 以上を持ち、その中で
// アドレスを替えられるため、/64 を 1 つの接続元とみなす
const ipv6KeyPrefix = 64

// ClientIP は接続元を表す鍵（IP）を返す
//
//   - RemoteAddr からポートを外したホスト部分を使う
//   - そのホストが trustedProxies に入るときに限り、CF-Connecting-IP の値が
//     IP として正しければそれを使う
//   - X-Forwarded-For は偽装できるため使わない
//   - IPv6 は /64 に丸め、"2001:db8::/64" の形で返す
func ClientIP(req *http.Request, trustedProxies []*net.IPNet) string {
	host := remoteHost(req.RemoteAddr)
	remoteIP := net.ParseIP(host)
	if remoteIP == nil {
		// RemoteAddr が IP でない（テストなど）ときはそのまま鍵にする
		return host
	}

	if containsIP(trustedProxies, remoteIP) {
		if v := strings.TrimSpace(req.Header.Get(cfConnectingIPHeader)); v != "" {
			if cfIP := net.ParseIP(v); cfIP != nil {
				return ipKey(cfIP)
			}
		}
	}

	return ipKey(remoteIP)
}

// ipKey は IPv4（IPv4 射影アドレスを含む）はそのまま、IPv6 は /64 に丸めた文字列にする
func ipKey(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return fmt.Sprintf("%s/%d", ip.Mask(net.CIDRMask(ipv6KeyPrefix, 128)), ipv6KeyPrefix)
}

// remoteHost は "host:port" からホスト部分を取り出す。ポートが無いときは全体を返す
func remoteHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return strings.Trim(remoteAddr, "[]")
	}
	return host
}

func containsIP(nets []*net.IPNet, ip net.IP) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
