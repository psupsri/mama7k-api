// Command seed resets the database and loads sample users and groups (password "password123").
// Master data stays empty for real data; -sample also loads sample game data (characters,
// lineups, builds, votes) as used by the tests. Never run this against production.
package main

import (
	"context"
	"flag"
	"log"
	"strings"

	"mama7k/api/internal/config"
	"mama7k/api/internal/db"
	"mama7k/api/internal/seed"
)

func main() {
	sample := flag.Bool("sample", false, "also load sample game data (master data, lineups, builds, votes)")
	flag.Parse()
	ctx := context.Background()
	cfg := config.Load()
	if !strings.Contains(cfg.DatabaseURL, "localhost") && !strings.Contains(cfg.DatabaseURL, "127.0.0.1") {
		log.Fatal("refusing to seed a non-local database")
	}
	if err := db.Migrate(ctx, cfg.DatabaseURL); err != nil {
		log.Fatal(err)
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	reset, what := seed.ResetAccounts, "users and groups"
	if *sample {
		reset, what = seed.Reset, "users, groups and sample game data"
	}
	if err := reset(ctx, pool); err != nil {
		log.Fatal(err)
	}
	log.Printf("seeded %s; log in as pisit@example.com / %s", what, seed.Password)
}
