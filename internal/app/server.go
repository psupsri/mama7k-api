// Package app holds the HTTP API: routing, auth, and the business rules from the FR document.
package app

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"mama7k/api/internal/config"
)

// Mailer sends transactional email (verification, password reset).
type Mailer interface {
	Send(to, subject, body string) error
}

// LogMailer writes emails to the log; used in development and tests.
type LogMailer struct{}

func (LogMailer) Send(to, subject, body string) error {
	slog.Info("email", "to", to, "subject", subject, "body", body)
	return nil
}

type Server struct {
	cfg    config.Config
	db     *pgxpool.Pool
	mailer Mailer
	now    func() time.Time
}

func NewServer(cfg config.Config, db *pgxpool.Pool, mailer Mailer) *Server {
	return &Server{cfg: cfg, db: db, mailer: mailer, now: time.Now}
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
	h("POST /api/auth/verify-email", s.verifyEmail)
	h("POST /api/auth/resend-verification", s.resendVerification)
	h("POST /api/auth/password-reset", s.requestPasswordReset)
	h("POST /api/auth/password-reset/confirm", s.confirmPasswordReset)
	h("GET /api/me", s.me)
	h("PATCH /api/me", s.updateProfile)
	h("DELETE /api/me", s.deleteAccount)

	// Master data (FR 9)
	h("GET /api/master", s.getMaster)

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
	h("GET /api/admin/builds", s.adminListPublicBuilds)
	h("PATCH /api/admin/builds/{id}", s.adminSetBuildHidden)
	h("DELETE /api/admin/builds/{id}", s.adminDeleteBuild)
	h("GET /api/admin/lineups", s.adminListLineups)
	h("PUT /api/admin/lineups/{id}", s.adminUpdateLineup)
	h("POST /api/admin/lineups/{id}/merge", s.adminMergeLineup)
	h("DELETE /api/admin/lineups/{id}", s.adminDeleteLineup)
	h("PUT /api/admin/master/{entity}/{code}", s.adminSaveMaster)
	h("PATCH /api/admin/master/{entity}/{code}", s.adminSetMasterActive)
	h("POST /api/admin/master/{entity}/{code}/image", s.adminUploadImage)
	h("POST /api/admin/master/{entity}/import/preview", s.adminPreviewImport)
	h("POST /api/admin/master/{entity}/import/commit", s.adminCommitImport)
	h("GET /api/admin/audit", s.adminAuditLog)

	mux.Handle("GET /media/", http.StripPrefix("/media/", http.FileServer(http.Dir(s.cfg.UploadDir))))

	return recoverer(requestLogger(csrfGuard(s.sessionMiddleware(mux))))
}
