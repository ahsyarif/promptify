package server

import (
	"errors"
	"net/http"

	"github.com/ahsyarif/promptify/internal/store"
)

const maxAuthFormBytes = 64 << 10

const msgBadCredentials = "Incorrect email or password."

func (s *Server) handleSignupPage(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.renderAuth(w, r, http.StatusOK, "signup", viewData{Next: safeNext(r.URL.Query().Get("next"))})
}

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthFormBytes)
	email := normalizeEmail(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	next := safeNext(r.PostFormValue("next"))

	errs := validateCredentials(email, password)
	if len(errs) == 0 {
		hash, err := hashPassword(password)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		u, err := s.store.CreateUser(r.Context(), email, hash)
		switch {
		case errors.Is(err, store.ErrConflict):
			errs["email"] = "An account with this email already exists."
		case err != nil:
			s.serverError(w, r, err)
			return
		default:
			if err := s.startSession(w, r, u.ID); err != nil {
				s.serverError(w, r, err)
				return
			}
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
	}
	s.renderAuth(w, r, http.StatusUnprocessableEntity, "signup", viewData{Email: email, Next: next, Errors: errs})
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	data := viewData{Next: safeNext(r.URL.Query().Get("next"))}
	if r.URL.Query().Get("error") == "google" {
		data.Notice = "Google sign-in didn't work. Please try again."
	}
	s.renderAuth(w, r, http.StatusOK, "login", data)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthFormBytes)
	email := normalizeEmail(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	next := safeNext(r.PostFormValue("next"))

	u, err := s.store.UserByEmail(r.Context(), email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, r, err)
		return
	}
	var msg string
	switch {
	case err != nil:
		msg = msgBadCredentials
	case !u.HasPassword():
		msg = "This account uses Google sign-in."
	case !checkPassword(u.PasswordHash, password):
		msg = msgBadCredentials
	}
	if msg != "" {
		s.renderAuth(w, r, http.StatusUnprocessableEntity, "login", viewData{Email: email, Next: next, FormError: msg})
		return
	}
	if err := s.startSession(w, r, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.endSession(w, r); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) renderAuth(w http.ResponseWriter, r *http.Request, status int, page string, form viewData) {
	title := "Log in"
	if page == "signup" {
		title = "Create an account"
	}
	data, err := s.baseData(r, title, page)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data.Email, data.Next, data.Notice, data.Errors, data.FormError = form.Email, form.Next, form.Notice, form.Errors, form.FormError
	s.render(w, status, page, data)
}
