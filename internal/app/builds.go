package app

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type buildInput struct {
	LineupID        string           `json:"lineupId"`
	Description     string           `json:"description"`
	Visibility      string           `json:"visibility"`
	GroupIDs        []string         `json:"groupIds"`
	CounterPets     []string         `json:"counterPets"`
	Formation       string           `json:"formation"`
	Placements      []Placement      `json:"placements"`
	OwnPets         []string         `json:"ownPets"`
	SkillOrder      []string         `json:"skillOrder"`
	Speed           *SpeedRule       `json:"speed"`
	Recommendations []Recommendation `json:"recommendations"`
}

func (in *buildInput) team() Team {
	return Team{Placements: in.Placements, OwnPets: nz(in.OwnPets), SkillOrder: nz(in.SkillOrder), Speed: in.Speed, Recommendations: in.Recommendations}
}

func nz[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// validateBuild enforces FR 4.1–4.3 against master data. isNew requires active items for
// anything newly referenced; edits may keep items that were deactivated later.
func validateBuild(ctx context.Context, q querier, ix *masterIndex, userID string, in *buildInput, isNew bool) error {
	if utf8.RuneCountInString(in.Description) > 2000 {
		return errValidation("คำอธิบายยาวเกิน 2000 ตัวอักษร")
	}
	if err := validatePlacements(ix, in.Formation, in.Placements, isNew); err != nil {
		return err
	}
	placed := map[string]bool{}
	for _, p := range in.Placements {
		placed[p.CharacterID] = true
	}
	if len(in.SkillOrder) > 3 {
		return errValidation("จองสกิลได้สูงสุด 3 ลำดับ")
	}
	if hasDuplicates(in.SkillOrder) {
		return errValidation("จองสกิลเดียวกันซ้ำไม่ได้")
	}
	for _, id := range in.SkillOrder {
		sk, found := ix.skills[id]
		if !found || !placed[sk.CharacterID] {
			return errValidation("สกิลต้องเป็นของตัวละครในทีม")
		}
	}
	for _, list := range [][]string{in.OwnPets, in.CounterPets} {
		if hasDuplicates(list) {
			return errValidation("Pet ซ้ำกัน")
		}
		for _, id := range list {
			if _, found := ix.pets[id]; !found {
				return errValidation("ไม่พบ Pet " + id)
			}
		}
	}
	if in.Speed != nil {
		switch {
		case in.Speed.Kind == "FASTER":
		case in.Speed.Kind == "MIN" && in.Speed.Value > 0:
		default:
			return errValidation("เงื่อนไข Speed ไม่ถูกต้อง")
		}
	}
	if len(in.Recommendations) != len(in.Placements) {
		return errValidation("คำแนะนำต้องครบทุกตัวละคร")
	}
	seenRec := map[string]bool{}
	for _, rec := range in.Recommendations {
		if !placed[rec.CharacterID] || seenRec[rec.CharacterID] {
			return errValidation("คำแนะนำต้องครบทุกตัวละคร")
		}
		seenRec[rec.CharacterID] = true
		if hasDuplicates(rec.Sets) {
			return errValidation("เซ็ตซ้ำกัน")
		}
		for _, id := range rec.Sets {
			if _, found := ix.sets[id]; !found {
				return errValidation("ไม่พบเซ็ต " + id)
			}
		}
		if len(rec.Stats) > 4 {
			return errValidation("สเตตัสได้สูงสุด 4 ตัวต่อตัวละคร")
		}
		statIDs := make([]string, len(rec.Stats))
		for i, st := range rec.Stats {
			if _, found := ix.stats[st.StatID]; !found {
				return errValidation("ไม่พบสเตตัส " + st.StatID)
			}
			if st.Value != nil && (*st.Value < 0 || *st.Value > 100000) {
				return errValidation("ค่าสเตตัสไม่ถูกต้อง")
			}
			statIDs[i] = st.StatID
		}
		if hasDuplicates(statIDs) {
			return errValidation("สเตตัสซ้ำกัน")
		}
		if len(rec.Rings) > 10 {
			return errValidation("ตัวเลือกแหวนมากเกินไป")
		}
		for _, rg := range rec.Rings {
			if _, found := ix.rings[rg.RingID]; !found || rg.Star < 4 || rg.Star > 6 {
				return errValidation("แหวนไม่ถูกต้อง")
			}
			if rg.Stack != nil {
				if _, found := ix.rings[rg.Stack.RingID]; !found || rg.Stack.RingID == rg.RingID {
					return errValidation("แหวนซ้อนต้องเป็นแหวนวงอื่น")
				}
				if rg.Stack.Star != nil && (*rg.Stack.Star < 4 || *rg.Stack.Star > 6) {
					return errValidation("ดาวแหวนซ้อนไม่ถูกต้อง")
				}
			} else if rec.RingStackRequired {
				return errValidation("ตัวเลือกแหวนทุกตัวต้องมีแหวนซ้อน")
			}
		}
		if utf8.RuneCountInString(rec.Note) > 200 {
			return errValidation("หมายเหตุยาวเกิน 200 ตัวอักษร")
		}
	}
	switch in.Visibility {
	case "PUBLIC", "PRIVATE":
	case "GROUP":
		if len(in.GroupIDs) == 0 {
			return errValidation("เลือกอย่างน้อย 1 Group")
		}
		if hasDuplicates(in.GroupIDs) {
			return errValidation("Group ซ้ำกัน")
		}
		var n int
		if err := q.QueryRow(ctx, `SELECT count(*) FROM group_members WHERE user_id = $1 AND group_id = ANY($2::uuid[])`, userID, in.GroupIDs).Scan(&n); err != nil {
			return err
		}
		if n != len(in.GroupIDs) {
			return errValidation("แชร์ได้เฉพาะ Group ที่คุณเป็นสมาชิก")
		}
	default:
		return errValidation("Visibility ไม่ถูกต้อง")
	}
	return nil
}

func (s *Server) readBuildInput(r *http.Request) (*buildInput, *masterIndex, error) {
	var in buildInput
	if err := readJSON(r, &in); err != nil {
		return nil, nil, err
	}
	in.CounterPets, in.GroupIDs = nz(in.CounterPets), nz(in.GroupIDs)
	m, err := loadMaster(r.Context(), s.db)
	if err != nil {
		return nil, nil, err
	}
	return &in, indexMaster(m), nil
}

func (s *Server) buildDetail(ctx context.Context, u *User, id string) (*BuildDetail, error) {
	b, err := s.findVisibleBuild(ctx, s.db, u, id)
	if err != nil {
		return nil, err
	}
	sums, err := s.summaries(ctx, s.db, u, []buildRow{*b})
	if err != nil {
		return nil, err
	}
	lineup, err := loadLineup(ctx, s.db, b.LineupID)
	if err != nil {
		return nil, err
	}
	all, err := voteCounts(ctx, s.db, []string{b.ID}, timeZero)
	if err != nil {
		return nil, err
	}
	d := &BuildDetail{BuildSummary: sums[0], Lineup: *lineup, StatsAll: buildStats("ALL", all[b.ID])}
	if u != nil {
		var v Vote
		err := s.db.QueryRow(ctx, `SELECT id, build_id, user_id, result, equip_level, voted_at FROM votes WHERE build_id = $1 AND user_id = $2`, b.ID, u.ID).
			Scan(&v.ID, &v.BuildID, &v.UserID, &v.Result, &v.EquipLevel, &v.VotedAt)
		if err == nil {
			d.MyVote = &v
		} else if err != pgx.ErrNoRows {
			return nil, err
		}
		d.CanEdit = d.IsMine && !b.CreatorDeleted
		d.CanVote = u.EmailVerified
	}
	return d, nil
}

// ---------- handlers ----------

const (
	defaultPageSize = 9 // 3 rows of 3 cards: one screen on a laptop
	maxPageSize     = 50
)

type buildListPage struct {
	Items    []BuildSummary `json:"items"`
	Total    int            `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"pageSize"`
	// Enemy pets countered by any build the viewer can see here (for the filter chips).
	CounterPets []string `json:"counterPets"`
}

func intParam(r *http.Request, key string, def, lo, hi int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return def
	}
	return max(lo, min(v, hi))
}

