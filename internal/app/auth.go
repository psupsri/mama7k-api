package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

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

const userColumns = `id, email, display_name, role, suspended, deleted_at IS NOT NULL, email_verified_at IS NOT NULL`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.Suspended, &u.Deleted, &u.EmailVerified); err != nil {
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

func (s *Server) sendEmailToken(ctx context.Context, userID, email, kind string) error {
	raw, hash := newToken()
	ttl := 24 * time.Hour
	if kind == "RESET_PASSWORD" {
		ttl = time.Hour
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO email_tokens (token_hash, user_id, kind, expires_at) VALUES ($1, $2, $3, $4)`,
		hash, userID, kind, s.now().Add(ttl)); err != nil {
		return err
	}
	if kind == "VERIFY_EMAIL" {
		return s.mailer.Send(email, "ยืนยันอีเมล 7K Re Community", s.cfg.BaseURL+"/verify-email?token="+raw)
	}
	return s.mailer.Send(email, "ตั้งรหัสผ่านใหม่ 7K Re Community", s.cfg.BaseURL+"/reset-password?token="+raw)
}

// useEmailToken marks a token as used and returns its user id.
func (s *Server) useEmailToken(ctx context.Context, tx pgx.Tx, raw, kind string) (string, error) {
	var userID string
	err := tx.QueryRow(ctx, `
		UPDATE email_tokens SET used_at = now()
		WHERE token_hash = $1 AND kind = $2 AND used_at IS NULL AND expires_at > now()
		RETURNING user_id`, hashToken(raw), kind).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errValidation("ลิงก์ไม่ถูกต้องหรือหมดอายุแล้ว")
	}
	return userID, err
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

func validateDisplayName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if n == "" || len([]rune(n)) > 50 {
		return "", errValidation("Display Name ต้องมี 1–50 ตัวอักษร")
	}
	return n, nil
}

// ---------- handlers ----------

func (s *Server) register(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Email, Password, DisplayName string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
		return errValidation("อีเมลไม่ถูกต้อง")
	}
	if err := validatePassword(in.Password); err != nil {
		return err
	}
	name, err := validateDisplayName(in.DisplayName)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
	if err != nil {
		return err
	}
	u, err := scanUser(s.db.QueryRow(r.Context(), `
		INSERT INTO users (email, password_hash, display_name) VALUES ($1, $2, $3)
		ON CONFLICT (email) WHERE deleted_at IS NULL DO NOTHING
		RETURNING `+userColumns, email, string(hash), name))
	if errors.Is(err, pgx.ErrNoRows) {
		return errConflict("EMAIL_TAKEN", "อีเมลนี้ถูกใช้แล้ว")
	}
	if err != nil {
		return err
	}
	if err := s.sendEmailToken(r.Context(), u.ID, u.Email, "VERIFY_EMAIL"); err != nil {
		return err
	}
	if err := s.startSession(r.Context(), w, u.ID); err != nil {
		return err
	}
	return ok(w, u)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Email, Password string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	var hash string
	var u User
	err := s.db.QueryRow(r.Context(), `SELECT password_hash, `+userColumns+` FROM users WHERE email = $1 AND deleted_at IS NULL`,
		strings.TrimSpace(in.Email)).Scan(&hash, &u.ID, &u.Email, &u.DisplayName, &u.Role, &u.Suspended, &u.Deleted, &u.EmailVerified)
	invalid := &Error{http.StatusUnauthorized, "INVALID_CREDENTIALS", "อีเมลหรือรหัสผ่านไม่ถูกต้อง"}
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

func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Token string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	return pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		userID, err := s.useEmailToken(r.Context(), tx, in.Token, "VERIFY_EMAIL")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `UPDATE users SET email_verified_at = coalesce(email_verified_at, now()) WHERE id = $1`, userID); err != nil {
			return err
		}
		return noContent(w)
	})
}

func (s *Server) resendVerification(w http.ResponseWriter, r *http.Request) error {
	u, err := requireUser(r)
	if err != nil {
		return err
	}
	if !u.EmailVerified {
		if err := s.sendEmailToken(r.Context(), u.ID, u.Email, "VERIFY_EMAIL"); err != nil {
			return err
		}
	}
	return noContent(w)
}

// requestPasswordReset always succeeds so the response does not reveal whether an email exists.
func (s *Server) requestPasswordReset(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Email string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	var id, email string
	err := s.db.QueryRow(r.Context(), `SELECT id, email FROM users WHERE email = $1 AND deleted_at IS NULL AND NOT suspended`,
		strings.TrimSpace(in.Email)).Scan(&id, &email)
	if err == nil {
		if err := s.sendEmailToken(r.Context(), id, email, "RESET_PASSWORD"); err != nil {
			return err
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	return noContent(w)
}

func (s *Server) confirmPasswordReset(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Token, Password string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := validatePassword(in.Password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		userID, err := s.useEmailToken(r.Context(), tx, in.Token, "RESET_PASSWORD")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `UPDATE users SET password_hash = $2 WHERE id = $1`, userID, string(hash)); err != nil {
			return err
		}
		// Sign out everywhere after a password change.
		if _, err := tx.Exec(r.Context(), `DELETE FROM sessions WHERE user_id = $1`, userID); err != nil {
			return err
		}
		return noContent(w)
	})
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
