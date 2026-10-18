package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

var rowSizes = map[string][2]int{ // formation -> [front, back]
	"F3B2": {3, 2},
	"F2B3": {2, 3},
	"F1B4": {1, 4},
	"F4B1": {4, 1},
}

func rowSize(formation, row string) int {
	sz := rowSizes[formation]
	if row == "FRONT" {
		return sz[0]
	}
	return sz[1]
}

// validatePlacements checks 3 distinct characters in distinct, existing slots.
// requireActive is true when creating something new with these characters.
func validatePlacements(ix *masterIndex, formation string, ps []Placement, requireActive bool) error {
	if _, ok := rowSizes[formation]; !ok {
		return errValidation("Formation ไม่ถูกต้อง")
	}
	if len(ps) != 3 {
		return errValidation("ต้องมีตัวละคร 3 ตัว")
	}
	chars, slots := map[string]bool{}, map[string]bool{}
	for _, p := range ps {
		if p.Row != "FRONT" && p.Row != "BACK" {
			return errValidation("แถวไม่ถูกต้อง")
		}
		if p.Slot < 0 || p.Slot >= rowSize(formation, p.Row) {
			return errValidation("ตำแหน่งไม่อยู่ใน Formation")
		}
		c, found := ix.characters[p.CharacterID]
		if !found {
			return errValidation("ไม่พบตัวละคร " + p.CharacterID)
		}
		if requireActive && !c.Active {
			return errValidation("ตัวละคร " + c.NameTh + " ปิดใช้งานแล้ว")
		}
		key := fmt.Sprintf("%s:%d", p.Row, p.Slot)
		if chars[p.CharacterID] || slots[key] {
			return errValidation("ตัวละครหรือตำแหน่งซ้ำกัน")
		}
		chars[p.CharacterID], slots[key] = true, true
	}
	return nil
}

// lineupKey: same formation + same characters per row, order within a row ignored (FR 3.2).
func lineupKey(formation string, ps []Placement) string {
	ids := func(row string) string {
		var out []string
		for _, p := range ps {
			if p.Row == row {
				out = append(out, p.CharacterID)
			}
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	return formation + "|F:" + ids("FRONT") + "|B:" + ids("BACK")
}

const lineupSelect = `
SELECT l.id, l.formation, l.created_by, l.created_at,
       (SELECT jsonb_agg(jsonb_build_object('characterId', p.character_id, 'row', p.row, 'slot', p.slot) ORDER BY p.row, p.slot)
        FROM lineup_placements p WHERE p.lineup_id = l.id)
FROM lineups l`

func scanLineup(row pgx.CollectableRow) (DefenseLineup, error) {
	var l DefenseLineup
	var ps []byte
	if err := row.Scan(&l.ID, &l.Formation, &l.CreatedBy, &l.CreatedAt, &ps); err != nil {
		return l, err
	}
	return l, json.Unmarshal(ps, &l.Placements)
}

func loadLineup(ctx context.Context, q querier, id string) (*DefenseLineup, error) {
	rows, err := q.Query(ctx, lineupSelect+` WHERE l.id = $1::uuid`, id)
	if err != nil {
		return nil, err
	}
	l, err := pgx.CollectExactlyOneRow(rows, scanLineup)
	if err != nil {
		return nil, errNotFound("ไม่พบบ้านศัตรู")
	}
	return &l, nil
}

func insertPlacements(ctx context.Context, tx pgx.Tx, lineupID string, ps []Placement) error {
	for _, p := range ps {
		if _, err := tx.Exec(ctx, `INSERT INTO lineup_placements (lineup_id, character_id, row, slot) VALUES ($1, $2, $3, $4)`,
			lineupID, p.CharacterID, p.Row, p.Slot); err != nil {
			return err
		}
	}
	return nil
}

// ---------- handlers ----------

// GET /api/lineups?characters=a,b — lineups containing all given characters.
func (s *Server) listLineups(w http.ResponseWriter, r *http.Request) error {
	chars := []string{} // not nil: a NULL array would make cardinality() NULL and match nothing
	if c := r.URL.Query().Get("characters"); c != "" {
		chars = strings.Split(c, ",")
	}
	vid, admin := viewerArgs(viewer(r))
	rows, err := s.db.Query(r.Context(), `
		SELECT l.id, l.formation, l.created_by, l.created_at,
		       (SELECT jsonb_agg(jsonb_build_object('characterId', p.character_id, 'row', p.row, 'slot', p.slot) ORDER BY p.row, p.slot)
		        FROM lineup_placements p WHERE p.lineup_id = l.id),
		       -- Count only builds this viewer may see (FR 4.6).
		       (SELECT count(*) FROM builds b WHERE b.lineup_id = l.id AND `+visibleSQL("$2", "$3")+`)
		FROM lineups l
		WHERE cardinality($1::text[]) = 0 OR (
		  SELECT count(DISTINCT p.character_id) FROM lineup_placements p
		  WHERE p.lineup_id = l.id AND p.character_id = ANY($1::text[])) = cardinality($1::text[])
		ORDER BY 6 DESC, l.created_at DESC
		LIMIT 200`, chars, vid, admin)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (LineupSummary, error) {
		var out LineupSummary
		var ps []byte
		if err := row.Scan(&out.Lineup.ID, &out.Lineup.Formation, &out.Lineup.CreatedBy, &out.Lineup.CreatedAt, &ps, &out.VisibleBuildCount); err != nil {
			return out, err
		}
		return out, json.Unmarshal(ps, &out.Lineup.Placements)
	})
	if err != nil {
		return err
	}
	return ok(w, list)
}

func (s *Server) getLineup(w http.ResponseWriter, r *http.Request) error {
	l, err := loadLineup(r.Context(), s.db, r.PathValue("id"))
	if err != nil {
		return err
	}
	return ok(w, l)
}

// POST /api/lineups — creates a lineup, or returns the existing one if it is a duplicate.
func (s *Server) createLineup(w http.ResponseWriter, r *http.Request) error {
	u, err := requireUser(r)
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
	m, err := loadMaster(ctx, s.db)
	if err != nil {
		return err
	}
	if err := validatePlacements(indexMaster(m), in.Formation, in.Placements, true); err != nil {
		return err
	}
	key := lineupKey(in.Formation, in.Placements)
	var id string
	existed := false
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		// ON CONFLICT makes concurrent creation of the same lineup safe.
		err := tx.QueryRow(ctx, `INSERT INTO lineups (formation, canonical_key, created_by) VALUES ($1, $2, $3)
			ON CONFLICT (canonical_key) DO NOTHING RETURNING id`, in.Formation, key, u.ID).Scan(&id)
		if err == pgx.ErrNoRows {
			existed = true
			return tx.QueryRow(ctx, `SELECT id FROM lineups WHERE canonical_key = $1`, key).Scan(&id)
		}
		if err != nil {
			return err
		}
		return insertPlacements(ctx, tx, id, in.Placements)
	})
	if err != nil {
		return err
	}
	l, err := loadLineup(ctx, s.db, id)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"lineup": l, "existed": existed})
}
