package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/jackc/pgx/v5"
)

// Error is an API error with a stable code the web client can switch on.
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errValidation(msg string) *Error { return &Error{http.StatusBadRequest, "VALIDATION", msg} }
func errUnauthenticated() *Error {
	return &Error{http.StatusUnauthorized, "UNAUTHENTICATED", "กรุณาเข้าสู่ระบบ"}
}
func errForbidden(msg string) *Error      { return &Error{http.StatusForbidden, "FORBIDDEN", msg} }
func errNotFound(msg string) *Error       { return &Error{http.StatusNotFound, "NOT_FOUND", msg} }
func errConflict(code, msg string) *Error { return &Error{http.StatusConflict, code, msg} }

// handlerFunc returns an error instead of writing it; wrap turns it into a JSON response.
type handlerFunc func(w http.ResponseWriter, r *http.Request) error

func (s *Server) wrap(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := h(w, r)
		if err == nil {
			return
		}
		var apiErr *Error
		switch {
		case errors.As(err, &apiErr):
		case errors.Is(err, pgx.ErrNoRows), isInvalidUUID(err):
			// A malformed id in the path is just a resource that does not exist.
			apiErr = errNotFound("ไม่พบข้อมูล")
		default:
			slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
			apiErr = &Error{http.StatusInternalServerError, "INTERNAL", "เกิดข้อผิดพลาดในระบบ"}
		}
		writeJSON(w, apiErr.Status, map[string]any{"error": apiErr})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, v any) error {
	writeJSON(w, http.StatusOK, v)
	return nil
}

func noContent(w http.ResponseWriter) error {
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func readJSON(r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errValidation("ข้อมูลไม่ถูกต้อง: " + err.Error())
	}
	return nil
}

// ---------- middleware ----------

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				slog.Error("panic", "value", v, "stack", string(debug.Stack()))
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": &Error{Code: "INTERNAL", Message: "เกิดข้อผิดพลาดในระบบ"}})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", rec.status, "ms", time.Since(start).Milliseconds())
	})
}

// csrfGuard requires a JSON body on state-changing requests. Browsers cannot send
// cross-site application/json without a CORS preflight, which this API never allows.
// Combined with the SameSite=Lax session cookie this blocks CSRF.
func csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if mt != "application/json" && mt != "multipart/form-data" {
				writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{"error": &Error{Code: "UNSUPPORTED_MEDIA_TYPE", Message: "ต้องส่งข้อมูลเป็น JSON"}})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- request context ----------

type ctxKey int

const userKey ctxKey = 1

func withUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// viewer returns the signed-in user, or nil for guests.
func viewer(r *http.Request) *User {
	u, _ := r.Context().Value(userKey).(*User)
	return u
}

func requireUser(r *http.Request) (*User, error) {
	if u := viewer(r); u != nil {
		return u, nil
	}
	return nil, errUnauthenticated()
}

func requireVerified(r *http.Request) (*User, error) {
	u, err := requireUser(r)
	if err != nil {
		return nil, err
	}
	if !u.EmailVerified {
		return nil, &Error{http.StatusForbidden, "EMAIL_NOT_VERIFIED", "กรุณายืนยันอีเมลก่อน"}
	}
	return u, nil
}

func requireAdmin(r *http.Request, superOnly bool) (*User, error) {
	u, err := requireUser(r)
	if err != nil {
		return nil, err
	}
	if u.Role == RoleUser || (superOnly && u.Role != RoleSuperAdmin) {
		return nil, errForbidden("ไม่มีสิทธิ์")
	}
	return u, nil
}
