package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lumen-dev02/team-task-manager/internal/auth"
	"github.com/lumen-dev02/team-task-manager/internal/domain"
	"github.com/lumen-dev02/team-task-manager/internal/store"
	"golang.org/x/time/rate"
)

type API struct {
	store     *store.Store
	tokens    *auth.Tokens
	log       *slog.Logger
	authLimit *rate.Limiter
	dummyHash string
}

type contextKey int

const userKey contextKey = iota

func New(s *store.Store, tokens *auth.Tokens, log *slog.Logger) http.Handler {
	// Reuse a valid hash to make missing-account and bad-password checks comparable.
	dummy, err := auth.Hash(rand.Text())
	if err != nil {
		panic(err)
	}
	a := &API{store: s, tokens: tokens, log: log, authLimit: rate.NewLimiter(2, 5), dummyHash: dummy}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { write(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /readyz", a.ready)
	mux.Handle("POST /api/v1/auth/register", a.limit(http.HandlerFunc(a.register)))
	mux.Handle("POST /api/v1/auth/login", a.limit(http.HandlerFunc(a.login)))
	a.route(mux, "GET /api/v1/me", a.me)
	a.route(mux, "POST /api/v1/projects", a.createProject)
	a.route(mux, "GET /api/v1/projects", a.projects)
	a.route(mux, "GET /api/v1/projects/{projectID}", a.project)
	a.route(mux, "DELETE /api/v1/projects/{projectID}", a.deleteProject)
	a.route(mux, "GET /api/v1/projects/{projectID}/members", a.members)
	a.route(mux, "POST /api/v1/projects/{projectID}/members", a.addMember)
	a.route(mux, "DELETE /api/v1/projects/{projectID}/members/{memberID}", a.removeMember)
	a.route(mux, "POST /api/v1/projects/{projectID}/tasks", a.createTask)
	a.route(mux, "GET /api/v1/projects/{projectID}/tasks", a.tasks)
	a.route(mux, "GET /api/v1/projects/{projectID}/tasks/{taskID}", a.task)
	a.route(mux, "PUT /api/v1/projects/{projectID}/tasks/{taskID}", a.updateTask)
	a.route(mux, "DELETE /api/v1/projects/{projectID}/tasks/{taskID}", a.deleteTask)
	a.route(mux, "GET /api/v1/projects/{projectID}/tasks/{taskID}/history", a.history)
	return a.middleware(mux)
}

func (a *API) route(mux *http.ServeMux, pattern string, h http.HandlerFunc) {
	mux.Handle(pattern, a.authenticate(h))
}
func userID(r *http.Request) int64 { return r.Context().Value(userKey).(int64) }

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func problem(w http.ResponseWriter, status int, code, message string) {
	write(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		problem(w, 404, "not_found", "Resource not found")
	case errors.Is(err, domain.ErrForbidden):
		problem(w, 403, "forbidden", "Owner role required")
	case errors.Is(err, domain.ErrConflict):
		problem(w, 409, "conflict", "Resource already exists or task version is outdated")
	case errors.Is(err, domain.ErrInvalid):
		problem(w, 400, "invalid_input", "Assignee must be a project member; the owner cannot be removed")
	case errors.Is(err, context.DeadlineExceeded):
		problem(w, 503, "unavailable", "Request timed out")
	default:
		a.log.Error("request failed", "error", err, "request_id", w.Header().Get("X-Request-ID"), "path", r.URL.Path)
		problem(w, 500, "internal_error", "Internal server error")
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		problem(w, 415, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	err = d.Decode(v)
	if err == nil {
		var extra any
		if err = d.Decode(&extra); errors.Is(err, io.EOF) {
			return true
		}
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		problem(w, 413, "body_too_large", "Body limit is 1 MiB")
	} else {
		problem(w, 400, "invalid_json", "Expected one JSON object with known fields")
	}
	return false
}

func pathID(w http.ResponseWriter, r *http.Request, key string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(key), 10, 64)
	if err != nil || id <= 0 {
		problem(w, 400, "invalid_id", "IDs must be positive integers")
		return 0, false
	}
	return id, true
}

func pagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	limit, offset := 20, 0
	for key, dest := range map[string]*int{"limit": &limit, "offset": &offset} {
		if values, ok := r.URL.Query()[key]; ok {
			n, err := strconv.Atoi(values[0])
			if err != nil {
				problem(w, 400, "invalid_pagination", "limit: 1..100; offset: 0..1000000")
				return 0, 0, false
			}
			*dest = n
		}
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 1000000 {
		problem(w, 400, "invalid_pagination", "limit: 1..100; offset: 0..1000000")
		return 0, 0, false
	}
	return limit, offset, true
}

func (a *API) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			w.Header().Set("WWW-Authenticate", "Bearer")
			problem(w, 401, "unauthorized", "Bearer token required")
			return
		}
		id, err := a.tokens.Verify(parts[1])
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			problem(w, 401, "unauthorized", "Invalid or expired token")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, id)))
	})
}

func (a *API) limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.authLimit.Allow() {
			w.Header().Set("Retry-After", "1")
			problem(w, 429, "rate_limited", "Try again later")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}

func (a *API) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w}
		w.Header().Set("X-Request-ID", rand.Text())
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		defer func() {
			if p := recover(); p != nil {
				a.log.Error("panic in handler", "panic", p, "request_id", w.Header().Get("X-Request-ID"))
				if rw.status == 0 {
					problem(rw, 500, "internal_error", "Internal server error")
				}
			}
			a.log.Info("http request", "method", r.Method, "path", r.URL.Path, "status", rw.status, "duration", time.Since(start), "request_id", w.Header().Get("X-Request-ID"))
		}()
		next.ServeHTTP(rw, r.WithContext(ctx))
	})
}

func (a *API) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := a.store.DB.Ping(ctx); err != nil {
		problem(w, 503, "unavailable", "Database unavailable")
		return
	}
	write(w, 200, map[string]string{"status": "ok"})
}
