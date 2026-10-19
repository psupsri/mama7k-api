package app_test

// Integration tests: real PostgreSQL (docker compose `db`, database mama7k_test) and real HTTP.
// Each case maps to a section of the FR document, mirroring web/src/api/rules.test.ts.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"mama7k/api/internal/app"
	"mama7k/api/internal/config"
	"mama7k/api/internal/db"
	"mama7k/api/internal/seed"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://mama7k:mama7k@localhost:5433/mama7k_test?sslmode=disable"
	}
	ctx := context.Background()
	if err := db.Migrate(ctx, url); err != nil {
		panic("migrate test db (is `docker compose up -d db` running?): " + err.Error())
	}
	var err error
	if pool, err = db.Connect(ctx, url); err != nil {
		panic(err)
	}
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

// ---------- harness ----------

type env struct {
	t   *testing.T
	srv *httptest.Server
}

func setup(t *testing.T) *env {
	t.Helper()
	if err := seed.Reset(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{SessionTTL: time.Hour, UploadDir: t.TempDir()}
	srv := httptest.NewServer(app.NewServer(cfg, pool).Handler())
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv}
}

// client is a user agent; token == "" means guest.
type client struct {
	e     *env
	token string
}

func (e *env) guest() *client { return &client{e: e} }

// as signs in seeded user n (1 = Pisit … 5 = suspended) by creating a session row directly,
// avoiding a bcrypt round trip per test.
func (e *env) as(n int) *client {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	raw := base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(raw))
	if _, err := pool.Exec(context.Background(), `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, now() + interval '1 hour')`,
		h[:], seed.UserID(n)); err != nil {
		e.t.Fatal(err)
	}
	return &client{e: e, token: raw}
}

type resp struct {
	status int
	body   []byte
	cookie string // session cookie set by the response, if any
}

func (r resp) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

func (r resp) errCode(t *testing.T) string {
	var e struct{ Error struct{ Code string } }
	r.json(t, &e)
	return e.Error.Code
}

func (c *client) do(method, path string, body any) resp {
	c.e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.e.srv.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.AddCookie(&http.Cookie{Name: "mama7k_session", Value: c.token})
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, body: b}
	for _, ck := range res.Cookies() {
		if ck.Name == "mama7k_session" {
			out.cookie = ck.Value
		}
	}
	return out
}

func (c *client) expect(status int, method, path string, body any) resp {
	c.e.t.Helper()
	r := c.do(method, path, body)
	if r.status != status {
		c.e.t.Fatalf("%s %s: want %d, got %d: %s", method, path, status, r.status, r.body)
	}
	return r
}

// short turns seeded build UUIDs back into "b1", "b2" … for readable assertions.
func short(id string) string { return "b" + id[len(id)-1:] }

func (c *client) lineupBuilds(lineup int, query string) []string {
	c.e.t.Helper()
	var page struct {
		Items []struct{ Build struct{ ID string } }
	}
	sep := "?"
	if strings.Contains(query, "?") {
		sep = "&"
	}
	c.expect(200, "GET", "/api/lineups/"+seed.LineupID(lineup)+"/builds"+query+sep+"pageSize=50", nil).json(c.e.t, &page)
	out := make([]string, len(page.Items))
	for i, s := range page.Items {
		out[i] = short(s.Build.ID)
	}
	sort.Strings(out)
	return out
}

func eq(t *testing.T, got, want any) {
	t.Helper()
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if !bytes.Equal(g, w) {
		t.Fatalf("got %s, want %s", g, w)
	}
}

// buildInput loads a build as its creator and returns it as an update payload.
func (c *client) buildInput(n int, patch map[string]any) map[string]any {
	c.e.t.Helper()
	var d struct{ Build map[string]any }
	c.expect(200, "GET", "/api/builds/"+seed.BuildID(n), nil).json(c.e.t, &d)
	b := d.Build
	groupIDs := []any{}
	for _, s := range b["shares"].([]any) {
		groupIDs = append(groupIDs, s.(map[string]any)["groupId"])
	}
	in := map[string]any{
		"lineupId": b["lineupId"], "description": b["description"], "visibility": b["visibility"], "groupIds": groupIDs,
		"counterPets": b["counterPets"], "formation": b["formation"], "placements": b["placements"], "ownPets": b["ownPets"],
		"skillOrder": b["skillOrder"], "speed": b["speed"], "recommendations": b["recommendations"],
	}
	for k, v := range patch {
		in[k] = v
	}
	return in
}

