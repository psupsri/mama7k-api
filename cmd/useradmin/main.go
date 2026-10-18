// Command useradmin creates a staff account or changes an existing account's role. It is the
// way to bootstrap the first Super Admin and Moderators, since the web can only grant roles
// once a Super Admin exists.
//
//	go run ./cmd/useradmin -username mod_01 -name "Mod" -role MODERATOR
//
// A new account gets a random temporary password (printed once) unless -password is given;
// with a temporary password the web asks for a new one after the first sign-in. For an
// existing account only the role changes.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"regexp"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"mama7k/api/internal/config"
	"mama7k/api/internal/db"
)

func main() {
	username := flag.String("username", "", "account username: 3–20 of a-z, 0-9, _ (required)")
	name := flag.String("name", "", "display name for a new account (default: the username)")
	role := flag.String("role", "MODERATOR", "USER, MODERATOR or SUPER_ADMIN")
	password := flag.String("password", "", "password for a new account (default: random, printed once)")
	flag.Parse()

	addr := strings.ToLower(strings.TrimSpace(*username))
	if !regexp.MustCompile(`^[a-z0-9_]{3,20}$`).MatchString(addr) {
		log.Fatal("-username is missing or invalid (3–20 of a-z, 0-9, _)")
	}
	if *role != "USER" && *role != "MODERATOR" && *role != "SUPER_ADMIN" {
		log.Fatal("-role must be USER, MODERATOR or SUPER_ADMIN")
	}

	ctx := context.Background()
	cfg := config.Load()
	if err := db.Migrate(ctx, cfg.DatabaseURL); err != nil {
		log.Fatal(err)
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if u, err := url.Parse(cfg.DatabaseURL); err == nil {
		fmt.Printf("database: %s%s\n", u.Host, u.Path)
	}

	var id, current string
	err = pool.QueryRow(ctx, `SELECT id, role FROM users WHERE username = $1 AND deleted_at IS NULL`, addr).Scan(&id, &current)
	switch {
	case err == nil:
		if _, err := pool.Exec(ctx, `UPDATE users SET role = $2 WHERE id = $1`, id, *role); err != nil {
			log.Fatal(err)
		}
		audit(ctx, pool, fmt.Sprintf("%s: %s → %s", addr, current, *role))
		fmt.Printf("updated %s: role %s → %s\n", addr, current, *role)
	case errors.Is(err, pgx.ErrNoRows):
		pw := *password
		if pw == "" {
			b := make([]byte, 12)
			if _, err := rand.Read(b); err != nil {
				log.Fatal(err)
			}
			pw = base64.RawURLEncoding.EncodeToString(b)
		}
		if len(pw) < 8 || len(pw) > 72 {
			log.Fatal("password must be 8–72 characters")
		}
		display := strings.TrimSpace(*name)
		if display == "" {
			display = addr
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
		if err != nil {
			log.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO users (username, password_hash, display_name, role, must_change_password)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`, addr, string(hash), display, *role, *password == "").Scan(&id); err != nil {
			log.Fatal(err)
		}
		audit(ctx, pool, fmt.Sprintf("%s: created as %s", addr, *role))
		fmt.Printf("created %s (%s) as %s\n", addr, display, *role)
		if *password == "" {
			fmt.Printf("temporary password: %s\n(shown once; the web asks for a new password after the first sign-in)\n", pw)
		}
	default:
		log.Fatal(err)
	}
}

// audit records the change with no actor, since it was made from the command line.
func audit(ctx context.Context, pool *pgxpool.Pool, target string) {
	if _, err := pool.Exec(ctx, `INSERT INTO audit_log (actor_id, action, target) VALUES (NULL, 'CLI_USER_ROLE', $1)`, target); err != nil {
		log.Printf("warning: audit log not written: %v", err)
	}
}
