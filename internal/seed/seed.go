// Package seed loads sample accounts, groups and game data, with deterministic ids so the
// integration tests can refer to "b1", "the outsider", etc.
package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// Password is the password of every seeded user.
const Password = "password123"

// Deterministic ids (u1, g1, l1, b1 … mapped onto valid UUIDs).
func UserID(n int) string   { return fmt.Sprintf("00000000-0000-0000-0000-0000000000c%d", n) }
func GroupID(n int) string  { return fmt.Sprintf("00000000-0000-0000-0000-0000000000d%d", n) }
func LineupID(n int) string { return fmt.Sprintf("00000000-0000-0000-0000-0000000000a%d", n) }
func BuildID(n int) string  { return fmt.Sprintf("00000000-0000-0000-0000-%012x", 0xb0+n) }

// Reset empties every table (keeps the schema) and loads accounts, groups and the sample
// game data (master data, lineups, builds, votes). The integration tests rely on it.
func Reset(ctx context.Context, pool *pgxpool.Pool) error { return reset(ctx, pool, true) }

// ResetAccounts empties every table and loads only the sample users and groups, leaving
// master data empty for real data to be entered through the admin pages or CSV import.
func ResetAccounts(ctx context.Context, pool *pgxpool.Pool) error { return reset(ctx, pool, false) }

func reset(ctx context.Context, pool *pgxpool.Pool, sample bool) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `TRUNCATE audit_log, votes, build_shares, builds, lineup_placements, lineups,
			group_members, groups, email_tokens, sessions, users, stat_types, rings, equipment_sets, pets, characters
			RESTART IDENTITY CASCADE`); err != nil {
			return err
		}
		return load(ctx, tx, sample)
	})
}

// Sample master data. Rows are {slug, name, name_en, …}; the slug only names the row in this
// file, and the database id is its position in the list (1, 2, …), as with real data.
var characters = [][3]string{
	{"yungon", "ยุนกอน", "Yu Shin"}, {"skull", "สกัล", "Skull"}, {"doyoung", "โดยอง", "Do Young"},
	{"lubu", "ลิโป้", "Lu Bu"}, {"hayeon", "ฮายอน", "Ha Yeon"}, {"soran", "ซอรัน", "Soran"},
	{"rudy", "รูดี้", "Rudy"}, {"evan", "อีวาน", "Evan"}, {"karma", "คาร์ม่า", "Karma"},
	{"hellenia", "เฮเลเนีย", "Hellenia"}, {"lucy", "ลูซี่", "Lucy"}, {"shane", "เชน", "Shane"},
	{"pascal", "ปาสคาล", "Pascal"}, {"fai", "ไฟ", "Fai"}, {"bidam", "บีดัม", "Bi Dam"},
	{"kyle", "ไคล์", "Kyle"}, {"biscuit", "บิสกิต", "Biscuit"}, {"teo", "เทโอ", "Teo"},
}

var pets = [][3]string{{"taeo", "แทโอ", ""}, {"yu", "ยู", ""}, {"ari", "อาริ", ""}, {"pet4", "Pet 4", ""}, {"pet5", "Pet 5", ""}, {"pet6", "Pet 6", ""}}

var equipmentSets = [][3]string{
	{"attack", "เซ็ตโจม", "Vanguard"}, {"pierce", "เซ็ตเจาะเกราะ", "Bounty Tracker"}, {"hp", "เซ็ตเลือด", "Paladin"},
	{"hit", "เซ็ตเข้าเป้า", "Spellweaver"}, {"boss", "เซ็ตบอส", "Avenger"}, {"resist", "เซ็ตต้าน", "Orchestrator"},
	{"block", "เซ็ตบล็อค", "Gatekeeper"}, {"crit", "เซ็ตคริ", "Assassin"}, {"def", "เซ็ตป้องกัน", "Guardian"},
}

var rings = [][3]string{
	{"authority", "อำนาจ", "Ring of Authority"}, {"immortality", "อมตะ", "Ring of Immortality"},
	{"resurrection", "ชุบ", "Ring of Resurrection"}, {"subjugation", "ปราบปราม", "Ring of Subjugation"},
	{"siege", "ล้อมเมือง", "Ring of Siege"}, {"annihilation", "ทำลายล้าง", "Ring of Annihilation"},
	{"roar", "คำราม", "Ring of Roar"}, {"bulwark", "ปราการ", "Ring of Bulwark"}, {"tenacity", "ทรหด", "Ring of Tenacity"},
	{"fortune", "คริ", "Ring of Fortune"}, {"concentration", "จุดอ่อน", "Ring of Concentration"},
	{"accuracy", "เข้าเป้า", "Ring of Accuracy"}, {"resistance", "ต้านทาน", "Ring of Resistance"},
}

