package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// dropSharesSQL removes the given shares; group builds left without any share become private.
func privatizeOrphanedGroupBuilds(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `
		UPDATE builds b SET visibility = 'PRIVATE'
		WHERE b.visibility = 'GROUP' AND NOT EXISTS (SELECT 1 FROM build_shares s WHERE s.build_id = b.id)`)
	return err
}

// removeMemberTx: a member leaving (or removed) also takes their builds' shares with that group (FR 5.1).
func removeMemberTx(ctx context.Context, tx pgx.Tx, groupID, userID string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM group_members WHERE group_id = $1 AND user_id = $2`, groupID, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM build_shares s USING builds b
		WHERE s.build_id = b.id AND s.group_id = $1 AND b.creator_id = $2`, groupID, userID); err != nil {
		return err
	}
	return privatizeOrphanedGroupBuilds(ctx, tx)
}

// deleteGroupTx deletes a group; builds left without a group become private (FR 5.4).
func deleteGroupTx(ctx context.Context, tx pgx.Tx, groupID string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM groups WHERE id = $1`, groupID); err != nil { // cascades members + shares
		return err
	}
	return privatizeOrphanedGroupBuilds(ctx, tx)
}

func scanGroup(row pgx.Row) (*Group, error) {
	var g Group
	if err := row.Scan(&g.ID, &g.Name, &g.OwnerID, &g.InviteCode, &g.CreatedAt); err != nil {
		return nil, err
	}
	return &g, nil
}

const groupColumns = `g.id, g.name, g.owner_id, g.invite_code, g.created_at`

// findGroupAsMember returns NOT_FOUND for non-members so group existence is not revealed.
func findGroupAsMember(ctx context.Context, q querier, userID, groupID string) (*Group, error) {
	g, err := scanGroup(q.QueryRow(ctx, `
		SELECT `+groupColumns+` FROM groups g
		JOIN group_members m ON m.group_id = g.id AND m.user_id = $2
		WHERE g.id = $1::uuid`, groupID, userID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, errNotFound("ไม่พบ Group")
		}
		return nil, err
	}
	return g, nil
}

func findGroupAsOwner(ctx context.Context, q querier, userID, groupID string) (*Group, error) {
	g, err := findGroupAsMember(ctx, q, userID, groupID)
	if err != nil {
		return nil, err
	}
	if g.OwnerID != userID {
		return nil, errForbidden("เฉพาะ Group Owner")
	}
	return g, nil
}

func validateGroupName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if n == "" || utf8.RuneCountInString(n) > 60 {
		return "", errValidation("ชื่อ Group ต้องมี 1–60 ตัวอักษร")
	}
	return n, nil
}

// ---------- handlers ----------

func (s *Server) listMyGroups(w http.ResponseWriter, r *http.Request) error {
	u, err := requireUser(r)
	if err != nil {
		return err
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT `+groupColumns+`, (SELECT count(*) FROM group_members x WHERE x.group_id = g.id)
		FROM groups g JOIN group_members m ON m.group_id = g.id AND m.user_id = $1
		ORDER BY g.created_at`, u.ID)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (GroupSummary, error) {
		var gs GroupSummary
		err := row.Scan(&gs.Group.ID, &gs.Group.Name, &gs.Group.OwnerID, &gs.Group.InviteCode, &gs.Group.CreatedAt, &gs.MemberCount)
		gs.IsOwner = gs.Group.OwnerID == u.ID
		return gs, err
	})
	if err != nil {
		return err
	}
	return ok(w, list)
}

