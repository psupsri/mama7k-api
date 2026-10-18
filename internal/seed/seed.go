// Package seed loads the same sample data the web mock uses, with deterministic ids so the
// integration tests can refer to "b1", "the outsider", etc.
package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// Password is the password of every seeded user.
const Password = "password123"

// Deterministic ids (the mock's u1, g1, l1, b1 … mapped onto valid UUIDs).
func UserID(n int) string   { return fmt.Sprintf("00000000-0000-0000-0000-0000000000c%d", n) }
func GroupID(n int) string  { return fmt.Sprintf("00000000-0000-0000-0000-0000000000d%d", n) }
func LineupID(n int) string { return fmt.Sprintf("00000000-0000-0000-0000-0000000000a%d", n) }
func BuildID(n int) string  { return fmt.Sprintf("00000000-0000-0000-0000-%012x", 0xb0+n) }

// Reset empties every table (keeps the schema) and loads the seed.
func Reset(ctx context.Context, pool *pgxpool.Pool) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `TRUNCATE audit_log, votes, build_shares, builds, lineup_placements, lineups,
			group_members, groups, email_tokens, sessions, users, stat_types, rings, equipment_sets, pets, skills, characters
			RESTART IDENTITY CASCADE`); err != nil {
			return err
		}
		return load(ctx, tx)
	})
}

type character struct {
	code, name string
	aliases    []string
}

var characters = []character{
	{"yungon", "ยุนกอน", nil}, {"skull", "สกัล", nil}, {"doyoung", "โดยอง", nil},
	{"lubu", "ลิโป้", []string{"Lu Bu"}}, {"hayeon", "ฮายอน", nil}, {"soran", "ซอรัน", nil},
	{"rudy", "รูดี้", []string{"Rudy"}}, {"evan", "อีวาน", []string{"Evan"}}, {"karma", "คาร์ม่า", []string{"Karma"}},
	{"hellenia", "เฮเลเนีย", []string{"Hellenia"}}, {"lucy", "ลูซี่", []string{"Lucy"}}, {"shane", "เชน", []string{"Shane"}},
	{"pascal", "ปาสคาล", []string{"Pascal"}}, {"fai", "ไฟ", []string{"Fai"}}, {"bidam", "บีดัม", []string{"Bi Dam"}},
	{"kyle", "ไคล์", []string{"Kyle"}}, {"biscuit", "บิสกิต", []string{"Biscuit"}}, {"teo", "เทโอ", []string{"Teo"}},
}