var stats = [][3]string{
	{"crit", "คริ", "Crit Rate"}, {"weakness", "อ่อน", "Weakness Hit"}, {"critdmg", "คริดาเมจ", "Crit Damage"},
	{"hit", "เข้าเป้า", "Effect Hit"}, {"resist", "ต้าน", "Effect Resistance"}, {"speed", "สปีด", "Speed"},
	{"block", "บล็อค", "Block Rate"}, {"dmgreduce", "ลดดาเมจ", "Damage Taken Reduction"}, {"atk", "โจม %", "All Attack %"},
	{"hpp", "เลือด %", "HP %"}, {"defp", "ป้องกัน %", "Defense %"},
}

// position returns the 1-based position of slug among the rows' first cells, as an id.
// Unknown slugs are a bug in this file, so they panic.
func position(n int, slugAt func(int) string, slug string) string {
	for i := range n {
		if slugAt(i) == slug {
			return strconv.Itoa(i + 1)
		}
	}
	panic("seed: unknown slug " + slug)
}

// Char, Pet, Set, Ring and Stat return the database id of a sample item by its slug.
func Char(slug string) string {
	return position(len(characters), func(i int) string { return characters[i][0] }, slug)
}
func Pet(slug string) string {
	return position(len(pets), func(i int) string { return pets[i][0] }, slug)
}
func Set(slug string) string {
	return position(len(equipmentSets), func(i int) string { return equipmentSets[i][0] }, slug)
}
func Ring(slug string) string {
	return position(len(rings), func(i int) string { return rings[i][0] }, slug)
}
func Stat(slug string) string {
	return position(len(stats), func(i int) string { return stats[i][0] }, slug)
}

