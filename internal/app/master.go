package app

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"
)

func imageURL(path *string) *string {
	if path == nil || *path == "" {
		return nil
	}
	u := "/media/" + *path
	return &u
}

// loadMaster returns every master item, including inactive ones (old builds still render them).
func loadMaster(ctx context.Context, q querier) (*MasterData, error) {
	var m MasterData
	base := func(row pgx.CollectableRow, extra ...any) (MasterItem, error) {
		var it MasterItem
		var img *string
		dst := append([]any{&it.ID, &it.Name, &it.Aliases, &img, &it.Active}, extra...)
		if err := row.Scan(dst...); err != nil {
			return it, err
		}
		it.Code, it.Image = it.ID, imageURL(img)
		return it, nil
	}
	collect := func(sql string, fn func(pgx.CollectableRow) error) error {
		rows, err := q.Query(ctx, sql)
		if err != nil {
			return err
		}
		_, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (struct{}, error) { return struct{}{}, fn(r) })
		return err
	}
	const cols = `id, name, aliases, image_path, active`
	err := collect(`SELECT `+cols+` FROM characters ORDER BY id`, func(r pgx.CollectableRow) error {
		it, err := base(r)
		m.Characters = append(m.Characters, it)
		return err
	})
	if err == nil {
		err = collect(`SELECT `+cols+`, character_id, slot FROM skills ORDER BY character_id, slot`, func(r pgx.CollectableRow) error {
			var sk Skill
			var err error
			sk.MasterItem, err = base(r, &sk.CharacterID, &sk.Slot)
			m.Skills = append(m.Skills, sk)
			return err
		})
	}
	if err == nil {
		err = collect(`SELECT `+cols+` FROM pets ORDER BY id`, func(r pgx.CollectableRow) error {
			it, err := base(r)
			m.Pets = append(m.Pets, it)
			return err
		})
	}
	if err == nil {
		err = collect(`SELECT `+cols+`, effect_2, effect_4 FROM equipment_sets ORDER BY id`, func(r pgx.CollectableRow) error {
			var st EquipmentSet
			var err error
			st.MasterItem, err = base(r, &st.Effect2, &st.Effect4)
			m.Sets = append(m.Sets, st)
			return err
		})
	}
	if err == nil {
		err = collect(`SELECT `+cols+`, rarity, effect FROM rings ORDER BY id`, func(r pgx.CollectableRow) error {
			var rg Ring
			var err error
			rg.MasterItem, err = base(r, &rg.Rarity, &rg.Effect)
			m.Rings = append(m.Rings, rg)
			return err
		})
	}
	if err == nil {
		err = collect(`SELECT `+cols+`, unit FROM stat_types ORDER BY id`, func(r pgx.CollectableRow) error {
			var st StatType
			var err error
			st.MasterItem, err = base(r, &st.Unit)
			m.Stats = append(m.Stats, st)
			return err
		})
	}
	if err != nil {
		return nil, err
	}
	// Return [] rather than null for empty tables.
	if m.Characters == nil {
		m.Characters = []MasterItem{}
	}
	if m.Skills == nil {
		m.Skills = []Skill{}
	}
	if m.Pets == nil {
		m.Pets = []MasterItem{}
	}
	if m.Sets == nil {
		m.Sets = []EquipmentSet{}
	}
	if m.Rings == nil {
		m.Rings = []Ring{}
	}
	if m.Stats == nil {
		m.Stats = []StatType{}
	}
	return &m, nil
}

// masterIndex gives O(1) lookups for validation.
type masterIndex struct {
	characters map[string]MasterItem
	skills     map[string]Skill
	pets       map[string]MasterItem
	sets       map[string]EquipmentSet
	rings      map[string]Ring
	stats      map[string]StatType
}

func indexMaster(m *MasterData) *masterIndex {
	ix := &masterIndex{map[string]MasterItem{}, map[string]Skill{}, map[string]MasterItem{}, map[string]EquipmentSet{}, map[string]Ring{}, map[string]StatType{}}
	for _, x := range m.Characters {
		ix.characters[x.ID] = x
	}
	for _, x := range m.Skills {
		ix.skills[x.ID] = x
	}
	for _, x := range m.Pets {
		ix.pets[x.ID] = x
	}
	for _, x := range m.Sets {
		ix.sets[x.ID] = x
	}
	for _, x := range m.Rings {
		ix.rings[x.ID] = x
	}
	for _, x := range m.Stats {
		ix.stats[x.ID] = x
	}
	return ix
}

func (s *Server) getMaster(w http.ResponseWriter, r *http.Request) error {
	m, err := loadMaster(r.Context(), s.db)
	if err != nil {
		return err
	}
	return ok(w, m)
}
