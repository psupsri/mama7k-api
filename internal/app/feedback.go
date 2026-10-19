package app

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Feedback is a message anyone can send from the site (guests too). Only staff read it.
type Feedback struct {
	ID         string        `json:"id"`
	Kind       string        `json:"kind"` // BUG | IDEA | DATA | OTHER
	Message    string        `json:"message"`
	Contact    string        `json:"contact"`
	Page       string        `json:"page"`
	User       *FeedbackUser `json:"user"` // nil = sent as a guest
	CreatedAt  time.Time     `json:"createdAt"`
	ResolvedAt *time.Time    `json:"resolvedAt"`
}

type FeedbackUser struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
}

var feedbackKinds = []string{"BUG", "IDEA", "DATA", "OTHER"}

// POST /api/feedback {"kind", "message", "contact"?, "page"?} — open to everyone, rate-limited.
func (s *Server) sendFeedback(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Kind, Message, Contact, Page string
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	in.Message, in.Contact, in.Page = strings.TrimSpace(in.Message), strings.TrimSpace(in.Contact), strings.TrimSpace(in.Page)
	if !contains(feedbackKinds, in.Kind) {
		return errValidation("เลือกประเภทของความคิดเห็น")
	}
	if in.Message == "" || utf8.RuneCountInString(in.Message) > 2000 {
		return errValidation("ข้อความต้องมี 1–2000 ตัวอักษร")
	}
	if utf8.RuneCountInString(in.Contact) > 100 {
		return errValidation("ช่องทางติดต่อยาวเกิน 100 ตัวอักษร")
	}
	if !strings.HasPrefix(in.Page, "/") || utf8.RuneCountInString(in.Page) > 300 {
		in.Page = "" // only a path on this site is kept
	}
	var userID *string
	if u := viewer(r); u != nil {
		userID = &u.ID
	}
	if _, err := s.db.Exec(r.Context(), `INSERT INTO feedback (kind, message, contact, page, user_id) VALUES ($1, $2, $3, $4, $5)`,
		in.Kind, in.Message, in.Contact, in.Page, userID); err != nil {
		return err
	}
	return noContent(w)
}

const feedbackSelect = `
SELECT f.id, f.kind, f.message, f.contact, f.page, f.created_at, f.resolved_at, u.id, u.username, u.display_name
FROM feedback f LEFT JOIN users u ON u.id = f.user_id`

func scanFeedback(row pgx.CollectableRow) (Feedback, error) {
	var f Feedback
	var uid, uname, dname *string
	if err := row.Scan(&f.ID, &f.Kind, &f.Message, &f.Contact, &f.Page, &f.CreatedAt, &f.ResolvedAt, &uid, &uname, &dname); err != nil {
		return f, err
	}
	if uid != nil {
		f.User = &FeedbackUser{ID: *uid, Username: *uname, DisplayName: *dname}
	}
	return f, nil
}

// GET /api/admin/feedback?status=open|done|all (default open), newest first. Staff only.
func (s *Server) adminListFeedback(w http.ResponseWriter, r *http.Request) error {
	if _, err := requireAdmin(r, false); err != nil {
		return err
	}
	where := `WHERE f.resolved_at IS NULL`
	switch r.URL.Query().Get("status") {
	case "done":
		where = `WHERE f.resolved_at IS NOT NULL`
	case "all":
		where = ``
	}
	rows, err := s.db.Query(r.Context(), feedbackSelect+` `+where+` ORDER BY f.created_at DESC LIMIT 500`)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, scanFeedback)
	if err != nil {
		return err
	}
	return ok(w, list)
}

// PATCH /api/admin/feedback/{id} {"resolved": bool} — mark handled or reopen. Staff only.
func (s *Server) adminResolveFeedback(w http.ResponseWriter, r *http.Request) error {
	me, err := requireAdmin(r, false)
	if err != nil {
		return err
	}
	var in struct {
		Resolved bool `json:"resolved"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	var f Feedback
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE feedback SET resolved_at = CASE WHEN $2 THEN now() END, resolved_by = CASE WHEN $2 THEN $3::uuid END
			WHERE id = $1::uuid`, r.PathValue("id"), in.Resolved, me.ID)
		if err != nil {
			if isInvalidUUID(err) {
				return errNotFound("ไม่พบความคิดเห็น")
			}
			return err
		}
		if tag.RowsAffected() == 0 {
			return errNotFound("ไม่พบความคิดเห็น")
		}
		action := "FEEDBACK_REOPEN"
		if in.Resolved {
			action = "FEEDBACK_RESOLVE"
		}
		if err := audit(ctx, tx, me.ID, action, r.PathValue("id")); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, feedbackSelect+` WHERE f.id = $1::uuid`, r.PathValue("id"))
		if err != nil {
			return err
		}
		f, err = pgx.CollectExactlyOneRow(rows, scanFeedback)
		return err
	})
	if err != nil {
		return err
	}
	return ok(w, f)
}

// DELETE /api/admin/feedback/{id} — for spam. Staff only.
func (s *Server) adminDeleteFeedback(w http.ResponseWriter, r *http.Request) error {
	me, err := requireAdmin(r, false)
	if err != nil {
		return err
	}
	ctx := r.Context()
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var preview string
		err := tx.QueryRow(ctx, `DELETE FROM feedback WHERE id = $1::uuid RETURNING left(message, 80)`, r.PathValue("id")).Scan(&preview)
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return errNotFound("ไม่พบความคิดเห็น")
		}
		if err != nil {
			return err
		}
		return audit(ctx, tx, me.ID, "FEEDBACK_DELETE", preview)
	})
	if err != nil {
		return err
	}
	return noContent(w)
}