func load(ctx context.Context, tx pgx.Tx, sample bool) error {
	exec := func(sql string, args ...any) error {
		_, err := tx.Exec(ctx, sql, args...)
		return err
	}
	if sample {
		// ---- master data ----
		for _, c := range characters {
			if err := exec(`INSERT INTO characters (id, name_th, name_en) VALUES ($1, $2, $3)`, Char(c[0]), c[1], c[2]); err != nil {
				return err
			}
		}
		for _, p := range pets {
			if err := exec(`INSERT INTO pets (id, name_th, name_en) VALUES ($1, $2, $3)`, Pet(p[0]), p[1], p[2]); err != nil {
				return err
			}
		}
		for _, x := range equipmentSets {
			if err := exec(`INSERT INTO equipment_sets (id, name_th, name_en) VALUES ($1, $2, $3)`, Set(x[0]), x[1], x[2]); err != nil {
				return err
			}
		}
		for _, x := range rings {
			if err := exec(`INSERT INTO rings (id, name_th, name_en) VALUES ($1, $2, $3)`, Ring(x[0]), x[1], x[2]); err != nil {
				return err
			}
		}
		for _, x := range stats {
			if err := exec(`INSERT INTO stat_types (id, name_th, name_en) VALUES ($1, $2, $3)`, Stat(x[0]), x[1], x[2]); err != nil {
				return err
			}
		}
	}

	// ---- users (all with password "password123") ----
	hash, err := bcrypt.GenerateFromPassword([]byte(Password), bcrypt.MinCost)
	if err != nil {
		return err
	}
	for i, u := range []struct {
		email, name, role string
		suspended         bool
	}{
		{"pisit@example.com", "Pisit", "SUPER_ADMIN", false},
		{"mee@example.com", "หมีน้อย", "USER", false},
		{"shadow@example.com", "ShadowBlade", "MODERATOR", false},
		{"fah@example.com", "สายฟ้า", "USER", false},
		{"banned@example.com", "โดนแบน", "USER", true},
	} {
		if err := exec(`INSERT INTO users (id, email, password_hash, display_name, role, suspended, email_verified_at)
			VALUES ($1, $2, $3, $4, $5, $6, now())`, UserID(i+1), u.email, string(hash), u.name, u.role, u.suspended); err != nil {
			return err
		}
	}

	days := func(n int) time.Time { return time.Now().Add(-time.Duration(n) * 24 * time.Hour) }

	// ---- groups ----
	for i, g := range []struct {
		name, code string
		owner      int
		age        int
	}{{"กิลด์มาม่า", "MAMA7K", 1, 60}, {"Night Raid", "NIGHT01", 3, 50}, {"ห้องลับสายฟ้า", "FAHFAH", 4, 40}} {
		if err := exec(`INSERT INTO groups (id, name, owner_id, invite_code, created_at) VALUES ($1, $2, $3, $4, $5)`,
			GroupID(i+1), g.name, UserID(g.owner), g.code, days(g.age)); err != nil {
			return err
		}
	}
	for _, m := range [][3]int{{1, 1, 60}, {1, 2, 55}, {1, 5, 30}, {2, 3, 50}, {2, 1, 45}, {3, 4, 40}} {
		if err := exec(`INSERT INTO group_members (group_id, user_id, joined_at) VALUES ($1, $2, $3)`, GroupID(m[0]), UserID(m[1]), days(m[2])); err != nil {
			return err
		}
	}

	if !sample {
		return nil
	}

	// ---- lineups ----
	type pl = [3]string // character code, row, slot
	lineups := []struct {
		formation string
		ps        []pl
		by        int
		age       int
	}{
		{"F4B1", []pl{{"lubu", "FRONT", "1"}, {"hayeon", "FRONT", "2"}, {"soran", "BACK", "0"}}, 1, 40},
		{"F3B2", []pl{{"rudy", "FRONT", "0"}, {"karma", "FRONT", "2"}, {"hellenia", "BACK", "1"}}, 2, 35},
		{"F2B3", []pl{{"evan", "FRONT", "0"}, {"kyle", "BACK", "0"}, {"lucy", "BACK", "2"}}, 3, 20},
		{"F1B4", []pl{{"biscuit", "FRONT", "0"}, {"fai", "BACK", "1"}, {"bidam", "BACK", "2"}}, 4, 5},
	}
	for i, l := range lineups {
		if err := exec(`INSERT INTO lineups (id, formation, canonical_key, created_by, created_at) VALUES ($1, $2, $3, $4, $5)`,
			LineupID(i+1), l.formation, key(l.formation, l.ps), UserID(l.by), days(l.age)); err != nil {
			return err
		}
		for _, p := range l.ps {
			if err := exec(`INSERT INTO lineup_placements (lineup_id, character_id, row, slot) VALUES ($1, $2, $3, $4::smallint)`,
				LineupID(i+1), Char(p[0]), p[1], p[2]); err != nil {
				return err
			}
		}
	}

	// ---- builds: b1–b8, then generated ones for paging (ids 1000+) ----
	for i, b := range append(builds(), pagingBuilds()...) {
		team, err := json.Marshal(b.team)
		if err != nil {
			return err
		}
		if err := exec(`INSERT INTO builds (id, lineup_id, creator_id, visibility, description, counter_pets, formation, team, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			BuildID(i+1), LineupID(b.lineup), UserID(b.creator), b.visibility, b.description, nz(b.counterPets), b.formation, team, days(30), days(3)); err != nil {
			return err
		}
		for _, sh := range b.shares {
			if err := exec(`INSERT INTO build_shares (build_id, group_id, hidden) VALUES ($1, $2, $3)`, BuildID(i+1), GroupID(sh.group), sh.hidden); err != nil {
				return err
			}
		}
	}

	// ---- votes: deterministic pseudo-random ----
	seed := 7
	rand := func() float64 {
		seed = (seed*9301 + 49297) % 233280
		return float64(seed) / 233280
	}
	voters := []int{1, 2, 3, 4} // then anonymous historical votes
	winRate := map[int]float64{1: 0.78, 2: 0.55, 3: 0.66, 5: 0.6, 6: 0.4, 7: 0.7, 8: 0.3}
	counts := map[int]int{1: 34, 2: 12, 3: 9, 5: 18, 6: 6, 7: 10, 8: 5}
	levels := []string{"EXACT_EQUIPMENT", "SAME_SET_DIFFERENT_SUBSTATS", "CHARACTERS_ONLY"}
	for _, b := range []int{1, 2, 3, 5, 6, 7, 8} {
		for i := 0; i < counts[b]; i++ {
			var user *string
			if i < len(voters) {
				if voters[i] == 1 && b != 2 {
					continue // leave room for the demo user to vote
				}
				id := UserID(voters[i])
				user = &id
			}
			level := levels[int(rand()*2.2)%3]
			penalty := 0.0
			if level == "CHARACTERS_ONLY" {
				penalty = 0.2
			}
			result := "LOSS"
			if rand() < winRate[b]-penalty {
				result = "WIN"
			}
			at := time.Now().Add(-time.Duration(rand() * 30 * 24 * float64(time.Hour)))
			if err := exec(`INSERT INTO votes (build_id, user_id, result, equip_level, voted_at) VALUES ($1, $2, $3, $4, $5)`,
				BuildID(b), user, result, level, at); err != nil {
				return err
			}
		}
	}
	return nil
}

func key(formation string, ps [][3]string) string {
	ids := func(row string) []string {
		var out []string
		for _, p := range ps {
			if p[1] == row {
				out = append(out, Char(p[0]))
			}
		}
		sortStrings(out)
		return out
	}
	join := func(xs []string) string {
		s := ""
		for i, x := range xs {
			if i > 0 {
				s += ","
			}
			s += x
		}
		return s
	}
	return formation + "|F:" + join(ids("FRONT")) + "|B:" + join(ids("BACK"))
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

func nz(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}