func (s *Server) getGroup(w http.ResponseWriter, r *http.Request) error {
	u, err := requireUser(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	g, err := findGroupAsMember(ctx, s.db, u.ID, r.PathValue("id"))
	if err != nil {
		return err
	}
	isOwner := g.OwnerID == u.ID
	rows, err := s.db.Query(ctx, `
		SELECT u.id, u.display_name, m.joined_at FROM group_members m JOIN users u ON u.id = m.user_id
		WHERE m.group_id = $1 ORDER BY m.joined_at`, g.ID)
	if err != nil {
		return err
	}
	members, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (GroupMemberView, error) {
		var mv GroupMemberView
		err := row.Scan(&mv.User.ID, &mv.User.DisplayName, &mv.JoinedAt)
		mv.IsOwner = mv.User.ID == g.OwnerID
		return mv, err
	})
	if err != nil {
		return err
	}
	// Hidden builds: only the owner (to unhide) and the creator still see them in the group view.
	list, err := loadBuilds(ctx, s.db, `
		JOIN build_shares gs ON gs.build_id = b.id AND gs.group_id = $1
		WHERE b.visibility = 'GROUP' AND (NOT gs.hidden OR $2 OR b.creator_id = $3)
		ORDER BY b.updated_at DESC`, g.ID, isOwner, u.ID)
	if err != nil {
		return err
	}
	sums, err := s.summaries(ctx, s.db, u, list)
	if err != nil {
		return err
	}
	builds := make([]GroupBuild, len(list))
	for i, b := range list {
		hidden := false
		for _, sh := range b.Shares {
			if sh.GroupID == g.ID {
				hidden = sh.Hidden
			}
		}
		builds[i] = GroupBuild{Summary: sums[i], Hidden: hidden}
	}
	return ok(w, GroupDetail{
		GroupSummary: GroupSummary{Group: *g, MemberCount: len(members), IsOwner: isOwner},
		Members:      members,
		Builds:       builds,
	})
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) error {
	u, err := requireUser(r)
	if err != nil {
		return err
	}
	var in struct{ Name string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	name, err := validateGroupName(in.Name)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var g *Group
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		g, err = scanGroup(tx.QueryRow(ctx, `INSERT INTO groups (name, owner_id, invite_code) VALUES ($1, $2, $3)
			RETURNING id, name, owner_id, invite_code, created_at`, name, u.ID, randomCode()))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)`, g.ID, u.ID)
		return err
	})
	if err != nil {
		return err
	}
	return ok(w, g)
}

func (s *Server) joinGroup(w http.ResponseWriter, r *http.Request) error {
	u, err := requireUser(r)
	if err != nil {
		return err
	}
	var in struct{ Code string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	g, err := scanGroup(s.db.QueryRow(ctx, `SELECT `+groupColumns+` FROM groups g WHERE g.invite_code = $1`, strings.ToUpper(strings.TrimSpace(in.Code))))
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound("Invite Code ไม่ถูกต้องหรือหมดอายุ")
	}
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO group_members (group_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, g.ID, u.ID); err != nil {
		return err
	}
	return ok(w, g)
}

func (s *Server) renameGroup(w http.ResponseWriter, r *http.Request) error {
	u, err := requireUser(r)
	if err != nil {
		return err
	}
	var in struct{ Name string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	name, err := validateGroupName(in.Name)
	if err != nil {
		return err
	}
	g, err := findGroupAsOwner(r.Context(), s.db, u.ID, r.PathValue("id"))
	if err != nil {
		return err
	}
	g, err = scanGroup(s.db.QueryRow(r.Context(), `UPDATE groups g SET name = $2 WHERE id = $1 RETURNING `+groupColumns, g.ID, name))
	if err != nil {
		return err
	}
	return ok(w, g)
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) error {
	u, err := requireUser(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	g, err := findGroupAsOwner(ctx, s.db, u.ID, r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error { return deleteGroupTx(ctx, tx, g.ID) }); err != nil {
		return err
	}
	return noContent(w)
}

func (s *Server) leaveGroup(w http.ResponseWriter, r *http.Request) error {
	u, err := requireUser(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	g, err := findGroupAsMember(ctx, s.db, u.ID, r.PathValue("id"))
	if err != nil {
		return err
	}
	if g.OwnerID == u.ID {
		return errConflict("OWNER_MUST_TRANSFER", "โอนสิทธิ์ Owner ก่อนออกจาก Group หรือลบ Group แทน")
	}
	if err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error { return removeMemberTx(ctx, tx, g.ID, u.ID) }); err != nil {
		return err
	}
	return noContent(w)
}

func (s *Server) kickMember(w http.ResponseWriter, r *http.Request) error {
	u, err := requireUser(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	g, err := findGroupAsOwner(ctx, s.db, u.ID, r.PathValue("id"))
	if err != nil {
		return err
	}
	target := r.PathValue("userId")
	if target == u.ID {
		return errValidation("นำตัวเองออกไม่ได้")
	}
	if err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error { return removeMemberTx(ctx, tx, g.ID, target) }); err != nil {
		return err
	}
	return noContent(w)
}

func (s *Server) transferOwner(w http.ResponseWriter, r *http.Request) error {
	u, err := requireUser(r)
	if err != nil {
		return err
	}
	var in struct {
		UserID string `json:"userId"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	g, err := findGroupAsOwner(ctx, s.db, u.ID, r.PathValue("id"))
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE groups SET owner_id = $2 WHERE id = $1
		  AND EXISTS (SELECT 1 FROM group_members WHERE group_id = $1 AND user_id = $2::uuid)`, g.ID, in.UserID)
	if err != nil && !isInvalidUUID(err) {
		return err
	}
	if err != nil || tag.RowsAffected() == 0 {
		return errValidation("ต้องโอนให้สมาชิกใน Group")
	}
	return noContent(w)
}

func (s *Server) regenerateInvite(w http.ResponseWriter, r *http.Request) error {
	u, err := requireUser(r)
	if err != nil {
		return err
	}
	g, err := findGroupAsOwner(r.Context(), s.db, u.ID, r.PathValue("id"))
	if err != nil {
		return err
	}
	code := randomCode()
	if _, err := s.db.Exec(r.Context(), `UPDATE groups SET invite_code = $2 WHERE id = $1`, g.ID, code); err != nil {
		return err
	}
	return ok(w, map[string]string{"inviteCode": code})
}

// PUT /api/groups/{id}/builds/{buildId}/hidden  {"hidden": true}
func (s *Server) setBuildHidden(w http.ResponseWriter, r *http.Request) error {
	u, err := requireUser(r)
	if err != nil {
		return err
	}
	var in struct {
		Hidden bool `json:"hidden"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	g, err := findGroupAsOwner(r.Context(), s.db, u.ID, r.PathValue("id"))
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(r.Context(), `UPDATE build_shares SET hidden = $3 WHERE group_id = $1 AND build_id = $2::uuid`, g.ID, r.PathValue("buildId"), in.Hidden)
	if err != nil && !isInvalidUUID(err) {
		return err
	}
	if err != nil || tag.RowsAffected() == 0 {
		return errNotFound("ไม่พบ Build ใน Group นี้")
	}
	return noContent(w)
}
