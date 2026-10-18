package app

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---------- users ----------

func (s *Server) adminListUsers(w http.ResponseWriter, r *http.Request) error {
	if _, err := requireAdmin(r, false); err != nil {
		return err
	}
	q := "%" + strings.TrimSpace(r.URL.Query().Get("q")) + "%"
	rows, err := s.db.Query(r.Context(), `SELECT `+userColumns+` FROM users
		WHERE deleted_at IS NULL AND (username ILIKE $1 OR display_name ILIKE $1) ORDER BY created_at LIMIT 200`, q)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (User, error) {
		u, err := scanUser(row)
		if err != nil {
			return User{}, err
		}
		return *u, nil
	})
	if err != nil {
		return err
	}
	return ok(w, list)
}

// PATCH /api/admin/users/{id}  {"suspended": bool} and/or {"role": "..."} (role: super admin only)
func (s *Server) adminUpdateUser(w http.ResponseWriter, r *http.Request) error {
	me, err := requireAdmin(r, false)
	if err != nil {
		return err
	}
	var in struct {
		Suspended *bool   `json:"suspended"`
		Role      *string `json:"role"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	target, err := scanUser(s.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, r.PathValue("id")))
	if err != nil {
		return errNotFound("ไม่พบผู้ใช้")
	}
	if target.ID == me.ID {
		return errValidation("แก้ไขบัญชีตัวเองไม่ได้")
	}
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if in.Suspended != nil {
			if target.Role != RoleUser && me.Role != RoleSuperAdmin {
				return errForbidden("Moderator ระงับบัญชีทีมงานไม่ได้")
			}
			if _, err := tx.Exec(ctx, `UPDATE users SET suspended = $2 WHERE id = $1`, target.ID, *in.Suspended); err != nil {
				return err
			}
			if *in.Suspended { // revoke sessions immediately
				if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, target.ID); err != nil {
					return err
				}
			}
			action := "USER_UNSUSPEND"
			if *in.Suspended {
				action = "USER_SUSPEND"
			}
			if err := audit(ctx, tx, me.ID, action, target.DisplayName+" (@"+target.Username+")"); err != nil {
				return err
			}
		}
		if in.Role != nil {
			if me.Role != RoleSuperAdmin {
				return errForbidden("เปลี่ยน role ได้เฉพาะ Super Admin")
			}
			if !contains([]string{"USER", "MODERATOR", "SUPER_ADMIN"}, *in.Role) {
				return errValidation("role ไม่ถูกต้อง")
			}
			if _, err := tx.Exec(ctx, `UPDATE users SET role = $2 WHERE id = $1`, target.ID, *in.Role); err != nil {
				return err
			}
			if err := audit(ctx, tx, me.ID, "USER_ROLE", target.DisplayName+" → "+*in.Role); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	u, err := scanUser(s.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, target.ID))
	if err != nil {
		return err
	}
	return ok(w, u)
}

// POST /api/admin/users/{id}/reset-password — Super Admin only. The site sends no email, so a
// user who forgot their password asks staff, who give them the temporary password returned
// here. Their sessions end and they must choose a new password after signing in.
func (s *Server) adminResetPassword(w http.ResponseWriter, r *http.Request) error {
	me, err := requireAdmin(r, true)
	if err != nil {
		return err
	}
	ctx := r.Context()
	target, err := scanUser(s.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, r.PathValue("id")))
	if err != nil {
		return errNotFound("ไม่พบผู้ใช้")
	}
	if target.ID == me.ID {
		return errValidation("เปลี่ยนรหัสผ่านของตัวเองที่หน้าโปรไฟล์")
	}
	pw := newTemporaryPassword()
	hash, err := hashPassword(pw)
	if err != nil {
		return err
	}
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2, must_change_password = true WHERE id = $1`, target.ID, hash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, target.ID); err != nil {
			return err
		}
		return audit(ctx, tx, me.ID, "USER_PASSWORD_RESET", target.DisplayName+" (@"+target.Username+")")
	})
	if err != nil {
		return err
	}
	return ok(w, map[string]string{"temporaryPassword": pw})
}

