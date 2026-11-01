package server

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	sessionCookie  = "session"
	sessionTTL     = 30 * 24 * time.Hour
	minPasswordLen = 8
	maxPasswordLen = 72
	maxEmailLen    = 254
)

// passwordIterations is the PBKDF2 work factor for new hashes. The count is
// stored in each hash, so it can be raised later without breaking logins.
var passwordIterations = 600_000

var b64 = base64.RawStdEncoding

// hashPassword returns "pbkdf2-sha256$<iterations>$<salt>$<hash>".
func hashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	rand.Read(salt)
	key, err := pbkdf2.Key(sha256.New, pw, salt, passwordIterations, sha256.Size)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", passwordIterations, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

func checkPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 {
		return false
	}
	salt, err := b64.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := b64.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func isSecure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// startSession logs the user in. Only a hash of the token is stored, so a
// database leak doesn't hand out live sessions.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	b := make([]byte, 32)
	rand.Read(b)
	token := base64.RawURLEncoding.EncodeToString(b)
	expires := time.Now().Add(sessionTTL)
	if err := s.store.CreateSession(r.Context(), userID, hashToken(token), expires); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (s *Server) endSession(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if err := s.store.DeleteSession(r.Context(), hashToken(c.Value)); err != nil {
			return err
		}
	}
	clearCookie(w, r, sessionCookie, "/")
	return nil
}

func clearCookie(w http.ResponseWriter, r *http.Request, name, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func validateCredentials(email, password string) map[string]string {
	errs := map[string]string{}
	at := strings.LastIndex(email, "@")
	switch {
	case email == "":
		errs["email"] = "Email is required."
	case len(email) > maxEmailLen || at < 1 || at == len(email)-1 || strings.ContainsFunc(email, unicode.IsSpace):
		errs["email"] = "Enter a valid email address."
	}
	switch n := utf8.RuneCountInString(password); {
	case n < minPasswordLen:
		errs["password"] = fmt.Sprintf("Password must be at least %d characters.", minPasswordLen)
	case n > maxPasswordLen:
		errs["password"] = fmt.Sprintf("Password must be %d characters or fewer.", maxPasswordLen)
	}
	return errs
}

// safeNext only allows redirects to local paths, so login links can't be
// used to bounce users to another site.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, `/\`) {
		return "/"
	}
	if strings.ContainsFunc(next, func(r rune) bool { return r == '\\' || unicode.IsControl(r) }) {
		return "/"
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	return next
}
