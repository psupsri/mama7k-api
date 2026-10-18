// Package config reads runtime settings from environment variables.
package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr             string        // listen address, e.g. ":8787"
	DatabaseURL      string        // postgres connection string
	CookieSecure     bool          // set Secure on the session cookie (true behind HTTPS)
	SessionTTL       time.Duration // how long a sign-in lasts
	UploadDir        string        // where admin-form images go when R2 is not configured
	MediaBaseURL     string        // public base URL for relative image paths, e.g. https://pub-xxx.r2.dev
	WebDir           string        // built web app (web/dist) to serve at /; empty = API only
	TrustedProxyHops int           // proxies in front of the API that append to X-Forwarded-For

	// Cloudflare R2 (S3 API) for admin-form uploads; all four must be set to use it.
	R2AccountID, R2AccessKeyID, R2SecretAccessKey, R2Bucket string
	R2UploadPrefix                                          string // key prefix, e.g. "master/uploads"
}

// R2Enabled reports whether admin-form uploads go to R2.
func (c Config) R2Enabled() bool {
	return c.R2AccountID != "" && c.R2AccessKeyID != "" && c.R2SecretAccessKey != "" && c.R2Bucket != ""
}

// Load reads settings from the environment. A ".env" file in the working directory is read
// first (KEY=value lines, # comments); variables already set in the environment win.
func Load() Config {
	loadDotEnv(".env")
	addr := ":8787"
	if p := os.Getenv("PORT"); p != "" { // set by hosts such as Render and Koyeb
		addr = ":" + p
	}
	return Config{
		Addr:              env("ADDR", addr),
		DatabaseURL:       env("DATABASE_URL", "postgres://mama7k:mama7k@localhost:5433/mama7k?sslmode=disable"),
		CookieSecure:      env("COOKIE_SECURE", "false") == "true",
		SessionTTL:        envDuration("SESSION_TTL", 30*24*time.Hour),
		UploadDir:         env("UPLOAD_DIR", "./uploads"),
		MediaBaseURL:      strings.TrimRight(env("MEDIA_PUBLIC_URL", "/media"), "/"),
		WebDir:            os.Getenv("WEB_DIR"),
		TrustedProxyHops:  envInt("TRUSTED_PROXY_HOPS", 0),
		R2AccountID:       os.Getenv("R2_ACCOUNT_ID"),
		R2AccessKeyID:     os.Getenv("R2_ACCESS_KEY_ID"),
		R2SecretAccessKey: os.Getenv("R2_SECRET_ACCESS_KEY"),
		R2Bucket:          os.Getenv("R2_BUCKET"),
		R2UploadPrefix:    strings.Trim(env("R2_UPLOAD_PREFIX", "master/uploads"), "/"),
	}
}

// envInt reads a non-negative integer. An invalid value falls back to the default.
func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		log.Printf("config: %s=%q is not a non-negative number; using %d", key, v, fallback)
		return fallback
	}
	return n
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
