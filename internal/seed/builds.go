package seed

import "fmt"

// JSON shapes of builds.team (mirrors app.Team).
type placement struct {
	CharacterID string `json:"characterId"`
	Row         string `json:"row"`
	Slot        int    `json:"slot"`
}
type stack struct {
	RingID string `json:"ringId"`
	Star   *int   `json:"star,omitempty"`
}
type ring struct {
	RingID string `json:"ringId"`
	Star   int    `json:"star"`
	Stack  *stack `json:"stack,omitempty"`
}
type stat struct {
	StatID string   `json:"statId"`
	Value  *float64 `json:"value,omitempty"`
}
type rec struct {
	CharacterID       string   `json:"characterId"`
	Sets              []string `json:"sets"`
	Stats             []stat   `json:"stats"`
	Rings             []ring   `json:"rings"`
	RingStackRequired bool     `json:"ringStackRequired"`
	Note              string   `json:"note"`
}
type speed struct {
	Kind string `json:"kind"`
}
type team struct {
	Placements      []placement `json:"placements"`
	OwnPets         []string    `json:"ownPets"`
	SkillOrder      []string    `json:"skillOrder"`
	Speed           *speed      `json:"speed"`
	Recommendations []rec       `json:"recommendations"`
}

type share struct {
	group  int
	hidden bool
}

type build struct {
	lineup, creator int
	visibility      string
	description     string
	shares          []share
	counterPets     []string
	formation       string
	team            team
}

func p(char, row string, slot int) placement { return placement{"char_" + char, row, slot} }
func v(x float64) *float64                   { return &x }
func i(x int) *int                           { return &x }
func st(id string, value *float64) stat      { return stat{"stat_" + id, value} }
func sets(ids ...string) []string {
	out := make([]string, len(ids))
	for n, id := range ids {
		out[n] = "set_" + id
	}
	return out
}
func r(id string, star int, s *stack) ring { return ring{"ring_" + id, star, s} }

// plain is a recommendation with only sets and stats.
func plain(char string, setIDs []string, stats ...stat) rec {
	if stats == nil {
		stats = []stat{}
	}
	return rec{CharacterID: "char_" + char, Sets: setIDs, Stats: stats, Rings: []ring{}}
}

