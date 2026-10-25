package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/cors"
)

func TestCORSOptions(t *testing.T) {
	const allowed = "https://fuwapachi.example"
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	tests := []struct {
		name        string
		origins     []string
		origin      string
		method      string // Access-Control-Request-Method（preflight）
		wantAllowed bool
	}{
		{name: "許可したオリジンの DELETE の preflight", origins: []string{allowed}, origin: allowed, method: "DELETE", wantAllowed: true},
		{name: "許可したオリジンの POST の preflight", origins: []string{allowed}, origin: allowed, method: "POST", wantAllowed: true},
		{name: "PUT は許可しない", origins: []string{allowed}, origin: allowed, method: "PUT", wantAllowed: false},
		{name: "許可していないオリジン", origins: []string{allowed}, origin: "https://evil.example", method: "DELETE", wantAllowed: false},
		{name: "許可するオリジンが空ならどれも許可しない", origins: nil, origin: allowed, method: "GET", wantAllowed: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := cors.New(CORSOptions(tt.origins)).Handler(ok)
			req := httptest.NewRequest(http.MethodOptions, "/messages", nil)
			req.Header.Set("Origin", tt.origin)
			req.Header.Set("Access-Control-Request-Method", tt.method)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			gotAllowed := rr.Header().Get("Access-Control-Allow-Origin") == tt.origin
			if gotAllowed != tt.wantAllowed {
				t.Errorf("Access-Control-Allow-Origin = %q, wantAllowed %v", rr.Header().Get("Access-Control-Allow-Origin"), tt.wantAllowed)
			}
			if v := rr.Header().Get("Access-Control-Allow-Credentials"); v != "" {
				t.Errorf("Access-Control-Allow-Credentials = %q, want empty", v)
			}
		})
	}
}

// 実際の要求（preflight でない GET）でも資格情報を許可しないこと
func TestCORSOptions_SimpleRequestNoCredentials(t *testing.T) {
	const allowed = "https://fuwapachi.example"
	h := cors.New(CORSOptions([]string{allowed})).Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/messages", nil)
	req.Header.Set("Origin", allowed)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != allowed {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, allowed)
	}
	if v := rr.Header().Get("Access-Control-Allow-Credentials"); v != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want empty", v)
	}
}