// GET /api/lineups/{id}/builds?enemyPet=&sort=WIN14|VOTES|NEWEST&page=1&pageSize=12
//
// Sorting uses vote stats, so all visible builds of the lineup are ranked in memory and then
// paged. Fine for a lineup's build count; revisit with a stats table if lineups grow huge.
func (s *Server) listLineupBuilds(w http.ResponseWriter, r *http.Request) error {
	u := viewer(r)
	vid, admin := viewerArgs(u)
	visible, err := loadBuilds(r.Context(), s.db, `WHERE b.lineup_id = $1::uuid AND `+visibleSQL("$2", "$3"), r.PathValue("id"), vid, admin)
	if err != nil {
		return err
	}
	pet := r.URL.Query().Get("enemyPet")
	petSet := map[string]bool{}
	var list []buildRow
	for _, b := range visible {
		for _, p := range b.CounterPets {
			petSet[p] = true
		}
		// A build with no counter pets works against any enemy pet (FR 4.2).
		if pet == "" || len(b.CounterPets) == 0 || contains(b.CounterPets, pet) {
			list = append(list, b)
		}
	}
	sums, err := s.summaries(r.Context(), s.db, u, list)
	if err != nil {
		return err
	}
	sortSummaries(sums, r.URL.Query().Get("sort"))
	pageSize := intParam(r, "pageSize", defaultPageSize, 1, maxPageSize)
	page := intParam(r, "page", 1, 1, 1_000_000)
	from, to := min((page-1)*pageSize, len(sums)), min(page*pageSize, len(sums))
	pets := make([]string, 0, len(petSet))
	for p := range petSet {
		pets = append(pets, p)
	}
	sort.Strings(pets)
	return ok(w, buildListPage{Items: sums[from:to], Total: len(sums), Page: page, PageSize: pageSize, CounterPets: pets})
}

