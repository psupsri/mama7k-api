package app

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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
		WHERE deleted_at IS NULL AND (email ILIKE $1 OR display_name ILIKE $1) ORDER BY created_at LIMIT 200`, q)
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
			if err := audit(ctx, tx, me.ID, action, target.DisplayName+" ("+target.Email+")"); err != nil {
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
		  OR EXISTS (SELECT 1 FROM characters c WHERE c.name ILIKE $1
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
	prefix  string
	columns []string // CSV columns, in template order
}

var entities = map[string]entitySpec{
	"characters": {"characters", "char_", []string{"code", "name", "aliases", "active"}},
	"skills":     {"skills", "skill_", []string{"code", "character_code", "slot", "name", "aliases", "active"}},
	"pets":       {"pets", "pet_", []string{"code", "name", "aliases", "active"}},
	"sets":       {"equipment_sets", "set_", []string{"code", "name", "aliases", "effect_2", "effect_4", "active"}},
	"rings":      {"rings", "ring_", []string{"code", "name", "aliases", "rarity", "effect", "active"}},
	"stats":      {"stat_types", "stat_", []string{"code", "name", "aliases", "unit", "active"}},
}

var codePattern = regexp.MustCompile(`^[a-z0-9_]+$`)

// masterRecord is one master item in a format-neutral shape (from JSON or a CSV row).
type masterRecord struct {
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases"`
	Active      *bool    `json:"active"`
	CharacterID string   `json:"characterId,omitempty"`
	Slot        int      `json:"slot,omitempty"`
	Effect2     string   `json:"effect2,omitempty"`
	Effect4     string   `json:"effect4,omitempty"`
	Rarity      string   `json:"rarity,omitempty"`
	Effect      string   `json:"effect,omitempty"`
	Unit        string   `json:"unit,omitempty"`
}

func entityFor(r *http.Request) (string, entitySpec, error) {
	name := r.PathValue("entity")
	spec, found := entities[name]
	if !found {
		return "", spec, errNotFound("ไม่พบประเภทข้อมูล " + name)
	}
	return name, spec, nil
}

func validateRecord(ctx context.Context, q querier, entity string, spec entitySpec, rec *masterRecord) error {
	rec.Name = strings.TrimSpace(rec.Name)
	if !codePattern.MatchString(rec.Code) || !strings.HasPrefix(rec.Code, spec.prefix) {
		return errValidation(fmt.Sprintf("code %q ต้องขึ้นต้นด้วย %s และใช้ได้เฉพาะ a-z 0-9 _", rec.Code, spec.prefix))
	}
	if rec.Name == "" {
		return errValidation(rec.Code + ": ต้องมีชื่อ")
	}
	if rec.Aliases == nil {
		rec.Aliases = []string{}
	}
	switch entity {
	case "skills":
		if rec.Slot != 1 && rec.Slot != 2 {
			return errValidation(rec.Code + ": slot ต้องเป็น 1 (บน) หรือ 2 (ล่าง)")
		}
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM characters WHERE id = $1)`, rec.CharacterID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return errValidation(rec.Code + ": ไม่พบตัวละครเจ้าของสกิล")
		}
	case "rings":
		if !contains([]string{"LEGENDARY", "RARE", "UNCOMMON", "COMMON"}, rec.Rarity) {
			return errValidation(rec.Code + ": rarity ไม่ถูกต้อง")
		}
	case "stats":
		if rec.Unit != "PERCENT" && rec.Unit != "NUMBER" {
			return errValidation(rec.Code + ": unit ไม่ถูกต้อง")
		}
	}
	return nil
}

// upsertRecord creates or updates by code. The code is the id and never changes (FR 9.2).
func upsertRecord(ctx context.Context, q querier, entity string, spec entitySpec, rec masterRecord) (created bool, err error) {
	active := true
	if rec.Active != nil {
		active = *rec.Active
	}
	cols := []string{"id", "name", "aliases", "active"}
	args := []any{rec.Code, rec.Name, rec.Aliases, active}
	switch entity {
	case "skills":
		cols, args = append(cols, "character_id", "slot"), append(args, rec.CharacterID, rec.Slot)
	case "sets":
		cols, args = append(cols, "effect_2", "effect_4"), append(args, rec.Effect2, rec.Effect4)
	case "rings":
		cols, args = append(cols, "rarity", "effect"), append(args, rec.Rarity, rec.Effect)
	case "stats":
		cols, args = append(cols, "unit"), append(args, rec.Unit)
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

// PUT /api/admin/master/{entity}/{code}
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
	rec.Code = r.PathValue("code")
	ctx := r.Context()
	if err := validateRecord(ctx, s.db, entity, spec, &rec); err != nil {
		return err
	}
	var created bool
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if created, err = upsertRecord(ctx, tx, entity, spec, rec); err != nil {
			return err
		}
		action := "MASTER_UPDATED"
		if created {
			action = "MASTER_CREATED"
		}
		return audit(ctx, tx, me.ID, action, entity+"/"+rec.Code)
	})
	if err != nil {
		return err
	}
	return ok(w, map[string]bool{"created": created})
}

// PATCH /api/admin/master/{entity}/{code} {"active": bool}. Items are never deleted.
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
	code := r.PathValue("code")
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE `+spec.table+` SET active = $2, updated_at = now() WHERE id = $1`, code, in.Active)
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
		return audit(ctx, tx, me.ID, action, entity+"/"+code)
	})
	if err != nil {
		return err
	}
	return noContent(w)
}

