package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func asPgError(err error, target **pgconn.PgError) bool { return errors.As(err, target) }

// isUniqueViolation reports a unique-constraint failure, optionally for a specific constraint.
func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return asPgError(err, &pgErr) && pgErr.Code == "23505" && (constraint == "" || pgErr.ConstraintName == constraint)
}

func randomCode() string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}

func audit(ctx context.Context, q querier, actorID, action, target string) error {
	_, err := q.Exec(ctx, `INSERT INTO audit_log (actor_id, action, target) VALUES ($1, $2, $3)`, actorID, action, target)
	return err
}

func contains[T comparable](list []T, v T) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func hasDuplicates[T comparable](list []T) bool {
	seen := make(map[T]bool, len(list))
	for _, x := range list {
		if seen[x] {
			return true
		}
		seen[x] = true
	}
	return false
}

var timeZero time.Time

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