// ---------- public builds (staff never see group or private builds) ----------

func (s *Server) adminListPublicBuilds(w http.ResponseWriter, r *http.Request) error {
	me, err := requireAdmin(r, false)
	if err != nil {
		return err
	}
	q := "%" + strings.TrimSpace(r.URL.Query().Get("q")) + "%"
	list, err := loadBuilds(r.Context(), s.db, `
		WHERE b.visibility = 'PUBLIC' AND (
		  b.description ILIKE $1
		  OR coalesce((SELECT display_name FROM users WHERE id = b.creator_id), '') ILIKE $1
		  OR EXISTS (SELECT 1 FROM characters c WHERE (c.name_th ILIKE $1 OR c.name_en ILIKE $1)
		             AND c.id IN (SELECT jsonb_array_elements(b.team->'placements')->>'characterId')))
		ORDER BY b.updated_at DESC LIMIT 200`, q)
	if err != nil {
		return err
	}
	sums, err := s.summaries(r.Context(), s.db, me, list)
	if err != nil {
		return err
	}
	return ok(w, sums)
}

func (s *Server) publicBuildID(ctx context.Context, id string) (string, error) {
	var bid string
	err := s.db.QueryRow(ctx, `SELECT id FROM builds WHERE id = $1::uuid AND visibility = 'PUBLIC'`, id).Scan(&bid)
	if err != nil {
		return "", errNotFound("ไม่พบ Public Build")
	}
	return bid, nil
}

func (s *Server) adminSetBuildHidden(w http.ResponseWriter, r *http.Request) error {
	me, err := requireAdmin(r, false)
	if err != nil {
		return err
	}
	var in struct {
		Hidden bool `json:"hidden"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	id, err := s.publicBuildID(ctx, r.PathValue("id"))
	if err != nil {
		return err
	}
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE builds SET admin_hidden = $2 WHERE id = $1`, id, in.Hidden); err != nil {
			return err
		}
		action := "BUILD_UNHIDE"
		if in.Hidden {
			action = "BUILD_HIDE"
		}
		return audit(ctx, tx, me.ID, action, id)
	})
	if err != nil {
		return err
	}
	return noContent(w)
}

func (s *Server) adminDeleteBuild(w http.ResponseWriter, r *http.Request) error {
	me, err := requireAdmin(r, false)
	if err != nil {
		return err
	}
	ctx := r.Context()
	id, err := s.publicBuildID(ctx, r.PathValue("id"))
	if err != nil {
		return err
	}
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM builds WHERE id = $1`, id); err != nil {
			return err
		}
		return audit(ctx, tx, me.ID, "BUILD_DELETE", id)
	})
	if err != nil {
		return err
	}
	return noContent(w)
}

// ---------- lineups ----------

func (s *Server) adminListLineups(w http.ResponseWriter, r *http.Request) error {
	if _, err := requireAdmin(r, false); err != nil {
		return err
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT l.id, l.formation, l.created_by, l.created_at,
		       (SELECT jsonb_agg(jsonb_build_object('characterId', p.character_id, 'row', p.row, 'slot', p.slot) ORDER BY p.row, p.slot)
		        FROM lineup_placements p WHERE p.lineup_id = l.id),
		       (SELECT count(*) FROM builds b WHERE b.lineup_id = l.id),
		       (SELECT count(*) FROM builds b WHERE b.lineup_id = l.id AND b.visibility = 'PUBLIC')
		FROM lineups l ORDER BY l.created_at DESC LIMIT 500`)
	if err != nil {
		return err
	}
	type item struct {
		Lineup      DefenseLineup `json:"lineup"`
		BuildCount  int           `json:"buildCount"`
		PublicCount int           `json:"publicCount"`
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (item, error) {
		var it item
		var ps []byte
		if err := row.Scan(&it.Lineup.ID, &it.Lineup.Formation, &it.Lineup.CreatedBy, &it.Lineup.CreatedAt, &ps, &it.BuildCount, &it.PublicCount); err != nil {
			return it, err
		}
		return it, jsonUnmarshal(ps, &it.Lineup.Placements)
	})
	if err != nil {
		return err
	}
	return ok(w, list)
}

