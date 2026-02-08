package config

import (
	"os"
	"strings"
)

// Config holds application configuration
type Config struct {
	// MariaDB接続設定
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string

	// サーバー設定
	ServerPort string
	Env        string

	// CORS設定
	AllowedOrigins []string
}

// Load loads configuration from environment variables
func Load() Config {
	dbHost := os.Getenv("DB_HOST")
	if dbHost == "" {
		dbHost = "localhost"
	}

	dbPort := os.Getenv("DB_PORT")
	if dbPort == "" {
		dbPort = "3306"
	}

	dbUser := os.Getenv("DB_USER")
	dbPassword := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")

	serverPort := os.Getenv("SERVER_PORT")
	if serverPort == "" {
		serverPort = "8080"
	}

	env := os.Getenv("ENV")
	if env == "" {
		env = "development"
	}

	allowedOrigins := os.Getenv("ALLOWED_ORIGINS")
	if allowedOrigins == "" {
		allowedOrigins = "http://localhost:3000,http://127.0.0.1:3000"
	}

	cfg := Config{
		DBHost:         dbHost,
		DBPort:         dbPort,
		DBUser:         dbUser,
		DBPassword:     dbPassword,
		DBName:         dbName,
		ServerPort:     serverPort,
		Env:            env,
		AllowedOrigins: strings.Split(allowedOrigins, ","),
	}

	for i := range cfg.AllowedOrigins {
		cfg.AllowedOrigins[i] = strings.TrimSpace(cfg.AllowedOrigins[i])
	}

	return cfg
}