func builds() []build {
	return []build{
		{ // b1: public
			lineup: 1, creator: 1, visibility: "PUBLIC", formation: "F4B1",
			description: "ต้องเร็วกว่าฝั่งตรงข้าม ยุนกอนเปิดก่อนแล้วสกัลตามด้วยสถานะ โดยองต้องซ้อนแหวนเท่านั้น",
			counterPets: []string{"pet_yu"},
			team: team{
				Placements: []placement{p("skull", "FRONT", 1), p("doyoung", "FRONT", 2), p("yungon", "BACK", 0)},
				OwnPets:    []string{"pet_taeo", "pet_yu", "pet_ari"},
				SkillOrder: []string{"skill_yungon_1", "skill_skull_2", "skill_yungon_2"},
				Speed:      &speed{"FASTER"},
				Recommendations: []rec{
					{CharacterID: "char_yungon", Sets: sets("crit", "pierce"), Stats: []stat{st("crit", v(100)), st("weakness", v(100))},
						Rings: []ring{r("authority", 6, &stack{"ring_immortality", i(6)}), r("authority", 6, nil)}},
					{CharacterID: "char_skull", Sets: sets("hit"), Stats: []stat{st("hit", v(100)), st("speed", nil)}, Rings: []ring{}, Note: "เน้นสปีดเร็ว ๆ"},
					{CharacterID: "char_doyoung", Sets: sets("crit"), Stats: []stat{st("crit", v(100)), st("resist", v(100))}, RingStackRequired: true,
						Rings: []ring{r("authority", 6, &stack{"ring_resurrection", nil}), r("authority", 6, &stack{"ring_immortality", i(6)})}},
				},
			},
		},
		{ // b2: group กิลด์มาม่า
			lineup: 1, creator: 2, visibility: "GROUP", formation: "F3B2", shares: []share{{1, false}},
			description: "สายถึก ให้รูดี้แบกดาเมจ ใส่ลดดาเมจกับบล็อคให้ถึง แล้วให้เทโอกับปาสคาลเก็บ",
			team: team{
				Placements: []placement{p("rudy", "FRONT", 1), p("teo", "BACK", 0), p("pascal", "BACK", 1)},
				OwnPets:    []string{"pet_ari"},
				SkillOrder: []string{"skill_rudy_1", "skill_pascal_2"},
				Recommendations: []rec{
					{CharacterID: "char_rudy", Sets: sets("def", "block"), Stats: []stat{st("dmgreduce", v(42)), st("block", v(100))}, Rings: []ring{r("bulwark", 6, nil)}, Note: "ถึก ๆ"},
					plain("teo", sets("attack"), st("crit", v(100)), st("weakness", v(100))),
					{CharacterID: "char_pascal", Sets: sets("crit"), Stats: []stat{st("crit", v(100))}, Rings: []ring{r("siege", 5, nil)}},
				},
			},
		},
		{ // b3: group Night Raid
			lineup: 1, creator: 3, visibility: "GROUP", formation: "F2B3", shares: []share{{2, false}},
			description: "สูตรของ Night Raid ใช้ได้ดีเมื่อศัตรูใช้แทโอ", counterPets: []string{"pet_taeo", "pet_pet4"},
			team: team{
				Placements: []placement{p("evan", "FRONT", 0), p("kyle", "BACK", 1), p("lucy", "BACK", 2)},
				OwnPets:    []string{"pet_pet5"},
				SkillOrder: []string{"skill_kyle_1"},
				Recommendations: []rec{
					plain("evan", sets("block")),
					{CharacterID: "char_kyle", Sets: sets("attack", "crit"), Stats: []stat{st("crit", v(100)), st("weakness", v(100)), st("speed", nil)}, Rings: []ring{r("fortune", 6, nil)}},
					plain("lucy", sets("hp"), st("hpp", nil)),
				},
			},
		},
		{ // b4: private
			lineup: 1, creator: 1, visibility: "PRIVATE", formation: "F3B2", description: "ยังไม่ได้ทดสอบ เก็บไว้ลองเอง",
			team: team{
				Placements:      []placement{p("shane", "FRONT", 0), p("fai", "FRONT", 1), p("bidam", "BACK", 0)},
				OwnPets:         []string{},
				SkillOrder:      []string{},
				Recommendations: []rec{plain("shane", []string{}), plain("fai", []string{}), plain("bidam", []string{})},
			},
		},
		{ // b5: public on lineup 2
			lineup: 2, creator: 4, visibility: "PUBLIC", formation: "F3B2", description: "ไฟกับบีดัมเปิดสถานะต่อเนื่อง ฮายอนอยู่หลังคอยต้าน",
			team: team{
				Placements: []placement{p("fai", "FRONT", 0), p("bidam", "FRONT", 2), p("hayeon", "BACK", 0)},
				OwnPets:    []string{"pet_yu", "pet_pet6"},
				SkillOrder: []string{"skill_fai_1", "skill_bidam_1", "skill_fai_2"},
				Recommendations: []rec{
					{CharacterID: "char_fai", Sets: sets("attack"), Stats: []stat{st("hit", v(100)), st("atk", nil)}, Rings: []ring{r("accuracy", 5, nil)}},
					plain("bidam", sets("attack", "hit"), st("hit", v(80))),
					plain("hayeon", sets("resist"), st("resist", v(70))),
				},
			},
		},
		{ // b6: shared with both groups, hidden in Night Raid
			lineup: 2, creator: 1, visibility: "GROUP", formation: "F4B1", shares: []share{{1, false}, {2, true}},
			description: "ใช้ในกิลด์มาม่า (Owner ของ Night Raid ซ่อน Build นี้ไว้)", counterPets: []string{"pet_ari"},
			team: team{
				Placements: []placement{p("lucy", "FRONT", 0), p("karma", "FRONT", 3), p("shane", "BACK", 0)},
				OwnPets:    []string{"pet_taeo"},
				SkillOrder: []string{"skill_shane_1", "skill_shane_2"},
				Recommendations: []rec{
					plain("lucy", sets("hp")),
					plain("karma", sets("def")),
					{CharacterID: "char_shane", Sets: sets("boss", "crit"), Stats: []stat{st("crit", v(100)), st("weakness", v(80)), st("atk", nil)}, Rings: []ring{r("annihilation", 6, nil)}},
				},
			},
		},
		{ // b7: shared with both groups
			lineup: 1, creator: 1, visibility: "GROUP", formation: "F3B2", shares: []share{{1, false}, {2, false}},
			description: "แชร์ให้ทั้งกิลด์มาม่าและ Night Raid ใช้ได้เมื่อศัตรูใช้อาริ", counterPets: []string{"pet_ari"},
			team: team{
				Placements: []placement{p("shane", "FRONT", 0), p("pascal", "FRONT", 1), p("lucy", "BACK", 0)},
				OwnPets:    []string{"pet_yu"},
				SkillOrder: []string{"skill_shane_1", "skill_pascal_1"},
				Recommendations: []rec{
					plain("shane", sets("crit"), st("crit", v(100)), st("weakness", v(100))),
					{CharacterID: "char_pascal", Sets: sets("crit", "attack"), Stats: []stat{st("crit", v(100))}, Rings: []ring{r("siege", 6, nil)}},
					plain("lucy", sets("hp"), st("hpp", nil)),
				},
			},
		},
		{ // b8: hidden by the กิลด์มาม่า owner
			lineup: 1, creator: 2, visibility: "GROUP", formation: "F2B3", shares: []share{{1, true}},
			description: "Owner กิลด์มาม่าซ่อนไว้เพราะใช้ไม่ได้แล้ว แต่ผู้สร้างยังเห็นและแก้ไขได้",
			team: team{
				Placements: []placement{p("kyle", "FRONT", 0), p("evan", "FRONT", 1), p("karma", "BACK", 1)},
				OwnPets:    []string{"pet_taeo"},
				SkillOrder: []string{"skill_kyle_2"},
				Recommendations: []rec{
					plain("kyle", sets("attack"), st("crit", v(90))),
					plain("evan", sets("block")),
					plain("karma", sets("def")),
				},
			},
		},
	}
}

