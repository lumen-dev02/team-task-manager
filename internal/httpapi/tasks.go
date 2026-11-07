package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/lumen-dev02/team-task-manager/internal/domain"
)

func validStatus(s string) bool { return s == "todo" || s == "in_progress" || s == "done" }

func validTask(in *domain.TaskInput) bool {
	title, ok := text(in.Title, 200)
	in.Title = title
	in.Description = strings.TrimSpace(in.Description)
	return ok && !strings.ContainsRune(in.Description, '\x00') && utf8.RuneCountInString(in.Description) <= 10000 && (in.AssigneeID == nil || *in.AssigneeID > 0)
}

func (a *API) createTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	var in struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		AssigneeID  *int64 `json:"assignee_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	task := domain.TaskInput{Title: in.Title, Description: in.Description, AssigneeID: in.AssigneeID}
	if !validTask(&task) {
		problem(w, 400, "invalid_input", "Title: 1..200 characters; description: up to 10000; assignee_id: positive ID or null")
		return
	}
	t, err := a.store.CreateTask(r.Context(), id, userID(r), task)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 201, t)
}

func (a *API) tasks(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	limit, offset, ok := pagination(w, r)
	if !ok {
		return
	}
	f := domain.TaskFilter{Status: r.URL.Query().Get("status"), Limit: limit, Offset: offset}
	if f.Status != "" && !validStatus(f.Status) {
		problem(w, 400, "invalid_input", "Unknown status")
		return
	}
	if value, present := r.URL.Query()["assignee_id"]; present {
		assignee, err := strconv.ParseInt(value[0], 10, 64)
		if err != nil || assignee <= 0 {
			problem(w, 400, "invalid_input", "assignee_id must be positive")
			return
		}
		f.AssigneeID = &assignee
	}
	items, err := a.store.Tasks(r.Context(), id, userID(r), f)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, items)
}

func (a *API) task(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	taskID, ok := pathID(w, r, "taskID")
	if !ok {
		return
	}
	t, err := a.store.Task(r.Context(), id, userID(r), taskID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, t)
}

func (a *API) updateTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	taskID, ok := pathID(w, r, "taskID")
	if !ok {
		return
	}
	var in domain.TaskInput
	if !decode(w, r, &in) {
		return
	}
	if !validTask(&in) || !validStatus(in.Status) || in.Version <= 0 {
		problem(w, 400, "invalid_input", "Valid title, description, status, assignee_id and current version required")
		return
	}
	t, err := a.store.UpdateTask(r.Context(), id, userID(r), taskID, in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, t)
}

func (a *API) deleteTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	taskID, ok := pathID(w, r, "taskID")
	if !ok {
		return
	}
	if err := a.store.DeleteTask(r.Context(), id, userID(r), taskID); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func (a *API) history(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	taskID, ok := pathID(w, r, "taskID")
	if !ok {
		return
	}
	limit, offset, ok := pagination(w, r)
	if !ok {
		return
	}
	items, err := a.store.History(r.Context(), id, userID(r), taskID, limit, offset)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, items)
}
