// Package server は HTTP サーバーの組み立て（期限など）を受け持つ
package server

import (
	"net/http"
	"time"
)

// Timeouts は http.Server に付ける期限
type Timeouts struct {
	ReadHeader time.Duration
	Read       time.Duration
	Write      time.Duration
	Idle       time.Duration
}

// DefaultTimeouts は本番で使う期限
//
// WebSocket は Upgrade の時点で gorilla/websocket が接続の期限を消し、
// 以降は handler 側の読み取り期限（pong）と書き込み期限で管理する。
// そのため ReadTimeout と WriteTimeout は WebSocket の接続を切らない（server_test.go で確認）。
var DefaultTimeouts = Timeouts{
	// ヘッダーを送り切らない接続（Slowloris）を早めに切る
	ReadHeader: 5 * time.Second,
	// 本文は最大 1MB（CreateMessage の MaxBytesReader）なので 15 秒あれば足りる
	Read: 15 * time.Second,
	// DB への問い合わせを含めた応答の書き込みまで
	Write: 15 * time.Second,
	// cloudflared の既定の keep-alive（90 秒）より長くして、
	// cloudflared が使い回そうとした接続をサーバーが先に閉じる場面を減らす
	Idle: 120 * time.Second,
}

// New は期限を付けた http.Server を返す
func New(addr string, h http.Handler) *http.Server {
	return NewWithTimeouts(addr, h, DefaultTimeouts)
}

// NewWithTimeouts は指定した期限を付けた http.Server を返す
func NewWithTimeouts(addr string, h http.Handler, t Timeouts) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: t.ReadHeader,
		ReadTimeout:       t.Read,
		WriteTimeout:      t.Write,
		IdleTimeout:       t.Idle,
	}
}
