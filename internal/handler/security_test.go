package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gorilla/mux"

	"fuwapachi/internal/config"
	"fuwapachi/internal/middleware"
)

func setupTestRouter() (*mux.Router, sqlmock.Sqlmock, error) {
	db, mock, err := sqlmock.New()
	if err != nil {
		return nil, nil, err
	}

	cfg := config.Config{}
	h := New(db, cfg)

	r := mux.NewRouter()
	postRouter := r.Methods("POST").Subrouter()
	postRouter.HandleFunc("/messages", h.CreateMessage)
	deleteRouter := r.Methods("DELETE").Subrouter()
	deleteRouter.HandleFunc("/messages/{id}", h.DeleteMessage)
	
	rl := middleware.NewRateLimiter()
	postRouter.Use(rl.Limit(1, 5))
	deleteRouter.Use(rl.Limit(1, 5))

	return r, mock, nil
}

func TestSecurity_XSS(t *testing.T) {
	r, mock, err := setupTestRouter()
	if err != nil {
		t.Fatalf("Failed to open sqlmock database: %s", err)
	}

	// 期待されるSQLのモック（エスケープされた文字列が渡されることを確認）
	mock.ExpectExec("INSERT INTO messages").
		WithArgs("&lt;script&gt;alert(&#39;XSS&#39;)&lt;/script&gt;", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	body := []byte(`{"content":"<script>alert('XSS')</script>"}`)
	req, _ := http.NewRequest("POST", "/messages", bytes.NewBuffer(body))
	req.RemoteAddr = "192.168.1.1:12345"
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusCreated {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusCreated)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}

func TestSecurity_LengthLimit(t *testing.T) {
	r, _, err := setupTestRouter()
	if err != nil {
		t.Fatalf("Failed to open sqlmock database: %s", err)
	}

	// 201文字の文字列を生成
	longStr := strings.Repeat("a", 201)
	body, _ := json.Marshal(map[string]string{"content": longStr})

	req, _ := http.NewRequest("POST", "/messages", bytes.NewBuffer(body))
	req.RemoteAddr = "192.168.1.2:12345"
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}
}

func TestSecurity_RateLimit(t *testing.T) {
	r, mock, err := setupTestRouter()
	if err != nil {
		t.Fatalf("Failed to open sqlmock database: %s", err)
	}

	// バースト5回までは許可される
	for i := 0; i < 5; i++ {
		mock.ExpectExec("INSERT INTO messages").WillReturnResult(sqlmock.NewResult(int64(i+1), 1))
	}

	for i := 0; i < 6; i++ {
		body := []byte(`{"content":"test"}`)
		req, _ := http.NewRequest("POST", "/messages", bytes.NewBuffer(body))
		// IPを固定してリクエスト（192.168.1.3）
		req.RemoteAddr = "192.168.1.3:12345"
		rr := httptest.NewRecorder()

		r.ServeHTTP(rr, req)

		if i < 5 {
			if rr.Code != http.StatusCreated {
				t.Errorf("Request %d should be allowed, got %v", i+1, rr.Code)
			}
		} else {
			// 6回目は429 Too Many Requestsになるはず
			if rr.Code != http.StatusTooManyRequests {
				t.Errorf("Request 6 should be blocked with 429, got %v", rr.Code)
			}
		}
	}
}
