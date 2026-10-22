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

// 要求のコンテキストが切れたら、遅い読み取り（SELECT）を待たずに応答が返ること
// （読み取りに r.Context() が渡っていることの確認）
func TestDBReadsHonorRequestContext(t *testing.T) {
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

// 書き込み（INSERT、UPDATE）は要求が切れても最後まで実行し、DELETE は削除の通知も出すこと
func TestDBWritesNotCanceledByRequest(t *testing.T) {
	const origin = "http://localhost:8080"

	t.Run("POST /messages", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		defer db.Close()
		mock.ExpectExec("INSERT INTO messages").WillDelayFor(300 * time.Millisecond).
			WillReturnResult(sqlmock.NewResult(1, 1))

		h := New(db, config.Config{AllowedOrigins: []string{origin}})
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		req := httptest.NewRequest(http.MethodPost, "/messages", strings.NewReader(`{"content":"hello"}`)).WithContext(ctx)
		req.RemoteAddr = "192.0.2.30:1234"
		rr := httptest.NewRecorder()
		h.SetupRouter().ServeHTTP(rr, req)

		if rr.Code != http.StatusCreated {
			t.Errorf("status = %d, want 201 (INSERT should complete)", rr.Code)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})

	t.Run("DELETE /messages/{id}", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		defer db.Close()
		mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		mock.ExpectExec("UPDATE messages SET deleted_at").WillDelayFor(300 * time.Millisecond).
			WillReturnResult(sqlmock.NewResult(0, 1))

		h := New(db, config.Config{})
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		req := httptest.NewRequest(http.MethodDelete, "/messages/9", nil).WithContext(ctx)
		req.RemoteAddr = "192.0.2.31:1234"
		rr := httptest.NewRecorder()
		h.SetupRouter().ServeHTTP(rr, req)

		if rr.Code != http.StatusNoContent {
			t.Errorf("status = %d, want 204 (UPDATE should complete)", rr.Code)
		}
		select {
		case ev := <-h.Broadcast:
			if ev.ID != "9" || ev.Type != "message_deleted" {
				t.Errorf("unexpected event: %+v", ev)
			}
		default:
			t.Error("delete event was not broadcast")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})
}

// 書き込みには期限があり、DB が応答しないときは期限で打ち切って 500 を返すこと
func TestDBWritesHaveTimeout(t *testing.T) {
	orig := dbWriteTimeout
	dbWriteTimeout = 100 * time.Millisecond
	defer func() { dbWriteTimeout = orig }()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	mock.ExpectExec("INSERT INTO messages").WillDelayFor(3 * time.Second).
		WillReturnResult(sqlmock.NewResult(1, 1))

	h := New(db, config.Config{})
	req := httptest.NewRequest(http.MethodPost, "/messages", strings.NewReader(`{"content":"hello"}`))
	req.RemoteAddr = "192.0.2.32:1234"
	rr := httptest.NewRecorder()

	start := time.Now()
	h.SetupRouter().ServeHTTP(rr, req)
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Errorf("handler waited %v; the write timeout is not applied", elapsed)
	}
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
}
