package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"
)

type ctxKey int

const visitorKey ctxKey = iota

const visitorCookie = "visitor_id"

// withVisitor gives every browser a random, persistent anonymous ID used to
// scope bookmarks without requiring an account.
func (s *Server) withVisitor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var id string
		if c, err := r.Cookie(visitorCookie); err == nil && validVisitorID(c.Value) {
			id = c.Value
		} else {
			id = newVisitorID()
			http.SetCookie(w, &http.Cookie{
				Name:     visitorCookie,
				Value:    id,
				Path:     "/",
				MaxAge:   int((2 * 365 * 24 * time.Hour).Seconds()),
				HttpOnly: true,
				Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
				SameSite: http.SameSiteLaxMode,
			})
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), visitorKey, id)))
	})
}

func visitorID(r *http.Request) string {
	return r.Context().Value(visitorKey).(string)
}

func newVisitorID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func validVisitorID(v string) bool {
	if len(v) != 32 {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; object-src 'none'; base-uri 'self'; form-action 'self'")
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

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.logger.Info("request", "method", r.Method, "path", r.URL.Path, "query", r.URL.RawQuery, "htmx", r.Header.Get("HX-Request") == "true", "status", rec.status, "duration", time.Since(start))
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				if err == http.ErrAbortHandler {
					panic(err)
				}
				s.logger.Error("panic", "err", err, "path", r.URL.Path)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