func (s *Server) adminUpdateLineup(w http.ResponseWriter, r *http.Request) error {
	me, err := requireAdmin(r, false)
	if err != nil {
		return err
	}
	var in struct {
		Formation  string      `json:"formation"`
		Placements []Placement `json:"placements"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	l, err := loadLineup(ctx, s.db, r.PathValue("id"))
	if err != nil {
		return err
	}
	m, err := loadMaster(ctx, s.db)
	if err != nil {
		return err
	}
	if err := validatePlacements(indexMaster(m), in.Formation, in.Placements, false); err != nil {
		return err
	}
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE lineups SET formation = $2, canonical_key = $3 WHERE id = $1`, l.ID, in.Formation, lineupKey(in.Formation, in.Placements))
		if isUniqueViolation(err, "lineups_canonical_key_key") {
			return errConflict("DUPLICATE", "ซ้ำกับบ้านศัตรูที่มีอยู่ ให้ใช้การ merge แทน")
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM lineup_placements WHERE lineup_id = $1`, l.ID); err != nil {
			return err
		}
		if err := insertPlacements(ctx, tx, l.ID, in.Placements); err != nil {
			return err
		}
		return audit(ctx, tx, me.ID, "LINEUP_UPDATE", l.ID)
	})
	if err != nil {
		return err
	}
	updated, err := loadLineup(ctx, s.db, l.ID)
	if err != nil {
		return err
	}
	return ok(w, updated)
}

// POST /api/admin/lineups/{id}/merge {"targetId": "..."} — moves every build (votes follow) to
// the target lineup and deletes the source (FR 8.2).
func (s *Server) adminMergeLineup(w http.ResponseWriter, r *http.Request) error {
	me, err := requireAdmin(r, false)
	if err != nil {
		return err
	}
	var in struct {
		TargetID string `json:"targetId"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	src, err := loadLineup(ctx, s.db, r.PathValue("id"))
	if err != nil {
		return err
	}
	dst, err := loadLineup(ctx, s.db, in.TargetID)
	if err != nil {
		return errNotFound("ไม่พบบ้านปลายทาง")
	}
	if src.ID == dst.ID {
		return errValidation("เลือกบ้านปลายทางคนละบ้าน")
	}
	var moved int64
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE builds SET lineup_id = $2 WHERE lineup_id = $1`, src.ID, dst.ID)
		if err != nil {
			return err
		}
		moved = tag.RowsAffected()
		if _, err := tx.Exec(ctx, `DELETE FROM lineups WHERE id = $1`, src.ID); err != nil {
			return err
		}
		return audit(ctx, tx, me.ID, "LINEUP_MERGE", fmt.Sprintf("%s → %s (%d Build)", src.ID, dst.ID, moved))
	})
	if err != nil {
		return err
	}
	return ok(w, map[string]int64{"moved": moved})
}

func (s *Server) adminDeleteLineup(w http.ResponseWriter, r *http.Request) error {
	me, err := requireAdmin(r, false)
	if err != nil {
		return err
	}
	ctx := r.Context()
	l, err := loadLineup(ctx, s.db, r.PathValue("id"))
	if err != nil {
		return err
	}
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM builds WHERE lineup_id = $1`, l.ID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return errConflict("HAS_BUILDS", "ลบได้เฉพาะบ้านที่ไม่มี Build")
	}
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM lineups WHERE id = $1`, l.ID); err != nil {
			return err
		}
		return audit(ctx, tx, me.ID, "LINEUP_DELETE", l.ID)
	})
	if err != nil {
		return err
	}
	return noContent(w)
}

// ---------- master data ----------

type entitySpec struct {
	table   string
	columns []string // CSV columns, in template order
}

var entities = map[string]entitySpec{
	"characters": {"characters", []string{"id", "name_th", "name_en", "image", "active"}},
	"pets":       {"pets", []string{"id", "name_th", "name_en", "image", "active"}},
	"sets":       {"equipment_sets", []string{"id", "name_th", "name_en", "image", "active"}},
	"rings":      {"rings", []string{"id", "name_th", "name_en", "image", "active"}},
	"stats":      {"stat_types", []string{"id", "name_th", "name_en", "image", "active"}},
}

// optionalColumns may be left out of an import file.
var optionalColumns = map[string]bool{"name_en": true, "image": true, "active": true}

// idPattern matches master ids: a running number per type, assigned once and never changed.
var idPattern = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)

// masterRecord is one master item in a format-neutral shape (from JSON or a CSV row).
type masterRecord struct {
	ID     string `json:"id"`
	NameTh string `json:"nameTh"`
	NameEn string `json:"nameEn"`
	Active *bool  `json:"active"`
	// Image is a path relative to MEDIA_PUBLIC_URL. Nil leaves the current image unchanged.
	Image *string `json:"image,omitempty"`
}

// imagePathPattern accepts paths like "master/characters/001_ludi.webp".
var imagePathPattern = regexp.MustCompile(`^[A-Za-z0-9_\-]+(/[A-Za-z0-9_\-]+)*\.(webp|png|jpe?g)$`)

func entityFor(r *http.Request) (string, entitySpec, error) {
	name := r.PathValue("entity")
	spec, found := entities[name]
	if !found {
		return "", spec, errNotFound("ไม่พบประเภทข้อมูล " + name)
	}
	return name, spec, nil
}

func validateRecord(rec *masterRecord) error {
	rec.NameTh, rec.NameEn = strings.TrimSpace(rec.NameTh), strings.TrimSpace(rec.NameEn)
	if !idPattern.MatchString(rec.ID) {
		return errValidation(fmt.Sprintf("id %q ต้องเป็นตัวเลข 1 ขึ้นไป", rec.ID))
	}
	if rec.NameTh == "" {
		return errValidation("id " + rec.ID + ": ต้องมีชื่อไทย (name_th)")
	}
	if rec.Image != nil && *rec.Image != "" && !imagePathPattern.MatchString(*rec.Image) {
		return errValidation("id " + rec.ID + ": path รูปไม่ถูกต้อง (เช่น master/characters/001_ludi.webp)")
	}
	return nil
}

// upsertRecord creates or updates by id. The id never changes (FR 9.2).
func upsertRecord(ctx context.Context, q querier, spec entitySpec, rec masterRecord) (created bool, err error) {
	active := true
	if rec.Active != nil {
		active = *rec.Active
	}
	cols := []string{"id", "name_th", "name_en", "active"}
	args := []any{rec.ID, rec.NameTh, rec.NameEn, active}
	if rec.Image != nil {
		var path any // empty clears the image
		if *rec.Image != "" {
			path = *rec.Image
		}
		cols, args = append(cols, "image_path"), append(args, path)
	}
	ph, sets := make([]string, len(cols)), make([]string, 0, len(cols))
	for i, c := range cols {
		ph[i] = "$" + strconv.Itoa(i+1)
		if c != "id" {
			sets = append(sets, c+" = EXCLUDED."+c)
		}
	}
	sql := fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s)
		ON CONFLICT (id) DO UPDATE SET %s, updated_at = now()
		RETURNING (xmax = 0)`, spec.table, strings.Join(cols, ", "), strings.Join(ph, ", "), strings.Join(sets, ", "))
	err = q.QueryRow(ctx, sql, args...).Scan(&created)
	return created, err
}

