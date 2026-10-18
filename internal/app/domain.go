package app

import "time"

// JSON shapes mirror web/src/types/domain.ts so the web client can switch from the mock
// API without UI changes. Keep the two in sync.

type Role string

const (
	RoleUser       Role = "USER"
	RoleModerator  Role = "MODERATOR"
	RoleSuperAdmin Role = "SUPER_ADMIN"
)

type User struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	DisplayName   string `json:"displayName"`
	Role          Role   `json:"role"`
	Suspended     bool   `json:"suspended"`
	Deleted       bool   `json:"deleted"`
	EmailVerified bool   `json:"emailVerified"`
}

// ---------- master data ----------

type MasterItem struct {
	ID      string   `json:"id"`
	Code    string   `json:"code"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
	Image   *string  `json:"image,omitempty"`
	Active  bool     `json:"active"`
}

type Skill struct {
	MasterItem
	CharacterID string `json:"characterId"`
	Slot        int    `json:"slot"`
}

type EquipmentSet struct {
	MasterItem
	Effect2 string `json:"effect2"`
	Effect4 string `json:"effect4"`
}

type Ring struct {
	MasterItem
	Rarity string `json:"rarity"`
	Effect string `json:"effect"`
}

type StatType struct {
	MasterItem
	Unit string `json:"unit"`
}

type MasterData struct {
	Characters []MasterItem   `json:"characters"`
	Skills     []Skill        `json:"skills"`
	Pets       []MasterItem   `json:"pets"`
	Sets       []EquipmentSet `json:"sets"`
	Rings      []Ring         `json:"rings"`
	Stats      []StatType     `json:"stats"`
}

// ---------- formation / lineups ----------

type Placement struct {
	CharacterID string `json:"characterId"`
	Row         string `json:"row"` // FRONT | BACK
	Slot        int    `json:"slot"`
}

type DefenseLineup struct {
	ID         string      `json:"id"`
	Formation  string      `json:"formation"`
	Placements []Placement `json:"placements"`
	CreatedBy  *string     `json:"createdBy"`
	CreatedAt  time.Time   `json:"createdAt"`
}

type LineupSummary struct {
	Lineup            DefenseLineup `json:"lineup"`
	VisibleBuildCount int           `json:"visibleBuildCount"`
}

// ---------- builds ----------

type Stack struct {
	RingID string `json:"ringId"`
	Star   *int   `json:"star,omitempty"`
}

type RingOption struct {
	RingID string `json:"ringId"`
	Star   int    `json:"star"`
	Stack  *Stack `json:"stack,omitempty"`
}

type StatTarget struct {
	StatID string   `json:"statId"`
	Value  *float64 `json:"value,omitempty"` // nil = "เน้น" (focus, no target number)
}

type Recommendation struct {
	CharacterID       string       `json:"characterId"`
	Sets              []string     `json:"sets"`
	Stats             []StatTarget `json:"stats"`
	Rings             []RingOption `json:"rings"`
	RingStackRequired bool         `json:"ringStackRequired"`
	Note              string       `json:"note"`
}

type SpeedRule struct {
	Kind  string `json:"kind"` // FASTER | MIN
	Value int    `json:"value,omitempty"`
}

// Team is stored as JSONB in builds.team.
type Team struct {
	Placements      []Placement      `json:"placements"`
	OwnPets         []string         `json:"ownPets"`
	SkillOrder      []string         `json:"skillOrder"`
	Speed           *SpeedRule       `json:"speed"`
	Recommendations []Recommendation `json:"recommendations"`
}

type BuildShare struct {
	GroupID string `json:"groupId"`
	Hidden  bool   `json:"hidden"`
}

type OffenseBuild struct {
	ID             string       `json:"id"`
	LineupID       string       `json:"lineupId"`
	Description    string       `json:"description"`
	CreatorID      *string      `json:"creatorId"`
	CreatorDeleted bool         `json:"creatorDeleted"`
	Visibility     string       `json:"visibility"`
	Shares         []BuildShare `json:"shares"`
	AdminHidden    bool         `json:"adminHidden"`
	CounterPets    []string     `json:"counterPets"`
	Formation      string       `json:"formation"`
	Team
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ---------- votes / stats ----------

type Vote struct {
	ID         string    `json:"id"`
	BuildID    string    `json:"buildId"`
	UserID     *string   `json:"userId"`
	Result     string    `json:"result"`
	EquipLevel string    `json:"equipLevel"`
	VotedAt    time.Time `json:"votedAt"`
}

type LevelStats struct {
	Level  string   `json:"level"`
	Win    int      `json:"win"`
	Loss   int      `json:"loss"`
	Total  int      `json:"total"`
	WinPct *float64 `json:"winPct"` // nil = N/A
}

type BuildStats struct {
	Window        string       `json:"window"` // ALL | D14
	Levels        []LevelStats `json:"levels"`
	Total         int          `json:"total"`
	OverallWinPct *float64     `json:"overallWinPct"`
}

type BuildSummary struct {
	Build       OffenseBuild `json:"build"`
	CreatorName string       `json:"creatorName"`
	GroupNames  []string     `json:"groupNames"`
	Stats14     BuildStats   `json:"stats14"`
	IsMine      bool         `json:"isMine"`
}

type BuildDetail struct {
	BuildSummary
	Lineup   DefenseLineup `json:"lineup"`
	StatsAll BuildStats    `json:"statsAll"`
	MyVote   *Vote         `json:"myVote"`
	CanEdit  bool          `json:"canEdit"`
	CanVote  bool          `json:"canVote"`
}

// ---------- groups ----------

type Group struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	OwnerID    string    `json:"ownerId"`
	InviteCode string    `json:"inviteCode"`
	CreatedAt  time.Time `json:"createdAt"`
}

type GroupSummary struct {
	Group       Group `json:"group"`
	MemberCount int   `json:"memberCount"`
	IsOwner     bool  `json:"isOwner"`
}

type GroupMemberView struct {
	User struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
	} `json:"user"`
	JoinedAt time.Time `json:"joinedAt"`
	IsOwner  bool      `json:"isOwner"`
}

type GroupBuild struct {
	Summary BuildSummary `json:"summary"`
	Hidden  bool         `json:"hidden"`
}

type GroupDetail struct {
	GroupSummary
	Members []GroupMemberView `json:"members"`
	Builds  []GroupBuild      `json:"builds"`
}
