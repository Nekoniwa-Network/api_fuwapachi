package middleware

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type RateLimiter struct {
	mu      sync.Mutex
	clients map[string]*client
}

type client struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRateLimiter creates a new RateLimiter
func NewRateLimiter() *RateLimiter {
	rl := &RateLimiter{
		clients: make(map[string]*client),
	}
	go rl.cleanup()
	return rl
}

func (rl *RateLimiter) cleanup() {
	for {
		time.Sleep(time.Minute)
		rl.mu.Lock()
		for ip, client := range rl.clients {
			// Remove clients unseen for 3 minutes
			if time.Since(client.lastSeen) > 3*time.Minute {
				delete(rl.clients, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// Limit applies rate limiting based on IP address
func (rl *RateLimiter) Limit(r rate.Limit, b int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ip := req.RemoteAddr
			// if forwarded := req.Header.Get("X-Forwarded-For"); forwarded != "" {
			// 	ip = strings.Split(forwarded, ",")[0]
			// }

			rl.mu.Lock()
			if _, found := rl.clients[ip]; !found {
				rl.clients[ip] = &client{limiter: rate.NewLimiter(r, b)}
			}
			rl.clients[ip].lastSeen = time.Now()
			limiter := rl.clients[ip].limiter
			rl.mu.Unlock()

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
