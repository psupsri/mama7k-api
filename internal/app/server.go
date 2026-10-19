// Package app holds the HTTP API: routing, auth, and the business rules from the FR document.
package app

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"mama7k/api/internal/config"
)

type Server struct {
	cfg    config.Config
	db     *pgxpool.Pool
	images ImageStore
	limits rateLimits
	now    func() time.Time
}

func NewServer(cfg config.Config, db *pgxpool.Pool) *Server {
	if cfg.MediaBaseURL != "" {
		mediaBaseURL = cfg.MediaBaseURL
	}
	var images ImageStore = LocalImageStore{Dir: cfg.UploadDir}
	if cfg.R2Enabled() {
		images = R2ImageStore{AccountID: cfg.R2AccountID, AccessKeyID: cfg.R2AccessKeyID, SecretAccessKey: cfg.R2SecretAccessKey,
			Bucket: cfg.R2Bucket, Prefix: cfg.R2UploadPrefix}
	}
	return &Server{cfg: cfg, db: db, images: images, limits: defaultRateLimits(), now: time.Now}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	h := func(pattern string, fn handlerFunc) { mux.Handle(pattern, s.wrap(fn)) }

	h("GET /api/health", func(w http.ResponseWriter, r *http.Request) error {
		return ok(w, map[string]string{"status": "ok"})
	})

	// Auth & profile (FR 2)
	h("POST /api/auth/register", s.register)
	h("POST /api/auth/login", s.login)
	h("POST /api/auth/logout", s.logout)
	h("GET /api/me", s.me)
	h("PATCH /api/me", s.updateProfile)
	h("DELETE /api/me", s.deleteAccount)
	h("POST /api/me/password", s.changePassword)

	// Master data (FR 9)
	h("GET /api/master", s.getMaster)

	// Feedback: anyone sends, staff reads
	h("POST /api/feedback", s.sendFeedback)

	// Defense lineups (FR 3)
	h("GET /api/lineups", s.listLineups)
	h("POST /api/lineups", s.createLineup)
	h("GET /api/lineups/{id}", s.getLineup)
	h("GET /api/lineups/{id}/builds", s.listLineupBuilds)

	// Offense builds & votes (FR 4, 6)
	h("GET /api/me/builds", s.listMyBuilds)
	h("POST /api/builds", s.createBuild)
	h("GET /api/builds/{id}", s.getBuild)
	h("PUT /api/builds/{id}", s.updateBuild)
	h("DELETE /api/builds/{id}", s.deleteBuild)
	h("PUT /api/builds/{id}/vote", s.castVote)
	h("DELETE /api/builds/{id}/vote", s.cancelVote)

	// Groups (FR 5)
	h("GET /api/groups", s.listMyGroups)
	h("POST /api/groups", s.createGroup)
	h("POST /api/groups/join", s.joinGroup)
	h("GET /api/groups/{id}", s.getGroup)
	h("PATCH /api/groups/{id}", s.renameGroup)
	h("DELETE /api/groups/{id}", s.deleteGroup)
	h("POST /api/groups/{id}/leave", s.leaveGroup)
	h("POST /api/groups/{id}/transfer", s.transferOwner)
	h("POST /api/groups/{id}/invite-code", s.regenerateInvite)
	h("DELETE /api/groups/{id}/members/{userId}", s.kickMember)
	h("PUT /api/groups/{id}/builds/{buildId}/hidden", s.setBuildHidden)

	// Admin (FR 8–9)
	h("GET /api/admin/users", s.adminListUsers)
	h("PATCH /api/admin/users/{id}", s.adminUpdateUser)
	h("POST /api/admin/users/{id}/reset-password", s.adminResetPassword)
	h("GET /api/admin/builds", s.adminListPublicBuilds)
	h("PATCH /api/admin/builds/{id}", s.adminSetBuildHidden)
	h("DELETE /api/admin/builds/{id}", s.adminDeleteBuild)
	h("GET /api/admin/lineups", s.adminListLineups)
	h("PUT /api/admin/lineups/{id}", s.adminUpdateLineup)
	h("POST /api/admin/lineups/{id}/merge", s.adminMergeLineup)
	h("DELETE /api/admin/lineups/{id}", s.adminDeleteLineup)
	h("POST /api/admin/master/{entity}", s.adminCreateMaster)
	h("PUT /api/admin/master/{entity}/{id}", s.adminSaveMaster)
	h("PATCH /api/admin/master/{entity}/{id}", s.adminSetMasterActive)
	h("POST /api/admin/master/{entity}/{id}/image", s.adminUploadImage)
	h("POST /api/admin/master/{entity}/import/preview", s.adminPreviewImport)
	h("POST /api/admin/master/{entity}/import/commit", s.adminCommitImport)
	h("GET /api/admin/audit", s.adminAuditLog)
	h("GET /api/admin/feedback", s.adminListFeedback)
	h("PATCH /api/admin/feedback/{id}", s.adminResolveFeedback)
	h("DELETE /api/admin/feedback/{id}", s.adminDeleteFeedback)

	mux.Handle("GET /media/", http.StripPrefix("/media/", http.FileServer(http.Dir(s.cfg.UploadDir))))
	mux.Handle("GET /api/", s.wrap(func(http.ResponseWriter, *http.Request) error { return errNotFound("ไม่พบ API นี้") }))
	if s.cfg.WebDir != "" {
		mux.Handle("GET /", webApp(s.cfg.WebDir))
	}

	return recoverer(s.requestLogger(s.rateLimit(csrfGuard(s.sessionMiddleware(mux)))))
}
