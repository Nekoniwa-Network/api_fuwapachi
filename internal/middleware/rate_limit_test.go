package middleware

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func mustCIDRs(t *testing.T, cidrs ...string) []*net.IPNet {
	t.Helper()
	var nets []*net.IPNet
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			t.Fatalf("ParseCIDR(%q): %v", c, err)
		}
		nets = append(nets, n)
	}
	return nets
}

func TestClientIP(t *testing.T) {
	trusted := mustCIDRs(t, "100.101.188.8/32", "fd00::/8")

	tests := []struct {
		name       string
		remoteAddr string
		cfIP       string
		xff        string
		trusted    []*net.IPNet
		want       string
	}{
		{name: "IPv4 のポートを外す", remoteAddr: "203.0.113.5:54321", want: "203.0.113.5"},
		{name: "IPv6 のポートと角括弧を外し /64 に丸める", remoteAddr: "[2001:db8::1]:443", want: "2001:db8::/64"},
		{name: "同じ /64 の別のアドレスは同じ鍵", remoteAddr: "[2001:db8::ffff:1234:5678:9abc]:443", want: "2001:db8::/64"},
		{name: "別の /64 は別の鍵", remoteAddr: "[2001:db8:0:1::1]:443", want: "2001:db8:0:1::/64"},
		{name: "IPv4 射影アドレスは IPv4 に戻す", remoteAddr: "[::ffff:203.0.113.5]:443", want: "203.0.113.5"},
		{name: "ポートが無い RemoteAddr", remoteAddr: "203.0.113.5", want: "203.0.113.5"},
		{name: "信頼する範囲が空なら CF-Connecting-IP を無視", remoteAddr: "100.101.188.8:1234", cfIP: "198.51.100.7", want: "100.101.188.8"},
		{name: "信頼するプロキシからの CF-Connecting-IP を使う", remoteAddr: "100.101.188.8:1234", cfIP: "198.51.100.7", trusted: trusted, want: "198.51.100.7"},
		{name: "CF-Connecting-IP の前後の空白を許す", remoteAddr: "100.101.188.8:1234", cfIP: "  198.51.100.7 ", trusted: trusted, want: "198.51.100.7"},
		{name: "信頼するプロキシからの IPv6 の CF-Connecting-IP は /64 に丸める", remoteAddr: "100.101.188.8:1234", cfIP: "2001:db8::abcd", trusted: trusted, want: "2001:db8::/64"},
		{name: "IPv6 の信頼するプロキシ", remoteAddr: "[fd12::1]:1234", cfIP: "198.51.100.7", trusted: trusted, want: "198.51.100.7"},
		{name: "範囲外の接続元の CF-Connecting-IP は無視", remoteAddr: "100.101.188.9:1234", cfIP: "198.51.100.7", trusted: trusted, want: "100.101.188.9"},
		{name: "IP として正しくない CF-Connecting-IP は無視", remoteAddr: "100.101.188.8:1234", cfIP: "not-an-ip", trusted: trusted, want: "100.101.188.8"},
		{name: "複数の値を並べた CF-Connecting-IP は無視", remoteAddr: "100.101.188.8:1234", cfIP: "198.51.100.7, 198.51.100.8", trusted: trusted, want: "100.101.188.8"},
		{name: "信頼するプロキシでも X-Forwarded-For は使わない", remoteAddr: "100.101.188.8:1234", xff: "198.51.100.7", trusted: trusted, want: "100.101.188.8"},
		{name: "直接の接続元の X-Forwarded-For は使わない", remoteAddr: "203.0.113.5:1234", xff: "198.51.100.7", want: "203.0.113.5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/messages", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.cfIP != "" {
				req.Header.Set("CF-Connecting-IP", tt.cfIP)
			}
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if got := ClientIP(req, tt.trusted); got != tt.want {
				t.Errorf("ClientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

// 信頼する範囲を設定したとき、同じ IP からポートを変えて接続しても同じ枠を使うこと
func TestLimit_SameIPDifferentPortsShareQuota(t *testing.T) {
	rl := newRateLimiter(mustCIDRs(t, "100.101.188.8/32"), 0)
	h := rl.Limit(1, 2)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	codes := make([]int, 0, 3)
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/messages", nil)
		req.RemoteAddr = fmt.Sprintf("203.0.113.5:%d", 40000+i)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		codes = append(codes, rr.Code)
	}

	want := []int{http.StatusOK, http.StatusOK, http.StatusTooManyRequests}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("status codes = %v, want %v", codes, want)
		}
	}
	if rl.Len() != 1 {
		t.Errorf("limiter entries = %d, want 1", rl.Len())
	}
}

// 信頼するプロキシ経由では、CF-Connecting-IP ごとに別の枠になること
func TestLimit_TrustedProxySeparatesClients(t *testing.T) {
	rl := newRateLimiter(mustCIDRs(t, "100.101.188.8/32"), 0)
	h := rl.Limit(1, 1)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	send := func(cfIP string) int {
		req := httptest.NewRequest(http.MethodPost, "/messages", nil)
		req.RemoteAddr = "100.101.188.8:5555"
		req.Header.Set("CF-Connecting-IP", cfIP)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}

	if got := send("198.51.100.1"); got != http.StatusOK {
		t.Fatalf("first client first request = %d, want 200", got)
	}
	if got := send("198.51.100.2"); got != http.StatusOK {
		t.Fatalf("second client first request = %d, want 200 (別の利用者は別の枠)", got)
	}
	if got := send("198.51.100.1"); got != http.StatusTooManyRequests {
		t.Fatalf("first client second request = %d, want 429", got)
	}
}

// 上限に達したら最も長く見えていない接続元を捨て、件数が上限を超えないこと
func TestRateLimiter_MaxClientsEvictsLeastRecentlySeen(t *testing.T) {
	const max = 3
	rl := newRateLimiter(nil, max)

	clock := time.Unix(1_700_000_000, 0)
	rl.now = func() time.Time { return clock }

	keys := []string{"a", "b", "c"}
	limiters := map[string]any{}
	for _, k := range keys {
		limiters[k] = rl.limiterFor(k, 1, 1)
		clock = clock.Add(time.Second)
	}
	// a を最近使ったことにする。最古は b になる
	if rl.limiterFor("a", 1, 1) != limiters["a"] {
		t.Fatal("existing key should return the same limiter")
	}
	clock = clock.Add(time.Second)

	rl.limiterFor("d", 1, 1)

	if rl.Len() != max {
		t.Fatalf("entries = %d, want %d", rl.Len(), max)
	}
	rl.mu.Lock()
	_, hasA := rl.clients["a"]
	_, hasB := rl.clients["b"]
	_, hasD := rl.clients["d"]
	rl.mu.Unlock()
	if !hasA || hasB || !hasD {
		t.Errorf("after eviction: a=%v b=%v d=%v, want a=true b=false d=true", hasA, hasB, hasD)
	}

	// 大量の新しい接続元が来ても上限を超えない
	for i := 0; i < 1000; i++ {
		rl.limiterFor(fmt.Sprintf("x%d", i), 1, 1)
	}
	if rl.Len() != max {
		t.Errorf("entries after flood = %d, want %d", rl.Len(), max)
	}
}

func TestRateLimiter_RemoveIdle(t *testing.T) {
	rl := newRateLimiter(nil, 0)
	clock := time.Unix(1_700_000_000, 0)
	rl.now = func() time.Time { return clock }

	rl.limiterFor("old", 1, 1)
	clock = clock.Add(2 * time.Minute)
	rl.limiterFor("new", 1, 1)
	clock = clock.Add(2 * time.Minute) // old は 4 分、new は 2 分見えていない

	rl.removeIdle()

	rl.mu.Lock()
	_, hasOld := rl.clients["old"]
	_, hasNew := rl.clients["new"]
	n := rl.lru.Len()
	rl.mu.Unlock()
	if hasOld || !hasNew || n != 1 {
		t.Errorf("after removeIdle: old=%v new=%v lru=%d, want old=false new=true lru=1", hasOld, hasNew, n)
	}
}

// 信頼する範囲が未設定のときは、変更前と同じく RemoteAddr（IP:ポート）全体を鍵にすること
// （トンネルの裏で設定し忘れても、全員が cloudflared の 1 つの枠を分け合わない）
func TestRateLimitKey_UnsetKeepsRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/messages", nil)
	req.RemoteAddr = "100.101.188.8:40001"
	req.Header.Set("CF-Connecting-IP", "198.51.100.7")
	if got := RateLimitKey(req, nil); got != "100.101.188.8:40001" {
		t.Errorf("RateLimitKey(unset) = %q, want RemoteAddr as is", got)
	}
	if got := RateLimitKey(req, mustCIDRs(t, "100.101.188.8/32")); got != "198.51.100.7" {
		t.Errorf("RateLimitKey(trusted) = %q, want CF-Connecting-IP", got)
	}
}

func TestLimit_UnsetDifferentPortsAreSeparate(t *testing.T) {
	rl := newRateLimiter(nil, 0)
	h := rl.Limit(1, 1)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/messages", nil)
		req.RemoteAddr = fmt.Sprintf("100.101.188.8:%d", 40000+i)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d from a new connection = %d, want 200 (変更前と同じ接続ごとの枠)", i, rr.Code)
		}
	}
}

// 信頼するプロキシ経由で、同じ /64 の IPv6 アドレスを替えても同じ枠になること
func TestLimit_IPv6SameSlash64SharesQuota(t *testing.T) {
	rl := newRateLimiter(mustCIDRs(t, "100.101.188.8/32"), 0)
	h := rl.Limit(1, 2)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	codes := []int{}
	for _, ip := range []string{"2001:db8::1", "2001:db8::2", "2001:db8::dead:beef"} {
		req := httptest.NewRequest(http.MethodPost, "/messages", nil)
		req.RemoteAddr = "100.101.188.8:5555"
		req.Header.Set("CF-Connecting-IP", ip)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		codes = append(codes, rr.Code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != http.StatusTooManyRequests {
		t.Errorf("codes = %v, want [200 200 429]", codes)
	}
}
