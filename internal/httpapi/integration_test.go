package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lumen-dev02/team-task-manager/internal/auth"
	"github.com/lumen-dev02/team-task-manager/internal/domain"
	"github.com/lumen-dev02/team-task-manager/internal/store"
	"github.com/lumen-dev02/team-task-manager/migrations"
)

// Use a fresh PostgreSQL schema per test run; existing application data is untouched.
func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is unset; PostgreSQL integration tests skipped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := "test_" + strings.ToLower(rand.Text())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err = migrations.Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = migrations.Up(ctx, db); err != nil {
		t.Fatalf("migration not idempotent: %v", err)
	}
	return db
}

func request(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func expect(t *testing.T, h http.Handler, method, path, token, body string, status int, out any) {
	t.Helper()
	w := request(h, method, path, token, body)
	if w.Code != status {
		t.Fatalf("%s %s: want %d, got %d: %s", method, path, status, w.Code, w.Body)
	}
	if out != nil {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v", w.Body, err)
		}
	}
}

func TestIntegration(t *testing.T) {
	db := testDB(t)
	s := store.New(db)
	tokens := auth.New(strings.Repeat("s", 32))
	h := New(s, tokens, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	var owner domain.User
	expect(t, h, "POST", "/api/v1/auth/register", "", `{"email":"OWNER@example.com","name":"Owner","password":"long-owner-password"}`, 201, &owner)
	if owner.Email != "owner@example.com" {
		t.Fatal("email not normalized")
	}
	hash, err := auth.Hash("long-member-password")
	if err != nil {
		t.Fatal(err)
	}
	member, err := s.CreateUser(ctx, "member@example.com", "Member", hash)
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := s.CreateUser(ctx, "outsider@example.com", "Outsider", hash)
	if err != nil {
		t.Fatal(err)
	}
	var login struct {
		Token string `json:"access_token"`
	}
	expect(t, h, "POST", "/api/v1/auth/login", "", `{"email":"owner@example.com","password":"long-owner-password"}`, 200, &login)
	ownerToken := login.Token
	memberToken, err := tokens.Issue(member.ID)
	if err != nil {
		t.Fatal(err)
	}
	outsiderToken, err := tokens.Issue(outsider.ID)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, h, "POST", "/api/v1/auth/login", "", `{"email":"owner@example.com","password":"incorrect"}`, 401, nil)
	expect(t, h, "POST", "/api/v1/auth/register", "", `{"email":"owner@example.com","name":"Duplicate","password":"long-owner-password"}`, 409, nil)
	expect(t, h, "GET", "/api/v1/me", ownerToken, "", 200, nil)
	expect(t, h, "GET", "/api/v1/projects", "", "", 401, nil)
	expect(t, h, "GET", "/healthz", "", "", 200, nil)
	expect(t, h, "GET", "/readyz", "", "", 200, nil)

	var project domain.Project
	expect(t, h, "POST", "/api/v1/projects", ownerToken, `{"name":"Coursework","description":"Team planning"}`, 201, &project)
	base := fmt.Sprintf("/api/v1/projects/%d", project.ID)
	var members []domain.Member
	expect(t, h, "GET", base+"/members", ownerToken, "", 200, &members)
	if len(members) != 1 || members[0].Role != "owner" {
		t.Fatalf("members=%+v", members)
	}
	expect(t, h, "POST", base+"/members", ownerToken, `{"email":"member@example.com"}`, 204, nil)
	expect(t, h, "POST", base+"/members", ownerToken, `{"email":"member@example.com"}`, 409, nil)
	expect(t, h, "POST", base+"/members", ownerToken, `{"email":"missing@example.com"}`, 404, nil)
	expect(t, h, "POST", base+"/members", memberToken, `{"email":"outsider@example.com"}`, 403, nil)
	expect(t, h, "DELETE", base, memberToken, "", 403, nil)
	expect(t, h, "DELETE", fmt.Sprintf("%s/members/%d", base, owner.ID), ownerToken, "", 400, nil)
	var projects []domain.Project
	expect(t, h, "GET", "/api/v1/projects", memberToken, "", 200, &projects)
	if len(projects) != 1 {
		t.Fatalf("member projects=%+v", projects)
	}
	expect(t, h, "GET", "/api/v1/projects", outsiderToken, "", 200, &projects)
	if len(projects) != 0 {
		t.Fatal("private project leaked")
	}

	var task domain.Task
	expect(t, h, "POST", base+"/tasks", memberToken, fmt.Sprintf(`{"title":"Implement API","assignee_id":%d}`, member.ID), 201, &task)
	if task.Version != 1 || task.Status != "todo" || task.CreatedBy != member.ID {
		t.Fatalf("task=%+v", task)
	}
	taskPath := fmt.Sprintf("%s/tasks/%d", base, task.ID)
	update := fmt.Sprintf(`{"title":"Implement API","description":"With tests","status":"in_progress","assignee_id":%d,"version":1}`, member.ID)
	expect(t, h, "PUT", taskPath, ownerToken, update, 200, &task)
	if task.Version != 2 || task.Status != "in_progress" {
		t.Fatalf("task=%+v", task)
	}
	expect(t, h, "PUT", taskPath, memberToken, update, 409, nil)
	var history []domain.StatusChange
	expect(t, h, "GET", taskPath+"/history", memberToken, "", 200, &history)
	if len(history) != 2 || history[0].FromStatus != nil || history[1].FromStatus == nil || *history[1].FromStatus != "todo" || history[1].ChangedBy != owner.ID {
		t.Fatalf("history=%+v", history)
	}
	var tasks []domain.Task
	expect(t, h, "GET", base+fmt.Sprintf("/tasks?status=in_progress&assignee_id=%d&limit=1", member.ID), memberToken, "", 200, &tasks)
	if len(tasks) != 1 {
		t.Fatalf("filtered tasks=%+v", tasks)
	}
	expect(t, h, "GET", base+"/tasks?status=done", memberToken, "", 200, &tasks)
	if len(tasks) != 0 {
		t.Fatal("status filter ignored")
	}
	expect(t, h, "GET", base+"/tasks?status=invalid", memberToken, "", 400, nil)
	expect(t, h, "GET", base+"/tasks?assignee_id=0", memberToken, "", 400, nil)
	expect(t, h, "GET", base+"/tasks?offset=-1", memberToken, "", 400, nil)
	expect(t, h, "POST", base+"/tasks", ownerToken, fmt.Sprintf(`{"title":"Bad assignment","assignee_id":%d}`, outsider.ID), 400, nil)
	expect(t, h, "POST", base+"/tasks", ownerToken, `{"title":"   "}`, 400, nil)
	expect(t, h, "PUT", taskPath, ownerToken, `{"title":"test","status":"invalid","version":2}`, 400, nil)

	for _, tc := range []struct{ method, path, body string }{
		{"GET", base, ""}, {"GET", base + "/members", ""}, {"GET", base + "/tasks", ""},
		{"GET", taskPath, ""}, {"GET", taskPath + "/history", ""},
		{"POST", base + "/tasks", `{"title":"Intrusion"}`},
		{"PUT", taskPath, update}, {"DELETE", taskPath, ""}, {"DELETE", base, ""},
		{"POST", base + "/members", `{"email":"outsider@example.com"}`},
	} {
		expect(t, h, tc.method, tc.path, outsiderToken, tc.body, 404, nil)
	}

	// A task ID from another project cannot bypass the nested project boundary.
	var second domain.Project
	expect(t, h, "POST", "/api/v1/projects", ownerToken, `{"name":"Other project"}`, 201, &second)
	wrong := fmt.Sprintf("/api/v1/projects/%d/tasks/%d", second.ID, task.ID)
	expect(t, h, "GET", wrong, ownerToken, "", 404, nil)
	expect(t, h, "PUT", wrong, ownerToken, update, 404, nil)
	expect(t, h, "DELETE", wrong, ownerToken, "", 404, nil)

	t.Run("concurrent updates", func(t *testing.T) {
		body := fmt.Sprintf(`{"title":"Concurrent","status":"done","version":%d}`, task.Version)
		codes := make(chan int, 2)
		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() { codes <- request(h, "PUT", taskPath, ownerToken, body).Code })
		}
		wg.Wait()
		close(codes)
		counts := map[int]int{}
		for code := range codes {
			counts[code]++
		}
		if counts[200] != 1 || counts[409] != 1 {
			t.Fatalf("codes=%v", counts)
		}
		expect(t, h, "GET", taskPath+"/history", ownerToken, "", 200, &history)
		if len(history) != 3 {
			t.Fatal("conflicting update changed history")
		}
	})

	expect(t, h, "GET", taskPath, ownerToken, "", 200, &task)
	beforeRemoval := task.Version
	reassign := fmt.Sprintf(`{"title":"Assigned again","status":"done","assignee_id":%d,"version":%d}`, member.ID, task.Version)
	expect(t, h, "PUT", taskPath, ownerToken, reassign, 200, &task)
	expect(t, h, "GET", taskPath+"/history", ownerToken, "", 200, &history)
	if len(history) != 3 {
		t.Fatal("non-status update created a history event")
	}
	expect(t, h, "DELETE", fmt.Sprintf("%s/members/%d", base, member.ID), ownerToken, "", 204, nil)
	expect(t, h, "GET", taskPath, ownerToken, "", 200, &task)
	if task.AssigneeID != nil {
		t.Fatal("removed member still assigned")
	}
	if task.Version != beforeRemoval+2 {
		t.Fatal("unassignment did not increment task version")
	}
	expect(t, h, "PUT", taskPath, ownerToken, reassign, 409, nil)
	expect(t, h, "GET", base, memberToken, "", 404, nil)
	expect(t, h, "GET", taskPath, memberToken, "", 404, nil)
	expect(t, h, "DELETE", taskPath, ownerToken, "", 204, nil)
	expect(t, h, "GET", taskPath+"/history", ownerToken, "", 404, nil)
	// Deleting a project with assigned tasks must cascade without breaking the composite FK.
	expect(t, h, "POST", base+"/tasks", ownerToken, fmt.Sprintf(`{"title":"Cascade test","assignee_id":%d}`, owner.ID), 201, &task)
	expect(t, h, "DELETE", base, ownerToken, "", 204, nil)
	expect(t, h, "GET", base, ownerToken, "", 404, nil)
	var remaining int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM task_status_history WHERE task_id=$1`, task.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatal("project deletion left task history behind")
	}
}

func TestIntegrationMembershipRace(t *testing.T) {
	db := testDB(t)
	s := store.New(db)
	ctx := context.Background()
	owner, err := s.CreateUser(ctx, "owner@example.com", "Owner", "not-used-for-login")
	if err != nil {
		t.Fatal(err)
	}
	member, err := s.CreateUser(ctx, "member@example.com", "Member", "not-used-for-login")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, owner.ID, "Race test", "")
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if err = s.AddMember(ctx, p.ID, owner.ID, member.Email); err != nil {
			t.Fatal(err)
		}
		var task domain.Task
		var createErr, removeErr error
		var wg sync.WaitGroup
		wg.Go(func() {
			task, createErr = s.CreateTask(ctx, p.ID, owner.ID, domain.TaskInput{Title: "Concurrent assignment", AssigneeID: &member.ID})
		})
		wg.Go(func() { removeErr = s.RemoveMember(ctx, p.ID, owner.ID, member.ID) })
		wg.Wait()
		if removeErr != nil {
			t.Fatal(removeErr)
		}
		if createErr != nil && createErr != domain.ErrInvalid {
			t.Fatal(createErr)
		}
		if createErr == nil {
			current, err := s.Task(ctx, p.ID, owner.ID, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.AssigneeID != nil {
				t.Fatal("race left a non-member assigned")
			}
		}
	}
}

func TestIntegrationMigrationChecksum(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `UPDATE schema_migrations SET checksum='changed' WHERE version=1`); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(ctx, db); err == nil {
		t.Fatal("accepted modified applied migration")
	}
}
