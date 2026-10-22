package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"fuwapachi/internal/config"
)

// 要求のコンテキストが切れたら、遅い DB 呼び出しを待たずに応答が返ること
// （DB 呼び出しに r.Context() が渡っていることの確認）
func TestDBCallsHonorRequestContext(t *testing.T) {
	const origin = "http://localhost:8080"
	const dbDelay = 3 * time.Second

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		expect func(mock sqlmock.Sqlmock)
	}{
		{
			name: "POST /messages", method: http.MethodPost, path: "/messages", body: `{"content":"hello"}`,
			expect: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("INSERT INTO messages").WillDelayFor(dbDelay).WillReturnResult(sqlmock.NewResult(1, 1))
			},
		},
		{
			name: "GET /messages", method: http.MethodGet, path: "/messages",
			expect: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT COALESCE").WillDelayFor(dbDelay).
					WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(0))
			},
		},
		{
			name: "DELETE /messages/{id} の存在確認", method: http.MethodDelete, path: "/messages/1",
			expect: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT EXISTS").WillDelayFor(dbDelay).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
			},
		},
		{
			name: "DELETE /messages/{id} の更新", method: http.MethodDelete, path: "/messages/1",
			expect: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT EXISTS").
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
				mock.ExpectExec("UPDATE messages SET deleted_at").WillDelayFor(dbDelay).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			defer db.Close()
			tt.expect(mock)

			h := New(db, config.Config{AllowedOrigins: []string{origin}})
			router := h.SetupRouter()

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body)).WithContext(ctx)
			req.Header.Set("Origin", origin)
			req.RemoteAddr = "192.0.2.20:1234"
			rr := httptest.NewRecorder()

			start := time.Now()
			router.ServeHTTP(rr, req)
			elapsed := time.Since(start)

			if elapsed >= dbDelay/2 {
				t.Errorf("handler waited %v for the DB; the request context is not passed", elapsed)
			}
			if rr.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500", rr.Code)
			}
		})
	}
}
