// Package config reads runtime settings from environment variables.
package config

import (
	"os"
	"time"
)

type Config struct {
	Addr         string        // listen address, e.g. ":8080"
	DatabaseURL  string        // postgres connection string
	BaseURL      string        // public web origin, used in emails and invite links
	CookieSecure bool          // set Secure on the session cookie (true behind HTTPS)
	SessionTTL   time.Duration // session lifetime
	UploadDir    string        // where uploaded master-data images are stored
}

func Load() Config {
	return Config{
		Addr:         env("ADDR", ":8080"),
		DatabaseURL:  env("DATABASE_URL", "postgres://mama7k:mama7k@localhost:5433/mama7k?sslmode=disable"),
		BaseURL:      env("BASE_URL", "http://localhost:5173"),
		CookieSecure: env("COOKIE_SECURE", "false") == "true",
		SessionTTL:   30 * 24 * time.Hour,
		UploadDir:    env("UPLOAD_DIR", "./uploads"),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
