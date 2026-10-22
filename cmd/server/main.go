package main

import (
	"fmt"
	"log"

	"github.com/joho/godotenv"
	"github.com/rs/cors"

	"fuwapachi/internal/config"
	"fuwapachi/internal/database"
	"fuwapachi/internal/handler"
	"fuwapachi/internal/server"
)

func main() {
	// .envファイルを読み込み
	if err := godotenv.Load(); err != nil {
		log.Printf("⚠️  .env file not found, using default values: %v", err)
	}

	// 環境変数を読み込み
	cfg := config.Load()

	// データベース接続を初期化
	db, err := database.Init(cfg)
	if err != nil {
		log.Fatalf("❌ Failed to initialize database: %v", err)
	}
	defer db.Close()

	// ハンドラー初期化
	h := handler.New(db, cfg)

	// WebSocket ブロードキャスターを開始
	go h.HandleBroadcast()

	router := h.SetupRouter()

	// CORS対応
	c := cors.New(cors.Options{
		AllowedOrigins:   cfg.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "DELETE", "OPTIONS", "PUT"},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		ExposedHeaders:   []string{"Content-Length"},
		MaxAge:           300,
		AllowCredentials: true,
	})

	httpHandler := c.Handler(router)

	fmt.Println("========================================")
	fmt.Println("  Fuwapachi API Server")
	fmt.Println("========================================")
	fmt.Printf("  Environment: %s\n", cfg.Env)
	fmt.Printf("  Server: http://localhost:%s\n", cfg.ServerPort)
	fmt.Printf("  WebSocket: ws://localhost:%s/ws\n", cfg.ServerPort)
	if cfg.DBName != "" {
		fmt.Printf("  Database: %s@%s:%s/%s\n", cfg.DBUser, cfg.DBHost, cfg.DBPort, cfg.DBName)
	}
	fmt.Printf("  Allowed Origins: %v\n", cfg.AllowedOrigins)
	fmt.Println("========================================")
	log.Println("🚀 Server started successfully")
	srv := server.New(":"+cfg.ServerPort, httpHandler)
	log.Fatal(srv.ListenAndServe())
}