func count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// ---------- FR 4.6 build access ----------

func TestGuestSeesPublicBuildsOnly(t *testing.T) {
	e := setup(t)
	eq(t, e.guest().lineupBuilds(1, ""), []string{"b1"})
}

func TestMembersSeeTheirGroupsAndCreatorsTheirPrivateBuilds(t *testing.T) {
	e := setup(t)
	eq(t, e.as(2).lineupBuilds(1, ""), []string{"b1", "b2", "b7", "b8"})       // member of กิลด์มาม่า, creator of hidden b8
	eq(t, e.as(1).lineupBuilds(1, ""), []string{"b1", "b2", "b3", "b4", "b7"}) // both groups, owns private b4; b8 hidden
	eq(t, e.as(4).lineupBuilds(1, ""), []string{"b1"})                         // outside both groups
}

func TestBuildHiddenInOneGroupStaysVisibleThroughAnother(t *testing.T) {
	e := setup(t)
	eq(t, e.as(3).lineupBuilds(2, ""), []string{"b5"}) // only in Night Raid, where b6 is hidden
	if !slices.Contains(e.as(2).lineupBuilds(2, ""), "b6") {
		t.Fatal("กิลด์มาม่า member should see b6")
	}
}

func TestInaccessibleBuildLooksMissing(t *testing.T) {
	e := setup(t)
	a := e.as(4).expect(404, "GET", "/api/builds/"+seed.BuildID(4), nil)
	b := e.as(4).expect(404, "GET", "/api/builds/00000000-0000-0000-0000-000000000999", nil)
	eq(t, a.errCode(t), b.errCode(t))
}

func TestLineupCountsOnlyVisibleBuilds(t *testing.T) {
	e := setup(t)
	var list []struct {
		Lineup            struct{ ID string }
		VisibleBuildCount int
	}
	e.guest().expect(200, "GET", "/api/lineups", nil).json(t, &list)
	for _, l := range list {
		if l.Lineup.ID == seed.LineupID(1) && l.VisibleBuildCount != 1 {
			t.Fatalf("guest count for l1 = %d, want 1", l.VisibleBuildCount)
		}
	}
}

func TestBuildListPaging(t *testing.T) {
	e := setup(t)
	type page struct {
		Items       []struct{ Build struct{ ID string } }
		Total       int
		Page        int
		PageSize    int
		CounterPets []string
	}
	var p1, p3, big page
	path := "/api/lineups/" + seed.LineupID(3) + "/builds?sort=NEWEST"
	e.guest().expect(200, "GET", path+"&page=1", nil).json(t, &p1)
	e.guest().expect(200, "GET", path+"&page=3", nil).json(t, &p3)
	e.guest().expect(200, "GET", path+"&pageSize=500", nil).json(t, &big)
	eq(t, []int{p1.Total, len(p1.Items), p1.PageSize, len(p3.Items)}, []int{26, 9, 9, 8})
	eq(t, big.PageSize, 50) // capped
	eq(t, p1.CounterPets, []string{seed.Pet("taeo")})
	eq(t, p1.Items[0].Build.ID, big.Items[0].Build.ID)
}

// ---------- FR 3.2 duplicate lineups ----------

func TestDuplicateLineupIgnoresOrderWithinRow(t *testing.T) {
	e := setup(t)
	var out struct {
		Lineup  struct{ ID string }
		Existed bool
	}
	e.as(2).expect(200, "POST", "/api/lineups", map[string]any{"formation": "F4B1", "placements": []map[string]any{
		{"characterId": seed.Char("hayeon"), "row": "FRONT", "slot": 0},
		{"characterId": seed.Char("lubu"), "row": "FRONT", "slot": 3},
		{"characterId": seed.Char("soran"), "row": "BACK", "slot": 0},
	}}).json(t, &out)
	if !out.Existed || out.Lineup.ID != seed.LineupID(1) {
		t.Fatalf("want existing l1, got %+v", out)
	}
}

