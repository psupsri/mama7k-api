// Package config reads runtime settings from environment variables.
package config

import (
	"log"
	"os"
	"strings"
	"time"
)

type Config struct {
	Addr         string        // listen address, e.g. ":8080"
	DatabaseURL  string        // postgres connection string
	BaseURL      string        // public web origin, used in emails and invite links
	CookieSecure bool          // set Secure on the session cookie (true behind HTTPS)
	SessionTTL   time.Duration // how long a sign-in lasts
	UploadDir    string        // where images uploaded through the admin form are stored
	MediaBaseURL string        // public base URL for relative image paths, e.g. https://pub-xxx.r2.dev
}

// Load reads settings from the environment. A ".env" file in the working directory is read
// first (KEY=value lines, # comments); variables already set in the environment win.
func Load() Config {
	loadDotEnv(".env")
	return Config{
		Addr:         env("ADDR", ":8787"),
		DatabaseURL:  env("DATABASE_URL", "postgres://mama7k:mama7k@localhost:5433/mama7k?sslmode=disable"),
		BaseURL:      env("BASE_URL", "http://localhost:5173"),
		CookieSecure: env("COOKIE_SECURE", "false") == "true",
		SessionTTL:   envDuration("SESSION_TTL", 30*24*time.Hour),
		UploadDir:    env("UPLOAD_DIR", "./uploads"),
		MediaBaseURL: strings.TrimRight(env("MEDIA_PUBLIC_URL", "/media"), "/"),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envDuration reads a Go duration such as "720h". An invalid value falls back to the default.
func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		log.Printf("config: %s=%q is not a positive duration such as 720h; using %s", key, v, fallback)
		return fallback
	}
	return d
}

func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // no .env file: rely on the environment
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.Trim(strings.TrimSpace(value), `"'`)
		if _, set := os.LookupEnv(key); !set {
			os.Setenv(key, value)
		}
	}
}