func (s *Server) listMyBuilds(w http.ResponseWriter, r *http.Request) error {
	u, err := requireUser(r)
	if err != nil {
		return err
	}
	list, err := loadBuilds(r.Context(), s.db, `WHERE b.creator_id = $1 ORDER BY b.updated_at DESC`, u.ID)
	if err != nil {
		return err
	}
	sums, err := s.summaries(r.Context(), s.db, u, list)
	if err != nil {
		return err
	}
	type mine struct {
		BuildSummary
		LineupID string `json:"lineupId"`
	}
	out := make([]mine, len(sums))
	for i, sm := range sums {
		out[i] = mine{sm, sm.Build.LineupID}
	}
	return ok(w, out)
}

func (s *Server) getBuild(w http.ResponseWriter, r *http.Request) error {
	d, err := s.buildDetail(r.Context(), viewer(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	return ok(w, d)
}

func (s *Server) createBuild(w http.ResponseWriter, r *http.Request) error {
	u, err := requireVerified(r)
	if err != nil {
		return err
	}
	in, ix, err := s.readBuildInput(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	if _, err := loadLineup(ctx, s.db, in.LineupID); err != nil {
		return errValidation("ไม่พบบ้านศัตรู")
	}
	if err := validateBuild(ctx, s.db, ix, u.ID, in, true); err != nil {
		return err
	}
	team, _ := json.Marshal(in.team())
	var id string
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO builds (lineup_id, creator_id, visibility, description, counter_pets, formation, team)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
			in.LineupID, u.ID, in.Visibility, in.Description, in.CounterPets, in.Formation, team).Scan(&id); err != nil {
			return err
		}
		if in.Visibility == "GROUP" {
			for _, gid := range in.GroupIDs {
				if _, err := tx.Exec(ctx, `INSERT INTO build_shares (build_id, group_id) VALUES ($1, $2)`, id, gid); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	d, err := s.buildDetail(ctx, u, id)
	if err != nil {
		return err
	}
	return ok(w, d.Build)
}

func (s *Server) updateBuild(w http.ResponseWriter, r *http.Request) error {
	u, err := requireVerified(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	b, err := s.findVisibleBuild(ctx, s.db, u, r.PathValue("id"))
	if err != nil {
		return err
	}
	if b.CreatorID == nil || *b.CreatorID != u.ID || b.CreatorDeleted {
		return errForbidden("แก้ไขได้เฉพาะผู้สร้าง")
	}
	in, ix, err := s.readBuildInput(r)
	if err != nil {
		return err
	}
	if in.LineupID != b.LineupID {
		return errValidation("เปลี่ยนบ้านศัตรูของ Build ไม่ได้")
	}
	if err := validateBuild(ctx, s.db, ix, u.ID, in, false); err != nil {
		return err
	}
	team, _ := json.Marshal(in.team())
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE builds SET visibility = $2, description = $3, counter_pets = $4, formation = $5, team = $6, updated_at = now()
			WHERE id = $1`, b.ID, in.Visibility, in.Description, in.CounterPets, in.Formation, team); err != nil {
			return err
		}
		// Changing the visibility type ends every share; adding or removing groups keeps the
		// hidden flag of the groups that stay (FR 4.5).
		if in.Visibility != "GROUP" || b.Visibility != "GROUP" {
			if _, err := tx.Exec(ctx, `DELETE FROM build_shares WHERE build_id = $1`, b.ID); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `DELETE FROM build_shares WHERE build_id = $1 AND NOT (group_id = ANY($2::uuid[]))`, b.ID, in.GroupIDs); err != nil {
			return err
		}
		if in.Visibility == "GROUP" {
			for _, gid := range in.GroupIDs {
				if _, err := tx.Exec(ctx, `INSERT INTO build_shares (build_id, group_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, b.ID, gid); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	d, err := s.buildDetail(ctx, u, b.ID)
	if err != nil {
		return err
	}
	return ok(w, d.Build)
}

func (s *Server) deleteBuild(w http.ResponseWriter, r *http.Request) error {
	u, err := requireUser(r)
	if err != nil {
		return err
	}
	b, err := s.findVisibleBuild(r.Context(), s.db, u, r.PathValue("id"))
	if err != nil {
		return err
	}
	if b.CreatorID == nil || *b.CreatorID != u.ID || b.CreatorDeleted {
		return errForbidden("ลบได้เฉพาะผู้สร้าง")
	}
	// Votes and shares are removed by ON DELETE CASCADE (FR 4.7).
	if _, err := s.db.Exec(r.Context(), `DELETE FROM builds WHERE id = $1`, b.ID); err != nil {
		return err
	}
	return noContent(w)
}

// ---------- votes (FR 6) ----------

// PUT /api/builds/{id}/vote — creates or replaces the viewer's single vote. The UI asks for
// confirmation before replacing; the API simply upserts.
func (s *Server) castVote(w http.ResponseWriter, r *http.Request) error {
	u, err := requireVerified(r)
	if err != nil {
		return err
	}
	var in struct {
		Result     string `json:"result"`
		EquipLevel string `json:"equipLevel"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.Result != "WIN" && in.Result != "LOSS" {
		return errValidation("ผลการต่อสู้ไม่ถูกต้อง")
	}
	if !contains(equipLevels, in.EquipLevel) {
		return errValidation("ระดับอุปกรณ์ไม่ถูกต้อง")
	}
	b, err := s.findVisibleBuild(r.Context(), s.db, u, r.PathValue("id"))
	if err != nil {
		return err
	}
	var v Vote
	err = s.db.QueryRow(r.Context(), `
		INSERT INTO votes (build_id, user_id, result, equip_level) VALUES ($1, $2, $3, $4)
		ON CONFLICT (build_id, user_id) WHERE user_id IS NOT NULL
		DO UPDATE SET result = EXCLUDED.result, equip_level = EXCLUDED.equip_level, voted_at = now()
		RETURNING id, build_id, user_id, result, equip_level, voted_at`, b.ID, u.ID, in.Result, in.EquipLevel).
		Scan(&v.ID, &v.BuildID, &v.UserID, &v.Result, &v.EquipLevel, &v.VotedAt)
	if err != nil {
		return err
	}
	return ok(w, v)
}

func (s *Server) cancelVote(w http.ResponseWriter, r *http.Request) error {
	u, err := requireUser(r)
	if err != nil {
		return err
	}
	b, err := s.findVisibleBuild(r.Context(), s.db, u, r.PathValue("id"))
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(r.Context(), `DELETE FROM votes WHERE build_id = $1 AND user_id = $2`, b.ID, u.ID); err != nil {
		return err
	}
	return noContent(w)
}
