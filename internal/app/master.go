package app

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

// mediaBaseURL is where relative image paths live (MEDIA_PUBLIC_URL, e.g. an R2 bucket).
// Set once by NewServer.
var mediaBaseURL = "/media"

// imageURL turns a stored image path into a URL. Relative paths (from CSV import, e.g.
// "master/characters/001_ludi.webp") are joined to the media base; paths that are
// already absolute ("/media/…" from the admin upload form, or "https://…") pass through.
func imageURL(path *string) *string {
	if path == nil || *path == "" {
		return nil
	}
	u := *path
	if !strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		u = mediaBaseURL + "/" + u
	}
	return &u
}

// loadMaster returns every master item, including inactive ones (old builds still render them).
func loadMaster(ctx context.Context, q querier) (*MasterData, error) {
	var m MasterData
	base := func(row pgx.CollectableRow, extra ...any) (MasterItem, error) {
		var it MasterItem
		var img *string
		dst := append([]any{&it.ID, &it.NameTh, &it.NameEn, &img, &it.Active}, extra...)
		if err := row.Scan(dst...); err != nil {
			return it, err
		}
		it.Image = imageURL(img)
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
	const cols = `id, name_th, name_en, image_path, active`
	err := collect(`SELECT `+cols+` FROM characters ORDER BY id::int`, func(r pgx.CollectableRow) error {
		it, err := base(r)
		m.Characters = append(m.Characters, it)
		return err
	})
	if err == nil {
		err = collect(`SELECT `+cols+` FROM pets ORDER BY id::int`, func(r pgx.CollectableRow) error {
			it, err := base(r)
			m.Pets = append(m.Pets, it)
			return err
		})
	}
	if err == nil {
		err = collect(`SELECT `+cols+` FROM equipment_sets ORDER BY id::int`, func(r pgx.CollectableRow) error {
			it, err := base(r)
			m.Sets = append(m.Sets, it)
			return err
		})
	}
	if err == nil {
		err = collect(`SELECT `+cols+` FROM rings ORDER BY id::int`, func(r pgx.CollectableRow) error {
			it, err := base(r)
			m.Rings = append(m.Rings, it)
			return err
		})
	}
	if err == nil {
		err = collect(`SELECT `+cols+` FROM stat_types ORDER BY id::int`, func(r pgx.CollectableRow) error {
			it, err := base(r)
			m.Stats = append(m.Stats, it)
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
	pets       map[string]MasterItem
	sets       map[string]EquipmentSet
	rings      map[string]Ring
	stats      map[string]StatType
}

func indexMaster(m *MasterData) *masterIndex {
	ix := &masterIndex{map[string]MasterItem{}, map[string]MasterItem{}, map[string]EquipmentSet{}, map[string]Ring{}, map[string]StatType{}}
	for _, x := range m.Characters {
		ix.characters[x.ID] = x
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
