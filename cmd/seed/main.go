// Command seed resets the database and loads the sample data used by the web mock.
// Every seeded user's password is "password123". Never run this against production.
package main

import (
	"context"
	"log"
	"strings"

	"mama7k/api/internal/config"
	"mama7k/api/internal/db"
	"mama7k/api/internal/seed"
)

func main() {
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
	if err := seed.Reset(ctx, pool); err != nil {
		log.Fatal(err)
	}
	log.Printf("seeded; log in as pisit@example.com / %s", seed.Password)
}
