package handler

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"fuwapachi/internal/config"
)

// 本番と同じ SetupRouter の経路で、TrustedProxies が limiter に渡り、
// 信頼するプロキシ経由の要求が CF-Connecting-IP ごとの枠になること
func TestSetupRouter_RateLimitUsesTrustedProxies(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	for i := 0; i < 6; i++ {
		mock.ExpectExec("INSERT INTO messages").WillReturnResult(sqlmock.NewResult(int64(i+1), 1))
	}

	_, proxy, _ := net.ParseCIDR("100.101.188.8/32")
	h := New(db, config.Config{TrustedProxies: []*net.IPNet{proxy}})
	router := h.SetupRouter()

	post := func(cfIP string, port int) int {
		req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewBufferString(`{"content":"hi"}`))
		req.RemoteAddr = fmt.Sprintf("100.101.188.8:%d", port)
		req.Header.Set("CF-Connecting-IP", cfIP)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr.Code
	}

	// 利用者 A はバースト 5 件を使い切ると、cloudflared の接続（ポート）が変わっても 429
	for i := 0; i < 5; i++ {
		if got := post("198.51.100.1", 40000+i); got != http.StatusCreated {
			t.Fatalf("user A request %d = %d, want 201", i+1, got)
		}
	}
	if got := post("198.51.100.1", 41000); got != http.StatusTooManyRequests {
		t.Fatalf("user A request 6 = %d, want 429", got)
	}
	// 利用者 B は別の枠
	if got := post("198.51.100.2", 41001); got != http.StatusCreated {
		t.Fatalf("user B request 1 = %d, want 201", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