func TestCharacterInAnotherRowIsADifferentLineup(t *testing.T) {
	e := setup(t)
	var out struct{ Existed bool }
	e.as(2).expect(200, "POST", "/api/lineups", map[string]any{"formation": "F3B2", "placements": []map[string]any{
		{"characterId": seed.Char("lubu"), "row": "FRONT", "slot": 0},
		{"characterId": seed.Char("soran"), "row": "FRONT", "slot": 1},
		{"characterId": seed.Char("hayeon"), "row": "BACK", "slot": 0},
	}}).json(t, &out)
	if out.Existed {
		t.Fatal("should be a new lineup")
	}
}

// ---------- FR 4.2 enemy pet filter ----------

func TestEnemyPetFilterIncludesBuildsThatCounterAnyPet(t *testing.T) {
	e := setup(t)
	eq(t, e.as(1).lineupBuilds(1, "?enemyPet="+seed.Pet("yu")), []string{"b1", "b2", "b4"})
}

// ---------- FR 6 votes ----------

func TestOneVotePerUserReplaceAndCancel(t *testing.T) {
	e := setup(t)
	c := e.as(1)
	path := "/api/builds/" + seed.BuildID(1) + "/vote"
	before := count(t, `SELECT count(*) FROM votes WHERE build_id = $1`, seed.BuildID(1))
	c.expect(200, "PUT", path, map[string]string{"result": "WIN", "equipLevel": "EXACT_EQUIPMENT"})
	c.expect(200, "PUT", path, map[string]string{"result": "LOSS", "equipLevel": "CHARACTERS_ONLY"})
	var mine struct{ Result, EquipLevel string }
	if err := pool.QueryRow(context.Background(), `SELECT result, equip_level FROM votes WHERE build_id = $1 AND user_id = $2`,
		seed.BuildID(1), seed.UserID(1)).Scan(&mine.Result, &mine.EquipLevel); err != nil {
		t.Fatal(err)
	}
	eq(t, mine, struct{ Result, EquipLevel string }{"LOSS", "CHARACTERS_ONLY"})
	c.expect(204, "DELETE", path, nil)
	eq(t, count(t, `SELECT count(*) FROM votes WHERE build_id = $1`, seed.BuildID(1)), before)
}

func TestCannotVoteOnInvisibleBuild(t *testing.T) {
	e := setup(t)
	r := e.as(4).expect(404, "PUT", "/api/builds/"+seed.BuildID(2)+"/vote", map[string]string{"result": "WIN", "equipLevel": "EXACT_EQUIPMENT"})
	eq(t, r.errCode(t), "NOT_FOUND")
}

func TestDeletingBuildDeletesVotes(t *testing.T) {
	e := setup(t)
	e.as(1).expect(204, "DELETE", "/api/builds/"+seed.BuildID(1), nil)
	eq(t, count(t, `SELECT count(*) FROM votes WHERE build_id = $1`, seed.BuildID(1)), 0)
}

func TestStatsReportPerEquipmentLevelWithNA(t *testing.T) {
	e := setup(t)
	var d struct {
		StatsAll struct {
			Total  int
			Levels []struct {
				Level  string
				Total  int
				WinPct *float64
			}
		}
	}
	e.guest().expect(200, "GET", "/api/builds/"+seed.BuildID(1), nil).json(t, &d)
	sum := 0
	for _, l := range d.StatsAll.Levels {
		sum += l.Total
		if (l.Total == 0) != (l.WinPct == nil) {
			t.Fatalf("level %s: total %d but winPct %v", l.Level, l.Total, l.WinPct)
		}
	}
	eq(t, len(d.StatsAll.Levels), 3)
	eq(t, sum, d.StatsAll.Total)
}

// ---------- FR 4.5 visibility changes ----------

func TestChangingVisibilityTypeEndsAllShares(t *testing.T) {
	e := setup(t)
	c := e.as(1)
	c.expect(200, "PUT", "/api/builds/"+seed.BuildID(6), c.buildInput(6, map[string]any{"visibility": "PUBLIC", "groupIds": []string{}}))
	eq(t, count(t, `SELECT count(*) FROM build_shares WHERE build_id = $1`, seed.BuildID(6)), 0)
}

