package config

import (
	"log"
	"net"
	"os"
	"strconv"
	"strings"
)

// DefaultWSMaxConnections は WebSocket の同時接続数の上限（既定値）
const DefaultWSMaxConnections = 1000

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

	// レート制限で CF-Connecting-IP を信じてよい接続元（cloudflared など）の範囲
	// 空のときは誰も信頼せず、RemoteAddr のホスト部分を使う
	TrustedProxies []*net.IPNet

	// WebSocket の同時接続数の上限
	WSMaxConnections int
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
		DBHost:     dbHost,
		DBPort:     dbPort,
		DBUser:     dbUser,
		DBPassword: dbPassword,
		DBName:     dbName,
		ServerPort: serverPort,
		Env:        env,
	}

	origins, rejected := ParseAllowedOrigins(allowedOrigins)
	for _, v := range rejected {
		log.Printf("⚠️  ALLOWED_ORIGINS: rejecting wildcard entry %q (list each origin explicitly)", v)
	}
	if len(origins) == 0 {
		log.Printf("⚠️  ALLOWED_ORIGINS has no usable entry: no origin is allowed")
	}
	cfg.AllowedOrigins = origins

	trusted, invalid := ParseCIDRs(os.Getenv("TRUSTED_PROXY_CIDRS"))
	for _, v := range invalid {
		log.Printf("⚠️  TRUSTED_PROXY_CIDRS: ignoring invalid entry %q", v)
	}
	cfg.TrustedProxies = trusted

	cfg.WSMaxConnections = DefaultWSMaxConnections
	if v := strings.TrimSpace(os.Getenv("WS_MAX_CONNECTIONS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			log.Printf("⚠️  WS_MAX_CONNECTIONS: invalid value %q, using default %d", v, DefaultWSMaxConnections)
		} else {
			cfg.WSMaxConnections = n
		}
	}

	return cfg
}

// ParseAllowedOrigins はカンマ区切りのオリジンの並びを解釈する
//
// 前後の空白を除き、空の要素は捨てる（空文字のオリジンが許可一覧に入ると、
// Origin ヘッダーの無い WebSocket 接続が通ってしまう）。
// "*" を含む要素は全許可やワイルドカードになるため、rejected に入れて捨てる。
func ParseAllowedOrigins(s string) (origins []string, rejected []string) {
	origins = []string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, "*") {
			rejected = append(rejected, part)
			continue
		}
		origins = append(origins, part)
	}
	return origins, rejected
}

// ParseCIDRs はカンマ区切りの CIDR の並びを解釈する
//
// 空の要素は捨てる。"/" の無い IP は 1 台だけの範囲（/32 または /128）として扱う。
// 解釈できない要素は invalid に入れて返し、範囲には含めない（信頼する範囲を広げない）。
func ParseCIDRs(s string) (nets []*net.IPNet, invalid []string) {
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			ip := net.ParseIP(part)
			if ip == nil {
				invalid = append(invalid, part)
				continue
			}
			bits := 128
			if ip.To4() != nil {
				ip = ip.To4()
				bits = 32
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, n, err := net.ParseCIDR(part)
		if err != nil {
			invalid = append(invalid, part)
			continue
		}
		nets = append(nets, n)
	}
	return nets, invalid
}
