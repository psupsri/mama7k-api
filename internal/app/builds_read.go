package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// querier is satisfied by both *pgxpool.Pool and pgx.Tx.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// visibleSQL is the build access rule (FR 4.6) as a SQL predicate over alias "b":
// public, or the viewer is the creator, or the viewer belongs to a group the build is shared
// with and that group has not hidden it. Orphaned builds are visible to nobody; builds hidden
// by an admin are visible only to their creator and staff. viewerParam may be NULL (guest).
func visibleSQL(viewerParam, adminParam string) string {
	return fmt.Sprintf(`(
  NOT (b.creator_deleted AND b.visibility <> 'PUBLIC')
  AND (NOT b.admin_hidden OR b.creator_id = %[1]s::uuid OR %[2]s::boolean)
  AND (
    b.visibility = 'PUBLIC'
    OR b.creator_id = %[1]s::uuid
    OR (b.visibility = 'GROUP' AND EXISTS (
      SELECT 1 FROM build_shares s
      JOIN group_members m ON m.group_id = s.group_id AND m.user_id = %[1]s::uuid
      WHERE s.build_id = b.id AND NOT s.hidden))
  ))`, viewerParam, adminParam)
}

// viewerArgs returns the (viewer id, is staff) pair used with visibleSQL.
func viewerArgs(u *User) (*string, bool) {
	if u == nil {
		return nil, false
	}
	return &u.ID, u.Role != RoleUser
}

const buildSelect = `
SELECT b.id, b.lineup_id, b.description, b.creator_id, b.creator_deleted, b.visibility, b.admin_hidden,
       b.counter_pets, b.formation, b.team, b.created_at, b.updated_at,
       coalesce((SELECT jsonb_agg(jsonb_build_object('groupId', s.group_id, 'hidden', s.hidden) ORDER BY s.group_id)
                 FROM build_shares s WHERE s.build_id = b.id), '[]'::jsonb),
       coalesce((SELECT display_name FROM users WHERE id = b.creator_id), 'Deleted user')
FROM builds b`

type buildRow struct {
	OffenseBuild
	creatorName string
}

func loadBuilds(ctx context.Context, q querier, where string, args ...any) ([]buildRow, error) {
	rows, err := q.Query(ctx, buildSelect+" "+where, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (buildRow, error) {
		var b buildRow
		var team, shares []byte
		err := row.Scan(&b.ID, &b.LineupID, &b.Description, &b.CreatorID, &b.CreatorDeleted, &b.Visibility, &b.AdminHidden,
			&b.CounterPets, &b.Formation, &team, &b.CreatedAt, &b.UpdatedAt, &shares, &b.creatorName)
		if err != nil {
			return b, err
		}
		if err := json.Unmarshal(team, &b.Team); err != nil {
			return b, err
		}
		return b, json.Unmarshal(shares, &b.Shares)
	})
}

// findVisibleBuild loads one build the viewer may see. Inaccessible builds return the same
// NOT_FOUND as missing ones so their existence is not revealed (FR 4.6).
func (s *Server) findVisibleBuild(ctx context.Context, q querier, u *User, id string) (*buildRow, error) {
	vid, admin := viewerArgs(u)
	list, err := loadBuilds(ctx, q, "WHERE b.id = $1::uuid AND "+visibleSQL("$2", "$3"), id, vid, admin)
	if err != nil {
		if isInvalidUUID(err) {
			return nil, errNotFound("ไม่พบ Build")
		}
		return nil, err
	}
	if len(list) == 0 {
		return nil, errNotFound("ไม่พบ Build")
	}
	return &list[0], nil
}

// ---------- stats (FR 6.4) ----------

var equipLevels = []string{"EXACT_EQUIPMENT", "SAME_SET_DIFFERENT_SUBSTATS", "CHARACTERS_ONLY"}

type voteCount struct {
	level, result string
	n             int
}

func buildStats(window string, counts []voteCount) BuildStats {
	st := BuildStats{Window: window, Levels: make([]LevelStats, 0, len(equipLevels))}
	wins := 0
	for _, lv := range equipLevels {
		ls := LevelStats{Level: lv}
		for _, c := range counts {
			if c.level != lv {
				continue
			}
			if c.result == "WIN" {
				ls.Win += c.n
			} else {
				ls.Loss += c.n
			}
		}
		ls.Total = ls.Win + ls.Loss
		ls.WinPct = pct(ls.Win, ls.Total)
		st.Levels = append(st.Levels, ls)
		st.Total += ls.Total
		wins += ls.Win
	}
	st.OverallWinPct = pct(wins, st.Total)
	return st
}