func TestRemovingOneGroupKeepsTheOthersHiddenFlag(t *testing.T) {
	e := setup(t)
	c := e.as(1)
	c.expect(200, "PUT", "/api/builds/"+seed.BuildID(6), c.buildInput(6, map[string]any{"groupIds": []string{seed.GroupID(2)}}))
	var hidden bool
	if err := pool.QueryRow(context.Background(), `SELECT hidden FROM build_shares WHERE build_id = $1 AND group_id = $2`, seed.BuildID(6), seed.GroupID(2)).Scan(&hidden); err != nil {
		t.Fatal(err)
	}
	eq(t, count(t, `SELECT count(*) FROM build_shares WHERE build_id = $1`, seed.BuildID(6)), 1)
	eq(t, hidden, true)
}

func TestCannotMoveBuildToAnotherLineup(t *testing.T) {
	e := setup(t)
	c := e.as(1)
	r := c.expect(400, "PUT", "/api/builds/"+seed.BuildID(1), c.buildInput(1, map[string]any{"lineupId": seed.LineupID(2)}))
	eq(t, r.errCode(t), "VALIDATION")
}

func TestOnlyCreatorCanEdit(t *testing.T) {
	e := setup(t)
	in := e.as(2).buildInput(1, nil) // b1 is public, so หมีน้อย can read it
	e.as(2).expect(403, "PUT", "/api/builds/"+seed.BuildID(1), in)
}

func TestBuildValidation(t *testing.T) {
	e := setup(t)
	c := e.as(1)
	cases := map[string]map[string]any{
		"skill of a character not in the team": {"skillOrder": []map[string]any{{"characterId": seed.Char("rudy"), "slot": 1}}},
		"skill slot other than upper/lower":    {"skillOrder": []map[string]any{{"characterId": seed.Char("yungon"), "slot": 3}}},
		"group the creator is not in":          {"visibility": "GROUP", "groupIds": []string{seed.GroupID(3)}},
		"group visibility without groups":      {"visibility": "GROUP", "groupIds": []string{}},
		"unknown pet":                          {"ownPets": []string{"999"}},
		"old minimum-speed rule":               {"speed": map[string]any{"kind": "MIN", "value": 100}},
	}
	for name, patch := range cases {
		t.Run(name, func(t *testing.T) {
			c.expect(400, "PUT", "/api/builds/"+seed.BuildID(1), c.buildInput(1, patch))
		})
	}
}

func TestSpeedRuleKindAndNote(t *testing.T) {
	e := setup(t)
	c := e.as(1)
	speedOf := func() any {
		var d struct{ Build map[string]any }
		c.expect(200, "GET", "/api/builds/"+seed.BuildID(1), nil).json(t, &d)
		return d.Build["speed"]
	}
	c.expect(200, "PUT", "/api/builds/"+seed.BuildID(1), c.buildInput(1, map[string]any{"speed": map[string]any{"kind": "SLOWER", "note": "  ช้ากว่ายุนกอนศัตรู  "}}))
	eq(t, speedOf(), map[string]any{"kind": "SLOWER", "note": "ช้ากว่ายุนกอนศัตรู"})
	c.expect(200, "PUT", "/api/builds/"+seed.BuildID(1), c.buildInput(1, map[string]any{"speed": map[string]any{"note": "สปีด 150+"}}))
	eq(t, speedOf(), map[string]any{"note": "สปีด 150+"})
	c.expect(200, "PUT", "/api/builds/"+seed.BuildID(1), c.buildInput(1, map[string]any{"speed": map[string]any{"kind": "", "note": " "}}))
	eq(t, speedOf(), nil)
}

// ---------- FR 5 groups ----------

func TestLeavingGroupDropsShareAndPrivatizes(t *testing.T) {
	e := setup(t)
	e.as(2).expect(204, "POST", "/api/groups/"+seed.GroupID(1)+"/leave", map[string]any{})
	eq(t, count(t, `SELECT count(*) FROM builds WHERE id = $1 AND visibility = 'PRIVATE'`, seed.BuildID(2)), 1)
	eq(t, count(t, `SELECT count(*) FROM build_shares WHERE build_id = $1`, seed.BuildID(2)), 0)
	eq(t, e.as(2).lineupBuilds(1, ""), []string{"b1", "b2", "b8"}) // still sees own builds, no group builds
}

