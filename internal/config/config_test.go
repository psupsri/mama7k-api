package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDotEnvFillsUnsetVariablesOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("# comment\nMEDIA_PUBLIC_URL=\"https://pub.example.r2.dev/\"\nADDR=:9999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ADDR", ":1234") // already set: must win
	os.Unsetenv("MEDIA_PUBLIC_URL")
	t.Cleanup(func() { os.Unsetenv("MEDIA_PUBLIC_URL") })
	loadDotEnv(path)
	if got := os.Getenv("ADDR"); got != ":1234" {
		t.Fatalf("ADDR = %q, want the environment value", got)
	}
	cfg := Load()
	if cfg.MediaBaseURL != "https://pub.example.r2.dev" {
		t.Fatalf("MediaBaseURL = %q", cfg.MediaBaseURL)
	}
}

func TestSessionTTL(t *testing.T) {
	t.Setenv("SESSION_TTL", "48h")
	if got := Load().SessionTTL; got != 48*time.Hour {
		t.Fatalf("SessionTTL = %s", got)
	}
	t.Setenv("SESSION_TTL", "30 days") // invalid: keep the default
	if got := Load().SessionTTL; got != 30*24*time.Hour {
		t.Fatalf("SessionTTL = %s", got)
	}
}

func TestPortFromHost(t *testing.T) {
	t.Setenv("ADDR", "")
	t.Setenv("PORT", "10000")
	if got := Load().Addr; got != ":10000" {
		t.Fatalf("Addr = %q", got)
	}
	t.Setenv("ADDR", ":7000") // explicit ADDR wins
	if got := Load().Addr; got != ":7000" {
		t.Fatalf("Addr = %q", got)
	}
}
