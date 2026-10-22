package server

import (
	"github.com/rs/cors"
)

// CORSOptions は API の CORS 設定を返す
//
//   - Cookie などの資格情報は使っていないので AllowCredentials は false
//   - メソッドはルートにあるものだけ（PUT のルートは無い）
//   - allowedOrigins が空のときはどのオリジンも許可しない
//     （rs/cors は AllowedOrigins が空だと全オリジンを許可するため、明示的に断る）
func CORSOptions(allowedOrigins []string) cors.Options {
	opts := cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		ExposedHeaders:   []string{"Content-Length"},
		MaxAge:           300,
		AllowCredentials: false,
	}
	if len(allowedOrigins) == 0 {
		opts.AllowOriginFunc = func(string) bool { return false }
	}
	return opts
}