var imageTypes = map[string]string{"image/png": ".png", "image/webp": ".webp", "image/jpeg": ".jpg"}

// POST /api/admin/master/{entity}/{code}/image (multipart, field "file", max 2 MB)
func (s *Server) adminUploadImage(w http.ResponseWriter, r *http.Request) error {
	me, err := requireAdmin(r, false)
	if err != nil {
		return err
	}
	entity, spec, err := entityFor(r)
	if err != nil {
		return err
	}
	code := r.PathValue("code")
	if !codePattern.MatchString(code) {
		return errValidation("code ไม่ถูกต้อง")
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
	rel := filepath.ToSlash(filepath.Join(entity, fmt.Sprintf("%s-%d%s", code, time.Now().Unix(), ext)))
	ctx := r.Context()
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+spec.table+` WHERE id = $1)`, code).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errNotFound("ไม่พบรายการ")
	}
	dst := filepath.Join(s.cfg.UploadDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return err
	}
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE `+spec.table+` SET image_path = $2, updated_at = now() WHERE id = $1`, code, rel); err != nil {
			return err
		}
		return audit(ctx, tx, me.ID, "MASTER_IMAGE", entity+"/"+code)
	})
	if err != nil {
		return err
	}
	return ok(w, map[string]string{"image": "/media/" + rel})
}

// ---------- CSV import (FR 9.3) ----------

type importRow struct {
	Line    int          `json:"line"`
	Code    string       `json:"code"`
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
		if _, found := header[c]; !found && c != "active" && c != "aliases" {
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
		row := importRow{Line: i + 2, Code: get("code"), Changes: []string{}}
		rec := masterRecord{Code: row.Code, Name: get("name"), CharacterID: get("character_code"),
			Effect2: get("effect_2"), Effect4: get("effect_4"), Rarity: strings.ToUpper(get("rarity")),
			Effect: get("effect"), Unit: strings.ToUpper(get("unit"))}
		for _, a := range strings.Split(get("aliases"), "|") {
			if a = strings.TrimSpace(a); a != "" {
				rec.Aliases = append(rec.Aliases, a)
			}
		}
		rec.Slot, _ = strconv.Atoi(get("slot"))
		cur, isUpdate := existing[rec.Code]
		if a := strings.ToLower(get("active")); a != "" {
			v := a == "true"
			rec.Active = &v
		} else if isUpdate {
			rec.Active = cur.Active // empty keeps the current state
		}
		switch {
		case seen[rec.Code]:
			row.Status, row.Error = "ERROR", "code ซ้ำในไฟล์"
		default:
			seen[rec.Code] = true
			if err := validateRecord(ctx, q, entity, spec, &rec); err != nil {
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
		return masterRecord{Code: it.ID, Name: it.Name, Aliases: it.Aliases, Active: &a}
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
	case "skills":
		for _, x := range m.Skills {
			r := b(x.MasterItem)
			r.CharacterID, r.Slot = x.CharacterID, x.Slot
			out[x.ID] = r
		}
	case "sets":
		for _, x := range m.Sets {
			r := b(x.MasterItem)
			r.Effect2, r.Effect4 = x.Effect2, x.Effect4
			out[x.ID] = r
		}
	case "rings":
		for _, x := range m.Rings {
			r := b(x.MasterItem)
			r.Rarity, r.Effect = x.Rarity, x.Effect
			out[x.ID] = r
		}
	case "stats":
		for _, x := range m.Stats {
			r := b(x.MasterItem)
			r.Unit = x.Unit
			out[x.ID] = r
		}
	}
	return out
}

func diffRecords(a, b masterRecord) []string {
	var ch []string
	if a.Name != b.Name {
		ch = append(ch, "name")
	}
	if strings.Join(a.Aliases, "|") != strings.Join(b.Aliases, "|") {
		ch = append(ch, "aliases")
	}
	if *a.Active != *b.Active {
		ch = append(ch, "active")
	}
	for _, f := range []struct {
		name string
		x, y string
	}{{"characterId", a.CharacterID, b.CharacterID}, {"effect2", a.Effect2, b.Effect2}, {"effect4", a.Effect4, b.Effect4},
		{"rarity", a.Rarity, b.Rarity}, {"effect", a.Effect, b.Effect}, {"unit", a.Unit, b.Unit}} {
		if f.x != f.y {
			ch = append(ch, f.name)
		}
	}
	if a.Slot != b.Slot {
		ch = append(ch, "slot")
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
			if _, err := upsertRecord(ctx, tx, entity, spec, row.Item); err != nil {
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
