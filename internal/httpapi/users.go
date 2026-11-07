package httpapi

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/lumen-dev02/team-task-manager/internal/auth"
	"github.com/lumen-dev02/team-task-manager/internal/domain"
)

func emailAddress(raw string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(raw))
	parsed, err := mail.ParseAddress(email)
	return email, err == nil && parsed.Address == email && len(email) <= 254
}

func text(raw string, max int) (string, bool) {
	s := strings.TrimSpace(raw)
	return s, s != "" && !strings.ContainsRune(s, '\x00') && utf8.RuneCountInString(s) <= max
}

func (a *API) register(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	email, emailOK := emailAddress(in.Email)
	name, nameOK := text(in.Name, 80)
	if !emailOK || !nameOK || len(in.Password) < 12 || len(in.Password) > 72 {
		problem(w, 400, "invalid_input", "Valid email, name (1..80 characters), password (12..72 bytes) required")
		return
	}
	hash, err := auth.Hash(in.Password)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	u, err := a.store.CreateUser(r.Context(), email, name, hash)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 201, u)
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	email, ok := emailAddress(in.Email)
	if !ok || len(in.Password) > 72 || len(in.Password) == 0 {
		problem(w, 401, "invalid_credentials", "Invalid email or password")
		return
	}
	u, err := a.store.UserByEmail(r.Context(), email)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		a.fail(w, r, err)
		return
	}
	hash := u.PasswordHash
	if errors.Is(err, domain.ErrNotFound) {
		hash = a.dummyHash
	}
	matched := auth.Match(hash, in.Password)
	if !matched || err != nil {
		problem(w, 401, "invalid_credentials", "Invalid email or password")
		return
	}
	token, err := a.tokens.Issue(u.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, map[string]any{"access_token": token, "token_type": "Bearer", "expires_in": int(auth.TokenTTL.Seconds())})
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	u, err := a.store.User(r.Context(), userID(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, u)
}