func load(ctx context.Context, tx pgx.Tx) error {
	exec := func(sql string, args ...any) error {
		_, err := tx.Exec(ctx, sql, args...)
		return err
	}
	// ---- master data ----
	for _, c := range characters {
		if err := exec(`INSERT INTO characters (id, name, aliases) VALUES ($1, $2, $3)`, "char_"+c.code, c.name, nz(c.aliases)); err != nil {
			return err
		}
		for slot, name := range map[int]string{1: "สกิลบน", 2: "สกิลล่าง"} {
			if err := exec(`INSERT INTO skills (id, character_id, slot, name) VALUES ($1, $2, $3, $4)`,
				fmt.Sprintf("skill_%s_%d", c.code, slot), "char_"+c.code, slot, name); err != nil {
				return err
			}
		}
	}
	for _, p := range [][2]string{{"taeo", "แทโอ"}, {"yu", "ยู"}, {"ari", "อาริ"}, {"pet4", "Pet 4"}, {"pet5", "Pet 5"}, {"pet6", "Pet 6"}} {
		if err := exec(`INSERT INTO pets (id, name) VALUES ($1, $2)`, "pet_"+p[0], p[1]); err != nil {
			return err
		}
	}
	for _, s := range [][5]string{
		{"attack", "เซ็ตโจม", "Vanguard", "เพิ่มพลังโจมตี", "เพิ่มพลังโจมตีมากขึ้น"},
		{"pierce", "เซ็ตเจาะเกราะ", "Bounty Tracker", "โอกาสโจมตีจุดอ่อน +15%", "[effect 4 ชิ้น]"},
		{"hp", "เซ็ตเลือด", "Paladin", "เพิ่ม HP", "ฟื้นฟูตาม Max HP"},
		{"hit", "เซ็ตเข้าเป้า", "Spellweaver", "อัตราเข้าเป้า", "เพิ่มโอกาสติดสถานะ"},
		{"boss", "เซ็ตบอส", "Avenger", "เพิ่มความเสียหาย", "เพิ่มความเสียหายต่อบอส"},
		{"resist", "เซ็ตต้าน", "Orchestrator", "ต้านทานสถานะ", "ต้านทานสถานะ + กัน CC 1 เทิร์น"},
		{"block", "เซ็ตบล็อค", "Gatekeeper", "อัตราบล็อค", "อัตราบล็อค x2"},
		{"crit", "เซ็ตคริ", "Assassin", "คริเรท +15%", "คริเรท +30% เจาะเกราะ +15%"},
		{"def", "เซ็ตป้องกัน", "Guardian", "เพิ่มพลังป้องกัน", "ต้านทานสถานะ"},
	} {
		if err := exec(`INSERT INTO equipment_sets (id, name, aliases, effect_2, effect_4) VALUES ($1, $2, $3, $4, $5)`,
			"set_"+s[0], s[1], []string{s[2]}, s[3], s[4]); err != nil {
			return err
		}
	}
	for _, r := range [][5]string{
		{"authority", "อำนาจ", "Ring of Authority", "LEGENDARY", "ได้ Barrier ตาม Attack หลัง Guts ทำงาน"},
		{"immortality", "อมตะ", "Ring of Immortality", "LEGENDARY", "เมื่อถูก KO เป็นอมตะ 1–3 เทิร์นตามดาว"},
		{"resurrection", "ชุบ", "Ring of Resurrection", "LEGENDARY", "ฟื้นคืนชีพ 1 ครั้ง"},
		{"subjugation", "ปราบปราม", "Ring of Subjugation", "RARE", "เพิ่มความเสียหายต่อบอส"},
		{"siege", "ล้อมเมือง", "Ring of Siege", "RARE", "เพิ่มความเสียหายสกิลโจมตี 1–3 เป้า"},
		{"annihilation", "ทำลายล้าง", "Ring of Annihilation", "RARE", "เพิ่มความเสียหายสกิลโจมตี 4–5 เป้า"},
		{"roar", "คำราม", "Ring of Roar", "RARE", "Barrier ตาม Attack"},
		{"bulwark", "ปราการ", "Ring of Bulwark", "RARE", "Barrier ตาม Defense"},
		{"tenacity", "ทรหด", "Ring of Tenacity", "RARE", "ฟื้นฟูเมื่อ HP ต่ำกว่า 50%"},
		{"fortune", "คริ", "Ring of Fortune", "COMMON", "เพิ่มคริเรท"},
		{"concentration", "จุดอ่อน", "Ring of Concentration", "COMMON", "เพิ่มโอกาสโจมตีจุดอ่อน"},
		{"accuracy", "เข้าเป้า", "Ring of Accuracy", "COMMON", "เพิ่มอัตราเข้าเป้า"},
		{"resistance", "ต้านทาน", "Ring of Resistance", "COMMON", "เพิ่มต้านทานสถานะ"},
	} {
		if err := exec(`INSERT INTO rings (id, name, aliases, rarity, effect) VALUES ($1, $2, $3, $4, $5)`,
			"ring_"+r[0], r[1], []string{r[2]}, r[3], r[4]); err != nil {
			return err
		}
	}
	for _, s := range []struct {
		code, name string
		aliases    []string
		unit       string
	}{
		{"crit", "คริ", []string{"คริเรท", "Crit Rate"}, "PERCENT"},
		{"weakness", "อ่อน", []string{"จุดอ่อน", "Weakness Hit"}, "PERCENT"},
		{"critdmg", "คริดาเมจ", []string{"Crit Damage"}, "PERCENT"},
		{"hit", "เข้าเป้า", []string{"Effect Hit"}, "PERCENT"},
		{"resist", "ต้าน", []string{"ต้านทาน", "Effect Resistance"}, "PERCENT"},
		{"speed", "สปีด", []string{"Speed"}, "NUMBER"},
		{"block", "บล็อค", []string{"Block Rate"}, "PERCENT"},
		{"dmgreduce", "ลดดาเมจ", []string{"ลดความเสียหาย", "Damage Taken Reduction"}, "PERCENT"},
		{"atk", "โจม %", []string{"All Attack %"}, "PERCENT"},
		{"hpp", "เลือด %", []string{"HP %"}, "PERCENT"},
		{"defp", "ป้องกัน %", []string{"Defense %"}, "PERCENT"},
	} {
		if err := exec(`INSERT INTO stat_types (id, name, aliases, unit) VALUES ($1, $2, $3, $4)`, "stat_"+s.code, s.name, s.aliases, s.unit); err != nil {
			return err
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
				LineupID(i+1), "char_"+p[0], p[1], p[2]); err != nil {
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

	// ---- votes: deterministic pseudo-random, as in the web mock ----
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
				out = append(out, "char_"+p[0])
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