// pagingBuilds adds many public builds to lineup 3 so the list has several pages.
func pagingBuilds() []build {
	pool := []string{"yungon", "skull", "doyoung", "rudy", "karma", "hellenia", "shane", "pascal", "fai", "bidam", "biscuit", "teo", "hayeon", "soran"}
	setPool := []string{"crit", "attack", "hit", "def", "boss", "resist"}
	var out []build
	for n := 0; n < 26; n++ {
		chars := []string{pool[(n*3)%len(pool)], pool[(n*3+5)%len(pool)], pool[(n*3+10)%len(pool)]}
		var counter []string
		if n%4 == 0 {
			counter = []string{"pet_taeo"}
		}
		recs := make([]rec, 3)
		for k, c := range chars {
			recs[k] = plain(c, sets(setPool[(n+k)%len(setPool)]), st("crit", v(100)))
		}
		out = append(out, build{
			lineup: 3, creator: []int{2, 3, 4}[n%3], visibility: "PUBLIC", formation: "F3B2",
			description: fmt.Sprintf("ตัวอย่าง Build ที่ %d สำหรับทดลองแบ่งหน้า", n+1), counterPets: counter,
			team: team{
				Placements:      []placement{p(chars[0], "FRONT", 0), p(chars[1], "FRONT", 2), p(chars[2], "BACK", 1)},
				OwnPets:         []string{[]string{"pet_taeo", "pet_yu", "pet_ari"}[n%3]},
				SkillOrder:      []string{},
				Recommendations: recs,
			},
		})
	}
	return out
}
