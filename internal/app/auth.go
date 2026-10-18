package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

const sessionCookie = "mama7k_session"

var dummyHash = sync.OnceValue(func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte("not-a-real-password"), 12)
	return h
})

func newToken() (raw string, hash []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashToken(raw)
}

func hashToken(raw string) []byte {
	h := sha256.Sum256([]byte(raw))
	return h[:]
}

const userColumns = `id, username, display_name, role, suspended, deleted_at IS NOT NULL, must_change_password`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.Suspended, &u.Deleted, &u.MustChangePassword); err != nil {
		return nil, err
	}
	return &u, nil
}

// sessionMiddleware attaches the signed-in user (if any) to the request context.
// Suspended or deleted users are treated as signed out (FR 2).
func (s *Server) sessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err == nil && c.Value != "" {
			u, err := scanUser(s.db.QueryRow(r.Context(), `
				UPDATE sessions SET last_seen_at = now()
				FROM users u
				WHERE sessions.token_hash = $1 AND sessions.expires_at > now()
				  AND u.id = sessions.user_id AND NOT u.suspended AND u.deleted_at IS NULL
				RETURNING u.`+strings.ReplaceAll(userColumns, ", ", ", u."), hashToken(c.Value)))
			if err == nil {
				r = r.WithContext(withUser(r.Context(), u))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) startSession(ctx context.Context, w http.ResponseWriter, userID string) error {
	raw, hash := newToken()
	expires := s.now().Add(s.cfg.SessionTTL)
	if _, err := s.db.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`, hash, userID, expires); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: raw, Path: "/", Expires: expires,
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func validatePassword(p string) error {
	if len(p) < 8 {
		return errValidation("รหัสผ่านต้องมีอย่างน้อย 8 ตัวอักษร")
	}
	if len(p) > 72 { // bcrypt limit
		return errValidation("รหัสผ่านยาวเกิน 72 ตัวอักษร")
	}
	return nil
}

var usernamePattern = regexp.MustCompile(`^[a-z0-9_]{3,20}$`)

// normalizeUsername lower-cases the name; usernames compare case-insensitively.
func normalizeUsername(n string) string {
	return strings.ToLower(strings.TrimSpace(n))
}

func validateUsername(n string) error {
	if !usernamePattern.MatchString(n) {
		return errValidation("ชื่อผู้ใช้ต้องมี 3–20 ตัว ใช้ได้เฉพาะ a-z, 0-9 และ _")
	}
	return nil
}

func hashPassword(p string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(p), 12)
	return string(h), err
}

// newTemporaryPassword returns a random password for an account created or reset by staff.
func newTemporaryPassword() string {
	b := make([]byte, 9)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func validateDisplayName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if n == "" || len([]rune(n)) > 50 {
		return "", errValidation("Display Name ต้องมี 1–50 ตัวอักษร")
	}
	return n, nil
}

// ---------- handlers ----------

func (s *Server) register(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Username, Password, DisplayName string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	username := normalizeUsername(in.Username)
	if err := validateUsername(username); err != nil {
		return err
	}
	if err := validatePassword(in.Password); err != nil {
		return err
	}
	name, err := validateDisplayName(in.DisplayName)
	if err != nil {
		return err
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		return err
	}
	u, err := scanUser(s.db.QueryRow(r.Context(), `
		INSERT INTO users (username, password_hash, display_name) VALUES ($1, $2, $3)
		ON CONFLICT (username) WHERE deleted_at IS NULL DO NOTHING
		RETURNING `+userColumns, username, hash, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return errConflict("USERNAME_TAKEN", "ชื่อผู้ใช้นี้ถูกใช้แล้ว")
	}
	if err != nil {
		return err
	}
	if err := s.startSession(r.Context(), w, u.ID); err != nil {
		return err
	}
	return ok(w, u)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Username, Password string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	var hash string
	var u User
	err := s.db.QueryRow(r.Context(), `SELECT password_hash, `+userColumns+` FROM users WHERE username = $1 AND deleted_at IS NULL`,
		normalizeUsername(in.Username)).Scan(&hash, &u.ID, &u.Username, &u.DisplayName, &u.Role, &u.Suspended, &u.Deleted, &u.MustChangePassword)
	invalid := &Error{http.StatusUnauthorized, "INVALID_CREDENTIALS", "ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง"}
	if errors.Is(err, pgx.ErrNoRows) {
		// Spend similar time as a real check so response timing does not reveal accounts.
		_ = bcrypt.CompareHashAndPassword(dummyHash(), []byte(in.Password))
		return invalid
	}
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		return invalid
	}
	if u.Suspended {
		return &Error{http.StatusForbidden, "SUSPENDED", "บัญชีนี้ถูกระงับ"}
	}
	if err := s.startSession(r.Context(), w, u.ID); err != nil {
		return err
	}
	return ok(w, u)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if _, err := s.db.Exec(r.Context(), `DELETE FROM sessions WHERE token_hash = $1`, hashToken(c.Value)); err != nil {
			return err
		}
	}
	clearSessionCookie(w)
	return noContent(w)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) error {
	return ok(w, viewer(r)) // null for guests
}

// POST /api/me/password {"currentPassword", "newPassword"} — also clears a staff reset. Other
// sessions are signed out; this one stays.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) error {
	u, err := requireUser(r)
	if err != nil {
		return err
	}
	var in struct{ CurrentPassword, NewPassword string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	var hash string
	if err := s.db.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, u.ID).Scan(&hash); err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.CurrentPassword)) != nil {
		return errValidation("รหัสผ่านปัจจุบันไม่ถูกต้อง")
	}
	if err := validatePassword(in.NewPassword); err != nil {
		return err
	}
	if in.NewPassword == in.CurrentPassword {
		return errValidation("รหัสผ่านใหม่ต้องไม่ซ้ำกับรหัสเดิม")
	}
	newHash, err := hashPassword(in.NewPassword)
	if err != nil {
		return err
	}
	var current []byte
	if c, err := r.Cookie(sessionCookie); err == nil {
		current = hashToken(c.Value)
	}
	var updated *User
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		updated, err = scanUser(tx.QueryRow(ctx, `UPDATE users SET password_hash = $2, must_change_password = false
			WHERE id = $1 RETURNING `+userColumns, u.ID, newHash))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND token_hash IS DISTINCT FROM $2`, u.ID, current)
		return err
	})
	if err != nil {
		return err
	}
	return ok(w, updated)
}

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) error {
	u, err := requireUser(r)
	if err != nil {
		return err
	}
	var in struct{ DisplayName string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	name, err := validateDisplayName(in.DisplayName)
	if err != nil {
		return err
	}
	updated, err := scanUser(s.db.QueryRow(r.Context(), `UPDATE users SET display_name = $2 WHERE id = $1 RETURNING `+userColumns, u.ID, name))
	if err != nil {
		return err
	}
	return ok(w, updated)
}

// deleteAccount implements FR 2.1 in one transaction.
func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) error {
	u, err := requireUser(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		// Builds stay but nobody can edit them; group/private builds become orphaned.
		if _, err := tx.Exec(ctx, `DELETE FROM build_shares WHERE build_id IN (SELECT id FROM builds WHERE creator_id = $1)`, u.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE builds SET creator_deleted = true, creator_id = NULL,
			  visibility = CASE WHEN visibility = 'GROUP' THEN 'PRIVATE'::visibility ELSE visibility END
			WHERE creator_id = $1`, u.ID); err != nil {
			return err
		}
		// Votes are kept but anonymised.
		if _, err := tx.Exec(ctx, `UPDATE votes SET user_id = NULL WHERE user_id = $1`, u.ID); err != nil {
			return err
		}
		// Owned groups pass to the longest-standing member, or are deleted when empty.
		rows, err := tx.Query(ctx, `SELECT id FROM groups WHERE owner_id = $1`, u.ID)
		if err != nil {
			return err
		}
		groupIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, gid := range groupIDs {
			var next string
			err := tx.QueryRow(ctx, `SELECT user_id FROM group_members WHERE group_id = $1 AND user_id <> $2 ORDER BY joined_at LIMIT 1`, gid, u.ID).Scan(&next)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				if err := deleteGroupTx(ctx, tx, gid); err != nil {
					return err
				}
			case err != nil:
				return err
			default:
				if _, err := tx.Exec(ctx, `UPDATE groups SET owner_id = $2 WHERE id = $1`, gid, next); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM group_members WHERE user_id = $1`, u.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, u.ID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE users SET deleted_at = now(), display_name = 'Deleted user', password_hash = '' WHERE id = $1`, u.ID)
		return err
	})
	if err != nil {
		return err
	}
	clearSessionCookie(w)
	return noContent(w)
}