func TestOwnerMustTransferBeforeLeaving(t *testing.T) {
	e := setup(t)
	r := e.as(1).expect(409, "POST", "/api/groups/"+seed.GroupID(1)+"/leave", map[string]any{})
	eq(t, r.errCode(t), "OWNER_MUST_TRANSFER")
}

func TestDeletingGroupLeavesOtherSharesUntouched(t *testing.T) {
	e := setup(t)
	e.as(1).expect(204, "DELETE", "/api/groups/"+seed.GroupID(1), nil)
	eq(t, count(t, `SELECT count(*) FROM builds WHERE id = $1 AND visibility = 'PRIVATE'`, seed.BuildID(2)), 1)
	eq(t, count(t, `SELECT count(*) FROM build_shares WHERE build_id = $1 AND group_id = $2 AND hidden`, seed.BuildID(6), seed.GroupID(2)), 1)
}

func TestOnlyOwnerCanHideBuilds(t *testing.T) {
	e := setup(t)
	e.as(2).expect(403, "PUT", "/api/groups/"+seed.GroupID(1)+"/builds/"+seed.BuildID(2)+"/hidden", map[string]bool{"hidden": true})
	e.as(1).expect(204, "PUT", "/api/groups/"+seed.GroupID(1)+"/builds/"+seed.BuildID(2)+"/hidden", map[string]bool{"hidden": true})
	if slices.Contains(e.as(1).lineupBuilds(1, ""), "b2") {
		t.Fatal("hidden build should disappear for group members")
	}
}

func TestNonMembersCannotSeeGroup(t *testing.T) {
	e := setup(t)
	e.as(4).expect(404, "GET", "/api/groups/"+seed.GroupID(1), nil)
}

func TestJoinByInviteCode(t *testing.T) {
	e := setup(t)
	e.as(4).expect(200, "POST", "/api/groups/join", map[string]string{"code": "mama7k"})
	if !slices.Contains(e.as(4).lineupBuilds(1, ""), "b2") {
		t.Fatal("new member should see the group's builds")
	}
}

// ---------- FR 2 accounts ----------

func TestAccountDeletion(t *testing.T) {
	e := setup(t)
	c := e.as(1)
	c.expect(200, "PUT", "/api/builds/"+seed.BuildID(5)+"/vote", map[string]string{"result": "WIN", "equipLevel": "EXACT_EQUIPMENT"})
	c.expect(204, "DELETE", "/api/me", nil)

	eq(t, count(t, `SELECT count(*) FROM builds WHERE id = $1 AND visibility = 'PUBLIC' AND creator_deleted AND creator_id IS NULL`, seed.BuildID(1)), 1)
	eq(t, count(t, `SELECT count(*) FROM builds WHERE id = $1 AND visibility = 'PRIVATE'`, seed.BuildID(6)), 1)
	eq(t, count(t, `SELECT count(*) FROM votes WHERE user_id = $1`, seed.UserID(1)), 0)
	eq(t, count(t, `SELECT count(*) FROM groups WHERE id = $1 AND owner_id = $2`, seed.GroupID(1), seed.UserID(2)), 1)

	eq(t, e.as(2).lineupBuilds(1, ""), []string{"b1", "b2", "b8"}) // b7 orphaned
	eq(t, e.as(2).lineupBuilds(2, ""), []string{"b5"})             // b6 orphaned
	c.expect(200, "GET", "/api/me", nil)                           // session gone: guest
	if got := strings.TrimSpace(string(c.do("GET", "/api/me", nil).body)); got != "null" {
		t.Fatalf("deleted user still signed in: %s", got)
	}
}

func TestSuspendedUserCannotLogIn(t *testing.T) {
	e := setup(t)
	r := e.guest().expect(403, "POST", "/api/auth/login", map[string]string{"username": "banned", "password": seed.Password})
	eq(t, r.errCode(t), "SUSPENDED")
}