// PUT /api/admin/master/{entity}/{id}
func (s *Server) adminSaveMaster(w http.ResponseWriter, r *http.Request) error {
	me, err := requireAdmin(r, false)
	if err != nil {
		return err
	}
	entity, spec, err := entityFor(r)
	if err != nil {
		return err
	}
	var rec masterRecord
	if err := readJSON(r, &rec); err != nil {
		return err
	}
	rec.ID = r.PathValue("id")
	ctx := r.Context()
	if err := validateRecord(&rec); err != nil {
		return err
	}
	var created bool
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if created, err = upsertRecord(ctx, tx, spec, rec); err != nil {
			return err
		}
		action := "MASTER_UPDATED"
		if created {
			action = "MASTER_CREATED"
		}
		return audit(ctx, tx, me.ID, action, entity+"/"+rec.ID)
	})
	if err != nil {
		return err
	}
	return ok(w, map[string]bool{"created": created})
}

// POST /api/admin/master/{entity} — creates an item with the next free id (max + 1).
func (s *Server) adminCreateMaster(w http.ResponseWriter, r *http.Request) error {
	me, err := requireAdmin(r, false)
	if err != nil {
		return err
	}
	entity, spec, err := entityFor(r)
	if err != nil {
		return err
	}
	var rec masterRecord
	if err := readJSON(r, &rec); err != nil {
		return err
	}
	ctx := r.Context()
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		// Blocks a concurrent create (or import) from taking the same number.
		if _, err := tx.Exec(ctx, `LOCK TABLE `+spec.table+` IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT (coalesce(max(id::int), 0) + 1)::text FROM `+spec.table).Scan(&rec.ID); err != nil {
			return err
		}
		if err := validateRecord(&rec); err != nil {
			return err
		}
		if _, err := upsertRecord(ctx, tx, spec, rec); err != nil {
			return err
		}
		return audit(ctx, tx, me.ID, "MASTER_CREATED", entity+"/"+rec.ID)
	})
	if err != nil {
		return err
	}
	return ok(w, map[string]string{"id": rec.ID})
}

