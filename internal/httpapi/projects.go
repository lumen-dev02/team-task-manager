package httpapi

import (
	"net/http"
	"strings"
	"unicode/utf8"
)

func (a *API) createProject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decode(w, r, &in) {
		return
	}
	name, ok := text(in.Name, 120)
	if !ok || strings.ContainsRune(in.Description, '\x00') || utf8.RuneCountInString(in.Description) > 4000 {
		problem(w, 400, "invalid_input", "Project name: 1..120 characters; description: up to 4000")
		return
	}
	p, err := a.store.CreateProject(r.Context(), userID(r), name, strings.TrimSpace(in.Description))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 201, p)
}

func (a *API) projects(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := pagination(w, r)
	if !ok {
		return
	}
	items, err := a.store.Projects(r.Context(), userID(r), limit, offset)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, items)
}

func (a *API) project(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	p, err := a.store.Project(r.Context(), id, userID(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, p)
}

func (a *API) deleteProject(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if err := a.store.DeleteProject(r.Context(), id, userID(r)); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func (a *API) members(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	items, err := a.store.Members(r.Context(), id, userID(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, items)
}

func (a *API) addMember(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	var in struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &in) {
		return
	}
	email, ok := emailAddress(in.Email)
	if !ok {
		problem(w, 400, "invalid_input", "Valid email required")
		return
	}
	if err := a.store.AddMember(r.Context(), id, userID(r), email); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func (a *API) removeMember(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	memberID, ok := pathID(w, r, "memberID")
	if !ok {
		return
	}
	if err := a.store.RemoveMember(r.Context(), id, userID(r), memberID); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}