func TestRegisterLoginLogout(t *testing.T) {
	e := setup(t)
	reg := e.guest().do("POST", "/api/auth/register", map[string]string{"username": "newbie_01", "password": "longenough", "displayName": "มือใหม่"})
	eq(t, reg.status, 200)
	e.guest().expect(409, "POST", "/api/auth/register", map[string]string{"username": "NEWBIE_01", "password": "longenough", "displayName": "x"})
	e.guest().expect(401, "POST", "/api/auth/login", map[string]string{"username": "newbie_01", "password": "wrong-password"})
	e.guest().expect(200, "POST", "/api/auth/login", map[string]string{"username": " Newbie_01 ", "password": "longenough"})
}

func TestLoginIsRateLimitedPerIP(t *testing.T) {
	e := setup(t)
	bad := map[string]string{"username": "mee", "password": "wrong-password"}
	for range 10 {
		e.guest().expect(401, "POST", "/api/auth/login", bad)
	}
	r := e.guest().expect(429, "POST", "/api/auth/login", map[string]string{"username": "mee", "password": seed.Password})
	eq(t, r.errCode(t), "RATE_LIMITED")
	e.guest().expect(200, "GET", "/api/master", nil) // reads are not limited
}

func TestUsernameRule(t *testing.T) {
	e := setup(t)
	// at most 5 per test: sign-up is rate-limited per IP
	for _, name := range []string{"ab", "has space", "ไทย", "plus+sign", "a_name_that_is_31_characters_xy"} {
		r := e.guest().expect(400, "POST", "/api/auth/register", map[string]string{"username": name, "password": "longenough", "displayName": "x"})
		eq(t, r.errCode(t), "VALIDATION")
	}
}

func TestUsernameAllowsCommonPunctuation(t *testing.T) {
	e := setup(t)
	for _, name := range []string{"p.supsri", "night-raid_01", "Someone@Mail.com"} {
		var u struct{ Username string }
		e.guest().expect(200, "POST", "/api/auth/register", map[string]string{"username": name, "password": "longenough", "displayName": "x"}).json(t, &u)
		eq(t, u.Username, strings.ToLower(name))
	}
}

func TestNewUserCanCreateRightAway(t *testing.T) {
	e := setup(t)
	reg := e.guest().do("POST", "/api/auth/register", map[string]string{"username": "fresh", "password": "longenough", "displayName": "สด"})
	eq(t, reg.status, 200)
	c := &client{e: e, token: reg.cookie}
	c.expect(200, "PUT", "/api/builds/"+seed.BuildID(1)+"/vote", map[string]string{"result": "WIN", "equipLevel": "EXACT_EQUIPMENT"})
}

func TestChangePasswordKeepsThisSessionOnly(t *testing.T) {
	e := setup(t)
	login := func(pw string) resp {
		return e.guest().do("POST", "/api/auth/login", map[string]string{"username": "mee", "password": pw})
	}
	first := &client{e: e, token: login(seed.Password).cookie}
	other := &client{e: e, token: login(seed.Password).cookie}
	r := first.expect(400, "POST", "/api/me/password", map[string]string{"currentPassword": "wrong-one", "newPassword": "brand-new-pw"})
	eq(t, r.errCode(t), "VALIDATION")
	first.expect(200, "POST", "/api/me/password", map[string]string{"currentPassword": seed.Password, "newPassword": "brand-new-pw"})
	first.expect(200, "GET", "/api/builds/"+seed.BuildID(2), nil) // still signed in (b2 is a group build)
	if got := strings.TrimSpace(string(other.do("GET", "/api/me", nil).body)); got != "null" {
		t.Fatalf("other session still signed in: %s", got)
	}
	eq(t, login(seed.Password).status, 401)
	eq(t, login("brand-new-pw").status, 200)
}

func TestSuperAdminResetsPassword(t *testing.T) {
	e := setup(t)
	victim := e.as(2)
	e.as(3).expect(403, "POST", "/api/admin/users/"+seed.UserID(2)+"/reset-password", map[string]any{}) // moderator
	var out struct{ TemporaryPassword string }
	e.as(1).expect(200, "POST", "/api/admin/users/"+seed.UserID(2)+"/reset-password", map[string]any{}).json(t, &out)
	if got := strings.TrimSpace(string(victim.do("GET", "/api/me", nil).body)); got != "null" {
		t.Fatalf("old session survived the reset: %s", got)
	}
	r := e.guest().expect(200, "POST", "/api/auth/login", map[string]string{"username": "mee", "password": out.TemporaryPassword})
	var me struct{ MustChangePassword bool }
	r.json(t, &me)
	eq(t, me.MustChangePassword, true)
	c := &client{e: e, token: r.cookie}
	var after struct{ MustChangePassword bool }
	c.expect(200, "POST", "/api/me/password", map[string]string{"currentPassword": out.TemporaryPassword, "newPassword": "my-own-password"}).json(t, &after)
	eq(t, after.MustChangePassword, false)
}