// PATCH /api/admin/master/{entity}/{id} {"active": bool}. Items are never deleted.
func (s *Server) adminSetMasterActive(w http.ResponseWriter, r *http.Request) error {
	me, err := requireAdmin(r, false)
	if err != nil {
		return err
	}
	entity, spec, err := entityFor(r)
	if err != nil {
		return err
	}
	var in struct {
		Active bool `json:"active"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	itemID := r.PathValue("id")
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE `+spec.table+` SET active = $2, updated_at = now() WHERE id = $1`, itemID, in.Active)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errNotFound("ไม่พบรายการ")
		}
		action := "MASTER_DEACTIVATE"
		if in.Active {
			action = "MASTER_ACTIVATE"
		}
		return audit(ctx, tx, me.ID, action, entity+"/"+itemID)
	})
	if err != nil {
		return err
	}
	return noContent(w)
}

var imageTypes = map[string]string{"image/png": ".png", "image/webp": ".webp", "image/jpeg": ".jpg"}

// POST /api/admin/master/{entity}/{id}/image (multipart, field "file", max 2 MB)
func (s *Server) adminUploadImage(w http.ResponseWriter, r *http.Request) error {
	me, err := requireAdmin(r, false)
	if err != nil {
		return err
	}
	entity, spec, err := entityFor(r)
	if err != nil {
		return err
	}
	itemID := r.PathValue("id")
	if !idPattern.MatchString(itemID) {
		return errNotFound("ไม่พบรายการ")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20+1024)
	file, _, err := r.FormFile("file")
	if err != nil {
		return errValidation("ต้องแนบไฟล์รูป (ไม่เกิน 2 MB)")
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return errValidation("ไฟล์ใหญ่เกิน 2 MB")
	}
	ext, allowed := imageTypes[http.DetectContentType(data)]
	if !allowed {
		return errValidation("รองรับเฉพาะ PNG, WebP และ JPEG")
	}
	rel := filepath.ToSlash(filepath.Join(entity, fmt.Sprintf("%s-%d%s", itemID, time.Now().Unix(), ext)))
	ctx := r.Context()
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+spec.table+` WHERE id = $1)`, itemID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errNotFound("ไม่พบรายการ")
	}
	stored, err := s.images.Put(ctx, rel, http.DetectContentType(data), data)
	if err != nil {
		return err
	}
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE `+spec.table+` SET image_path = $2, updated_at = now() WHERE id = $1`, itemID, stored); err != nil {
			return err
		}
		return audit(ctx, tx, me.ID, "MASTER_IMAGE", entity+"/"+itemID)
	})
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"image": imageURL(&stored)})
}

// ---------- CSV import (FR 9.3) ----------

type importRow struct {
	Line    int          `json:"line"`
	ID      string       `json:"id"`
	Status  string       `json:"status"` // CREATE | UPDATE | UNCHANGED | ERROR
	Changes []string     `json:"changes"`
	Error   string       `json:"error,omitempty"`
	Item    masterRecord `json:"item"`
}

// parseImport reads the CSV and diffs it against the database without writing anything.
func parseImport(ctx context.Context, q querier, entity string, spec entitySpec, text string) ([]importRow, error) {
	cr := csv.NewReader(strings.NewReader(strings.TrimPrefix(text, "\uFEFF")))
	cr.FieldsPerRecord = -1
	records, err := cr.ReadAll()
	if err != nil {
		return nil, errValidation("อ่านไฟล์ CSV ไม่ได้: " + err.Error())
	}
	if len(records) == 0 {
		return nil, errValidation("ไฟล์ว่าง")
	}
	header := map[string]int{}
	for i, h := range records[0] {
		header[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, c := range spec.columns {
		if _, found := header[c]; !found && !optionalColumns[c] {
			return nil, errValidation("ขาดคอลัมน์: " + c)
		}
	}
	m, err := loadMaster(ctx, q)
	if err != nil {
		return nil, err
	}
	existing := existingRecords(entity, m)
	seen := map[string]bool{}
	out := []importRow{}
	for i, cells := range records[1:] {
		get := func(col string) string {
			if idx, found := header[col]; found && idx < len(cells) {
				return strings.TrimSpace(cells[idx])
			}
			return ""
		}
		if strings.Join(cells, "") == "" {
			continue
		}
		row := importRow{Line: i + 2, ID: get("id"), Changes: []string{}}
		rec := masterRecord{ID: row.ID, NameTh: get("name_th"), NameEn: get("name_en")}
		if _, hasImage := header["image"]; hasImage && get("image") != "" {
			img := get("image")
			rec.Image = &img
		}
		cur, isUpdate := existing[rec.ID]
		if a := strings.ToLower(get("active")); a != "" {
			v := a == "true"
			rec.Active = &v
		} else if isUpdate {
			rec.Active = cur.Active // empty keeps the current state
		}
		switch {
		case seen[rec.ID]:
			row.Status, row.Error = "ERROR", "id ซ้ำในไฟล์"
		default:
			seen[rec.ID] = true
			if err := validateRecord(&rec); err != nil {
				var apiErr *Error
				if !errors.As(err, &apiErr) {
					return nil, err
				}
				row.Status, row.Error = "ERROR", apiErr.Message
			} else if !isUpdate {
				row.Status = "CREATE"
			} else if row.Changes = diffRecords(cur, rec); len(row.Changes) > 0 {
				row.Status = "UPDATE"
			} else {
				row.Status = "UNCHANGED"
			}
		}
		row.Item = rec
		out = append(out, row)
	}
	return out, nil
}

func existingRecords(entity string, m *MasterData) map[string]masterRecord {
	out := map[string]masterRecord{}
	b := func(it MasterItem) masterRecord {
		a := it.Active
		return masterRecord{ID: it.ID, NameTh: it.NameTh, NameEn: it.NameEn, Active: &a, Image: it.Image}
	}
	switch entity {
	case "characters":
		for _, x := range m.Characters {
			out[x.ID] = b(x)
		}
	case "pets":
		for _, x := range m.Pets {
			out[x.ID] = b(x)
		}
	case "sets":
		for _, x := range m.Sets {
			out[x.ID] = b(x)
		}
	case "rings":
		for _, x := range m.Rings {
			out[x.ID] = b(x)
		}
	case "stats":
		for _, x := range m.Stats {
			out[x.ID] = b(x)
		}
	}
	return out
}

func diffRecords(a, b masterRecord) []string {
	var ch []string
	if a.NameTh != b.NameTh {
		ch = append(ch, "nameTh")
	}
	if a.NameEn != b.NameEn {
		ch = append(ch, "nameEn")
	}
	if *a.Active != *b.Active {
		ch = append(ch, "active")
	}
	if b.Image != nil && ptrString(imageURL(a.Image)) != ptrString(imageURL(b.Image)) {
		ch = append(ch, "image")
	}
	return ch
}

// POST /api/admin/master/{entity}/import/preview {"csv": "..."} — step 1, writes nothing.
func (s *Server) adminPreviewImport(w http.ResponseWriter, r *http.Request) error {
	if _, err := requireAdmin(r, false); err != nil {
		return err
	}
	entity, spec, err := entityFor(r)
	if err != nil {
		return err
	}
	var in struct {
		CSV string `json:"csv"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	rows, err := parseImport(r.Context(), s.db, entity, spec, in.CSV)
	if err != nil {
		return err
	}
	return ok(w, rows)
}

