package config

import (
	"os"
	"time"
)

// Config holds the runtime settings for the service.
type Config struct {
	// RemoveBGAPIKey is the API key for https://www.remove.bg/
	RemoveBGAPIKey string
	// RemoveBGEndpoint is the remove.bg API endpoint (overridable for tests).
	RemoveBGEndpoint string
	// LotteryWinningEndpoint is the official 大乐透 draw-history API
	// (overridable for tests).
	LotteryWinningEndpoint string
	// ListenAddr is the address the HTTP server listens on.
	ListenAddr string
	// MaxImageBytes limits the size of an uploaded image.
	MaxImageBytes int64

	// JWTSecret is the HMAC key used to sign tokens. When empty a random key
	// is generated at startup (tokens do not survive restarts).
	JWTSecret string
	// JWTTTL is the token lifetime.
	JWTTTL time.Duration
	// JWTIssuer is the issuer claim.
	JWTIssuer string
	// MFAIssuer is the name shown in authenticator apps (TOTP entries).
	MFAIssuer string

	// EmailMode selects the mail sender: "console" (log only, default) or
	// "smtp". SMTP* configure the SMTP server for email delivery.
	EmailMode    string
	SMTPHost     string
	SMTPPort     string
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string

	// UsersFile optionally persists accounts to a JSON file; empty = memory only.
	UsersFile string
	// AdminUsername / AdminPassword seed the initial admin account at startup.
	AdminUsername string
	// AdminPassword seeds the initial admin account at startup.
	AdminPassword string

	// DBHost enables the MySQL-backed user store when non-empty. All DB* fields
	// together form the connection; when DBHost is empty the in-memory/JSON
	// store is used instead.
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	DBCharset  string
}

// Load reads configuration from environment variables, falling back to defaults.
func Load() *Config {
	return &Config{
		RemoveBGAPIKey:         os.Getenv("REMOVE_BG_API_KEY"),
		RemoveBGEndpoint:       getenv("REMOVE_BG_ENDPOINT", "https://api.remove.bg/v1.0/removebg"),
		LotteryWinningEndpoint: getenv("LOTTERY_WINNING_ENDPOINT", "https://webapi.sporttery.cn/gateway/lottery/getHistoryPageListV1.qry"),
		ListenAddr:             getenv("LISTEN_ADDR", ":8080"),
		MaxImageBytes:          10 << 20, // 10 MB

		JWTSecret:     os.Getenv("JWT_SECRET"),
		JWTTTL:        duration("JWT_TTL", 24*time.Hour),
		JWTIssuer:     getenv("JWT_ISSUER", "wechatapp-web"),
		MFAIssuer:     getenv("MFA_ISSUER", "wechatapp-web"),
		EmailMode:     getenv("EMAIL_MODE", "console"),
		SMTPHost:      os.Getenv("SMTP_HOST"),
		SMTPPort:      os.Getenv("SMTP_PORT"),
		SMTPUser:      os.Getenv("SMTP_USER"),
		SMTPPassword:  os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:      os.Getenv("SMTP_FROM"),
		UsersFile:     os.Getenv("USERS_FILE"),
		AdminUsername: getenv("ADMIN_USERNAME", "admin"),
		AdminPassword: getenv("ADMIN_PASSWORD", "admin123"),

		DBHost:     os.Getenv("DB_HOST"),
		DBPort:     getenv("DB_PORT", "3306"),
		DBUser:     getenv("DB_USER", "root"),
		DBPassword: os.Getenv("DB_PASSWORD"),
		DBName:     os.Getenv("DB_NAME"),
		DBCharset:  getenv("DB_CHARSET", "utf8mb4"),
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func duration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