func TestGuestCannotWrite(t *testing.T) {
	e := setup(t)
	e.guest().expect(401, "POST", "/api/groups", map[string]string{"name": "x"})
}

func TestStateChangesRequireJSON(t *testing.T) {
	e := setup(t)
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/groups", strings.NewReader("name=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	eq(t, res.StatusCode, http.StatusUnsupportedMediaType)
}

// ---------- FR 8–9 admin ----------

func TestAdminRequiresStaffRole(t *testing.T) {
	e := setup(t)
	e.as(2).expect(403, "GET", "/api/admin/users", nil)
	e.as(3).expect(403, "GET", "/api/admin/audit", nil) // moderator: no audit log
}

func TestMergeLineupsAndDeleteOnlyEmpty(t *testing.T) {
	e := setup(t)
	m := e.as(3)
	r := m.expect(409, "DELETE", "/api/admin/lineups/"+seed.LineupID(1), nil)
	eq(t, r.errCode(t), "HAS_BUILDS")
	m.expect(200, "POST", "/api/admin/lineups/"+seed.LineupID(2)+"/merge", map[string]string{"targetId": seed.LineupID(1)})
	eq(t, count(t, `SELECT count(*) FROM lineups WHERE id = $1`, seed.LineupID(2)), 0)
	eq(t, count(t, `SELECT count(*) FROM builds WHERE lineup_id = $1`, seed.LineupID(1)), 8)
	eq(t, count(t, `SELECT count(*) FROM audit_log WHERE action = 'LINEUP_MERGE'`), 1)
}

func TestImportPreviewAndAllOrNothingCommit(t *testing.T) {
	e := setup(t)
	a := e.as(1)
	csv := "id,name_th,name_en,active\n" + seed.Pet("yu") + ",ยูใหม่,Yu,true\n100,ใหม่,,\npet_x,x,,"
	var rows []struct{ Status string }
	a.expect(200, "POST", "/api/admin/master/pets/import/preview", map[string]string{"csv": csv}).json(t, &rows)
	statuses := []string{}
	for _, r := range rows {
		statuses = append(statuses, r.Status)
	}
	eq(t, statuses, []string{"UPDATE", "CREATE", "ERROR"})
	a.expect(400, "POST", "/api/admin/master/pets/import/commit", map[string]string{"csv": csv})
	eq(t, count(t, `SELECT count(*) FROM pets WHERE id = '100'`), 0) // nothing written

	good := strings.Join(strings.Split(csv, "\n")[:3], "\n")
	var res map[string]int
	a.expect(200, "POST", "/api/admin/master/pets/import/commit", map[string]string{"csv": good}).json(t, &res)
	eq(t, res, map[string]int{"created": 1, "updated": 1})
	eq(t, count(t, `SELECT count(*) FROM pets WHERE id = $1 AND name_th = 'ยูใหม่' AND name_en = 'Yu'`, seed.Pet("yu")), 1)
}

func TestAdminSeesOnlyPublicBuilds(t *testing.T) {
	e := setup(t)
	var list []struct {
		Build struct{ ID, Visibility string }
	}
	e.as(1).expect(200, "GET", "/api/admin/builds", nil).json(t, &list)
	for _, b := range list {
		if b.Build.Visibility != "PUBLIC" {
			t.Fatalf("admin list leaked %s build %s", b.Build.Visibility, b.Build.ID)
		}
	}
	e.as(1).expect(404, "PATCH", "/api/admin/builds/"+seed.BuildID(2), map[string]bool{"hidden": true})
}