// POST /api/admin/master/{entity}/import/commit {"csv": "..."} — step 2. Re-validates the same
// file inside one transaction and writes all rows or none.
func (s *Server) adminCommitImport(w http.ResponseWriter, r *http.Request) error {
	me, err := requireAdmin(r, false)
	if err != nil {
		return err
	}
	entity, spec, err := entityFor(r)
	if err != nil {
		return err
	}
	var in struct {
		CSV string `json:"csv"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	var created, updated int
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		rows, err := parseImport(ctx, tx, entity, spec, in.CSV)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Status == "ERROR" {
				return errValidation(fmt.Sprintf("แถว %d: %s", row.Line, row.Error))
			}
		}
		for _, row := range rows {
			if row.Status == "UNCHANGED" {
				continue
			}
			if _, err := upsertRecord(ctx, tx, spec, row.Item); err != nil {
				return err
			}
			if row.Status == "CREATE" {
				created++
			} else {
				updated++
			}
		}
		return audit(ctx, tx, me.ID, "MASTER_IMPORT", fmt.Sprintf("%s: เพิ่ม %d, แก้ %d", entity, created, updated))
	})
	if err != nil {
		return err
	}
	return ok(w, map[string]int{"created": created, "updated": updated})
}

// ---------- audit ----------

func (s *Server) adminAuditLog(w http.ResponseWriter, r *http.Request) error {
	if _, err := requireAdmin(r, true); err != nil {
		return err
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT a.id::text, coalesce(a.actor_id::text, ''), coalesce(u.display_name, 'Deleted user'), a.action, a.target, a.at
		FROM audit_log a LEFT JOIN users u ON u.id = a.actor_id ORDER BY a.at DESC LIMIT 500`)
	if err != nil {
		return err
	}
	type entry struct {
		ID        string    `json:"id"`
		ActorID   string    `json:"actorId"`
		ActorName string    `json:"actorName"`
		Action    string    `json:"action"`
		Target    string    `json:"target"`
		At        time.Time `json:"at"`
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[entry])
	if err != nil {
		return err
	}
	return ok(w, list)
}

func ptrString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
