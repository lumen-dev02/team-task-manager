package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lumen-dev02/team-task-manager/internal/auth"
	"github.com/lumen-dev02/team-task-manager/internal/domain"
	"golang.org/x/time/rate"
)

func TestDecode(t *testing.T) {
	for _, tc := range []struct {
		name, body, contentType string
		want                    int
	}{
		{"valid", `{"title":"test"}`, "application/json", 200},
		{"charset", `{"title":"test"}`, "application/json; charset=utf-8", 200},
		{"unknown field", `{"admin":true}`, "application/json", 400},
		{"two objects", `{} {}`, "application/json", 400},
		{"trailing garbage", `{} x`, "application/json", 400},
		{"array", `[]`, "application/json", 400},
		{"empty", "", "application/json", 400},
		{"wrong type", `{"title":12}`, "application/json", 400},
		{"content type", `{}`, "text/plain", 415},
		{"large", `{"title":"` + strings.Repeat("a", 1<<20) + `"}`, "application/json", 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.contentType)
			w := httptest.NewRecorder()
			var in struct {
				Title string `json:"title"`
			}
			ok := decode(w, r, &in)
			if ok != (tc.want == 200) || w.Code != tc.want {
				t.Fatalf("ok=%v status=%d body=%s", ok, w.Code, w.Body)
			}
		})
	}
}

func TestAuthRateLimit(t *testing.T) {
	a := &API{authLimit: rate.NewLimiter(0, 1)}
	h := a.limit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/", nil))
	if w.Code != 204 {
		t.Fatal("initial request was rejected")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/", nil))
	if w.Code != 429 || w.Header().Get("Retry-After") != "1" {
		t.Fatal("rate limit not enforced")
	}
}

func TestValidation(t *testing.T) {
	for _, email := range []string{"not-email", "Name <a@example.com>", "a@example.com\x00"} {
		if _, ok := emailAddress(email); ok {
			t.Fatalf("accepted %q", email)
		}
	}
	if email, ok := emailAddress(" A@EXAMPLE.COM "); !ok || email != "a@example.com" {
		t.Fatalf("email=%q ok=%v", email, ok)
	}
	if _, ok := text(strings.Repeat("я", 80), 80); !ok {
		t.Fatal("counted bytes instead of characters")
	}
	if _, ok := text("   ", 80); ok {
		t.Fatal("accepted blank text")
	}
	if _, ok := text("a\x00b", 80); ok {
		t.Fatal("accepted a PostgreSQL-incompatible NUL")
	}
	in := domain.TaskInput{Title: "  task  "}
	if !validTask(&in) || in.Title != "task" {
		t.Fatal("valid task rejected")
	}
	in.AssigneeID = new(int64)
	if validTask(&in) {
		t.Fatal("accepted zero assignee ID")
	}
}

func TestPagination(t *testing.T) {
	for _, tc := range []struct {
		query         string
		ok            bool
		limit, offset int
	}{
		{"", true, 20, 0}, {"?limit=100&offset=20", true, 100, 20},
		{"?limit=0", false, 0, 0}, {"?limit=101", false, 0, 0}, {"?offset=-1", false, 0, 0},
		{"?offset=1000001", false, 0, 0}, {"?limit=x", false, 0, 0}, {"?offset=", false, 0, 0},
	} {
		w := httptest.NewRecorder()
		limit, offset, ok := pagination(w, httptest.NewRequest("GET", "/"+tc.query, nil))
		if ok != tc.ok || (ok && (limit != tc.limit || offset != tc.offset)) {
			t.Fatalf("%s: %d %d %v", tc.query, limit, offset, ok)
		}
	}
}

func TestMiddleware(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &API{log: log, tokens: auth.New(strings.Repeat("s", 32))}
	called := false
	h := a.middleware(a.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		write(w, 200, map[string]int64{"id": userID(r)})
	})))
	for _, header := range []string{"", "Basic abc", "Bearer broken", "Bearer a b"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Authorization", header)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 || called || w.Header().Get("X-Request-ID") == "" {
			t.Fatalf("unauthorized response: %d", w.Code)
		}
	}
	token, err := a.tokens.Issue(12)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !called || !strings.Contains(w.Body.String(), `"id":12`) {
		t.Fatal(w.Body.String())
	}

	w = httptest.NewRecorder()
	a.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("test") })).ServeHTTP(w, r)
	var body any
	if w.Code != 500 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
		t.Fatal("panic was not converted to JSON 500")
	}
}