func pct(n, total int) *float64 {
	if total == 0 {
		return nil
	}
	v := float64(n) / float64(total) * 100
	return &v
}

// voteCounts returns per-build counts; since=zero means all time.
func voteCounts(ctx context.Context, q querier, buildIDs []string, since time.Time) (map[string][]voteCount, error) {
	out := map[string][]voteCount{}
	if len(buildIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `
		SELECT build_id, equip_level, result, count(*) FROM votes
		WHERE build_id = ANY($1::uuid[]) AND ($2::timestamptz IS NULL OR voted_at > $2)
		GROUP BY build_id, equip_level, result`, buildIDs, nullTime(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var c voteCount
		if err := rows.Scan(&id, &c.level, &c.result, &c.n); err != nil {
			return nil, err
		}
		out[id] = append(out[id], c)
	}
	return out, rows.Err()
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (s *Server) since14d() time.Time { return s.now().Add(-14 * 24 * time.Hour) }

// summaries turns build rows into what the viewer is allowed to know about them.
func (s *Server) summaries(ctx context.Context, q querier, u *User, list []buildRow) ([]BuildSummary, error) {
	ids := make([]string, len(list))
	groupIDs := map[string]bool{}
	for i, b := range list {
		ids[i] = b.ID
		for _, sh := range b.Shares {
			groupIDs[sh.GroupID] = true
		}
	}
	counts, err := voteCounts(ctx, q, ids, s.since14d())
	if err != nil {
		return nil, err
	}
	names, member, err := groupContext(ctx, q, u, groupIDs)
	if err != nil {
		return nil, err
	}
	out := make([]BuildSummary, len(list))
	for i, b := range list {
		mine := u != nil && b.CreatorID != nil && *b.CreatorID == u.ID
		groupNames := []string{}
		for _, sh := range b.Shares {
			// Creators see all their shares; others only groups they belong to that show the build.
			if mine || (!sh.Hidden && member[sh.GroupID]) {
				groupNames = append(groupNames, names[sh.GroupID])
			}
		}
		out[i] = BuildSummary{Build: b.OffenseBuild, CreatorName: b.creatorName, GroupNames: groupNames, Stats14: buildStats("D14", counts[b.ID]), IsMine: mine}
	}
	return out, nil
}

// groupContext returns names for the given groups and which of them the viewer belongs to.
func groupContext(ctx context.Context, q querier, u *User, groupIDs map[string]bool) (map[string]string, map[string]bool, error) {
	names, member := map[string]string{}, map[string]bool{}
	if len(groupIDs) == 0 {
		return names, member, nil
	}
	ids := make([]string, 0, len(groupIDs))
	for id := range groupIDs {
		ids = append(ids, id)
	}
	var vid *string
	if u != nil {
		vid = &u.ID
	}
	rows, err := q.Query(ctx, `
		SELECT g.id, g.name, EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = g.id AND m.user_id = $2::uuid)
		FROM groups g WHERE g.id = ANY($1::uuid[])`, ids, vid)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		var isMember bool
		if err := rows.Scan(&id, &name, &isMember); err != nil {
			return nil, nil, err
		}
		names[id], member[id] = name, isMember
	}
	return names, member, rows.Err()
}

func sortSummaries(list []BuildSummary, sortBy string) {
	winPct := func(s BuildSummary) float64 {
		if s.Stats14.OverallWinPct == nil {
			return -1
		}
		return *s.Stats14.OverallWinPct
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		switch sortBy {
		case "VOTES":
			return a.Stats14.Total > b.Stats14.Total
		case "NEWEST":
			return a.Build.CreatedAt.After(b.Build.CreatedAt)
		default: // WIN14
			if winPct(a) != winPct(b) {
				return winPct(a) > winPct(b)
			}
			return a.Stats14.Total > b.Stats14.Total
		}
	})
}

func isInvalidUUID(err error) bool {
	var pgErr *pgconn.PgError
	return asPgError(err, &pgErr) && pgErr.Code == "22P02"
}