func TestSuspendRevokesSessions(t *testing.T) {
	e := setup(t)
	victim := e.as(2)
	e.as(1).expect(200, "PATCH", "/api/admin/users/"+seed.UserID(2), map[string]bool{"suspended": true})
	if got := strings.TrimSpace(string(victim.do("GET", "/api/me", nil).body)); got != "null" {
		t.Fatalf("suspended user still signed in: %s", got)
	}
}

func TestImportImagePaths(t *testing.T) {
	e := setup(t)
	a := e.as(1)
	yungon := seed.Char("yungon")
	csv := "id,name_th,name_en,image,active\n" +
		yungon + ",ยุนกอน,,master/characters/039_yungon.webp,\n" +
		"100,ใหม่,,../secret.webp,\n"
	var rows []struct{ Status, Error string }
	a.expect(200, "POST", "/api/admin/master/characters/import/preview", map[string]string{"csv": csv}).json(t, &rows)
	eq(t, []string{rows[0].Status, rows[1].Status}, []string{"UPDATE", "ERROR"}) // path traversal rejected

	good := strings.Join(strings.Split(csv, "\n")[:2], "\n")
	a.expect(200, "POST", "/api/admin/master/characters/import/commit", map[string]string{"csv": good})
	image := func() string {
		var m struct{ Characters []struct{ ID, Image string } }
		e.guest().expect(200, "GET", "/api/master", nil).json(t, &m)
		for _, c := range m.Characters {
			if c.ID == yungon {
				return c.Image
			}
		}
		return ""
	}
	eq(t, image(), "/media/master/characters/039_yungon.webp") // joined to MEDIA_PUBLIC_URL (default /media)

	// An empty image cell keeps the current image.
	a.expect(200, "POST", "/api/admin/master/characters/import/commit", map[string]string{"csv": "id,name_th,image\n" + yungon + ",ยุนกอน 2,"})
	eq(t, image(), "/media/master/characters/039_yungon.webp")
}

func TestCreateMasterTakesNextID(t *testing.T) {
	e := setup(t)
	var out struct{ ID string }
	e.as(1).expect(200, "POST", "/api/admin/master/pets", map[string]string{"nameTh": "ใหม่", "nameEn": "New"}).json(t, &out)
	eq(t, out.ID, "7") // six sample pets
	eq(t, count(t, `SELECT count(*) FROM pets WHERE id = '7' AND name_en = 'New'`), 1)
	e.as(1).expect(400, "POST", "/api/admin/master/pets", map[string]string{"nameTh": " "})
}

func TestListLineupsWithoutFilterReturnsAll(t *testing.T) {
	e := setup(t)
	var all, some []struct{ Lineup struct{ ID string } }
	e.guest().expect(200, "GET", "/api/lineups", nil).json(t, &all)
	eq(t, len(all), 4)
	e.guest().expect(200, "GET", "/api/lineups?characters="+seed.Char("lubu")+","+seed.Char("soran"), nil).json(t, &some)
	eq(t, len(some), 1)
}

func TestServesWebAppWithClientRoutes(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/assets", 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(dir+"/index.html", []byte("<html>app</html>"), 0o644)
	_ = os.WriteFile(dir+"/assets/app-123.js", []byte("console.log(1)"), 0o644)
	srv := httptest.NewServer(app.NewServer(config.Config{SessionTTL: time.Hour, UploadDir: t.TempDir(), WebDir: dir}, pool).Handler())
	t.Cleanup(srv.Close)
	get := func(path string) (int, string, http.Header) {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b), res.Header
	}
	for _, p := range []string{"/", "/lineups/abc", "/admin/master"} {
		if code, body, h := get(p); code != 200 || body != "<html>app</html>" || h.Get("Cache-Control") != "no-cache" {
			t.Fatalf("%s: %d %q %q", p, code, body, h.Get("Cache-Control"))
		}
	}
	if code, body, h := get("/assets/app-123.js"); code != 200 || body != "console.log(1)" || !strings.Contains(h.Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %d %q %q", code, body, h.Get("Cache-Control"))
	}
	if code, body, _ := get("/api/nope"); code != 404 || !strings.Contains(body, "NOT_FOUND") {
		t.Fatalf("unknown API path: %d %s", code, body)
	}
	if code, _, _ := get("/api/health"); code != 200 {
		t.Fatalf("health: %d", code)
	}
}
